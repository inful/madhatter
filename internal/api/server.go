package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/database/sqlc"
	"github.com/inful/madhatter/internal/holiday"
	"github.com/inful/madhatter/internal/notify"
	"github.com/inful/madhatter/internal/notify/channels"
	emailchannel "github.com/inful/madhatter/internal/notify/channels/email"
	logchannel "github.com/inful/madhatter/internal/notify/channels/log"
	"github.com/inful/madhatter/internal/rota"
	"github.com/inful/madhatter/internal/wfh"
)

const (
	lookaheadDays          = 365
	calendarDaysLookahead  = 30
	serverReadTimeout      = 15 * time.Second
	serverWriteTimeout     = 15 * time.Second
	serverIdleTimeout      = 60 * time.Second
	sessionCleanupInterval = 1 * time.Hour  // Clean up expired sessions every hour.
	leaveCleanupInterval   = 24 * time.Hour // Clean up expired leave records once a day.
	shutdownTimeout        = 30 * time.Second
)

type Server struct {
	router         *chi.Mux
	api            huma.API
	db             *database.DB
	engine         *rota.Engine
	authManager    *auth.AuthManager
	authMiddleware *auth.Middleware
	sessionManager *auth.SessionManager
	holidayService *holiday.Service
	wfhService     *wfh.Service
	wfhScheduler   *wfh.Scheduler
	notifier       *notify.ChannelNotifier
	notifyWorker   *notify.Worker
	//nolint:containedctx // Context is used for graceful shutdown
	cleanupCtx    context.Context
	cleanupCancel context.CancelFunc
}

func NewServer(db *database.DB, development bool) (*Server, error) {
	router := chi.NewRouter()

	// Setup authentication components
	authManager, authMiddleware, sessionManager, err := setupAuth(db, development)
	if err != nil {
		return nil, err
	}

	// Initialize holiday service
	holidayService, err := holiday.InitializeHolidayService(db)
	if err != nil {
		log.Printf("Warning: Failed to initialize holiday service: %v\n", err)
		// Continue without holiday service - it's optional
		holidayService = nil
	}

	// Wire the holiday checker into the DB so any feature that needs to
	// reject state on holidays (e.g. WFH requests) can consult it.
	if holidayService != nil {
		db.SetHolidayChecker(holidayService.ShouldSkipDate)
	}

	// Create engine and set holiday checker
	engine := rota.NewEngine(db)
	if holidayService != nil {
		engine.SetHolidayChecker(holidayService.ShouldSkipDate)
	}

	s := &Server{
		router:         router,
		db:             db,
		engine:         engine,
		authManager:    authManager,
		authMiddleware: authMiddleware,
		sessionManager: sessionManager,
		holidayService: holidayService,
	}

	// Initialize WFH service.
	s.setupWFHService(db)

	// Build the notification system. In --development mode (or when
	// NOTIFY_EMAIL_ENABLED is false), only a LogChannel is registered;
	// every "send" writes to slog and the worker is a no-op. In
	// production the email channel is registered and the worker
	// drains the outbox into SMTP. The notifier itself is always
	// installed, so handlers can call it unconditionally.
	if err := s.setupNotifier(db); err != nil {
		return nil, err
	}

	// Apply authentication middleware to the router BEFORE creating HUMA API
	// This ensures all routes (including HUMA-registered ones) go through auth
	if s.authMiddleware != nil {
		// Use OptionalAuth so API endpoints can check auth status in handlers
		router.Use(s.authMiddleware.OptionalAuth)
	}

	// Create HUMA API with Chi adapter and security scheme documentation
	s.api = humachi.New(router, buildHumaConfig())

	// Register all operations
	s.registerOperations(development)
	if err := s.registerWebRoutes(development); err != nil {
		return nil, err
	}

	return s, nil
}

// buildHumaConfig returns the HUMA config with the OpenAPI security
// schemes documented. Extracted to keep NewServer's cyclomatic
// complexity below the lint limit.
func buildHumaConfig() huma.Config {
	config := huma.DefaultConfig("Support Rota API", "1.0.0")
	config.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		// Session-based authentication (web interface)
		"sessionAuth": {
			Type:        "apiKey",
			In:          "cookie",
			Name:        "session_token",
			Description: "Session-based authentication using secure cookies. Used for web interface authentication.",
		},
		// Token-based authentication (API access)
		"apiTokenAuth": {
			Type:         "http",
			Scheme:       "bearer",
			BearerFormat: "API Token",
			Description:  "Token-based authentication using Bearer tokens. Generated via /api/v1/tokens/generate endpoint.",
		},
	}
	return config
}

// setupWFHService initializes the WFH service and scheduler when
// WFH is enabled in config. Failures to start the scheduler are
// logged but do not abort server construction. Extracted from
// NewServer for cyclomatic complexity.
func (s *Server) setupWFHService(db *database.DB) {
	wfhCfg := wfh.LoadConfigFromEnv()
	if !wfhCfg.Enabled {
		return
	}
	s.wfhService = wfh.NewService(db, wfhCfg)
	s.wfhScheduler = wfh.NewScheduler(s.wfhService)
	if startErr := s.wfhScheduler.Start(); startErr != nil {
		log.Printf("Warning: Failed to start WFH scheduler: %v\n", startErr)
	}
}

// setupNotifier builds the notification system and wires it into
// the consumers. In --development mode (or when
// NOTIFY_EMAIL_ENABLED is false), only a LogChannel is registered;
// in production the email channel is registered and the worker
// drains the outbox into SMTP. Returns the underlying error
// (wrapped) when building fails so NewServer can fail fast.
func (s *Server) setupNotifier(db *database.DB) error {
	notifier, worker, err := s.buildNotifier(db)
	if err != nil {
		return fmt.Errorf("build notifier: %w", err)
	}
	s.notifier = notifier
	s.notifyWorker = worker

	if s.wfhService != nil {
		s.wfhService.SetNotifier(s.notifier)
	}
	if s.engine != nil {
		s.engine.SetNotifier(rotaCoverAdapter{inner: s.notifier})
	}
	return nil
}

func (s *Server) setupSessionCleanup(ctx context.Context) {
	cleanupCtx, cleanupCancel := context.WithCancel(ctx)
	s.cleanupCtx = cleanupCtx
	s.cleanupCancel = cleanupCancel

	// Start session cleanup background task (only if auth is enabled)
	if s.sessionManager != nil {
		log.Println("Starting session cleanup task...")
		//nolint:contextcheck // Cleanup context is properly managed and canceled in StopCleanup
		s.sessionManager.StartCleanup(s.cleanupCtx)
	} else {
		log.Println("Authentication disabled - skipping session cleanup")
	}
}

func (s *Server) newScheduleMaintenance() *rota.ScheduleMaintenance {
	maintenance := rota.NewScheduleMaintenance(s.db)
	if s.holidayService != nil {
		maintenance.SetHolidayChecker(s.holidayService.ShouldSkipDate)
	}

	return maintenance
}

// startLeaveCleanup starts a background goroutine that deletes expired leave records daily.
func (s *Server) startLeaveCleanup() {
	log.Println("Starting leave record cleanup task...")
	ticker := time.NewTicker(leaveCleanupInterval)
	go func() {
		defer ticker.Stop()
		// Run immediately on start.
		if err := s.db.DeleteExpiredLeaveRecords(s.cleanupCtx); err != nil {
			log.Printf("Leave cleanup error: %v\n", err)
		}
		for {
			select {
			case <-ticker.C:
				if err := s.db.DeleteExpiredLeaveRecords(s.cleanupCtx); err != nil {
					log.Printf("Leave cleanup error: %v\n", err)
				}
			case <-s.cleanupCtx.Done():
				return
			}
		}
	}()
}

func (s *Server) handleShutdownSignals(parentCtx context.Context, srv *http.Server) {
	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sigint:
		log.Println("Shutting down server...")
		s.stopCleanup()
		shutdownCtx, shutdownCancel := context.WithTimeout(parentCtx, shutdownTimeout)
		defer shutdownCancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("Server shutdown error: %v\n", err)
		}
	case <-parentCtx.Done():
		// Parent context canceled
		log.Println("Parent context canceled, shutting down server...")
		s.stopCleanup()
	}
}

func (s *Server) stopCleanup() {
	if s.cleanupCancel != nil {
		s.cleanupCancel()
	}
	if s.sessionManager != nil {
		s.sessionManager.StopCleanup()
	}
	if s.wfhScheduler != nil {
		s.wfhScheduler.Stop()
	}
	// The notifier worker is cancelled via s.cleanupCtx; nothing
	// more to do here. The goroutine exits when the context is
	// cancelled.
}

func (s *Server) Start(ctx context.Context, port string) error {
	s.setupSessionCleanup(ctx)
	s.startLeaveCleanup()
	if s.notifyWorker != nil {
		// Run the outbox worker under the same lifecycle context as
		// the rest of the background tasks; cancellation via
		// s.stopCleanup() (or parent ctx cancel) stops it cleanly.
		//nolint:contextcheck // cleanupCtx is properly cancelled in stopCleanup
		go s.notifyWorker.Run(s.cleanupCtx)
	}
	defer s.stopCleanup()

	// Use http.Server with timeouts for production
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      s.router,
		ReadTimeout:  serverReadTimeout,
		WriteTimeout: serverWriteTimeout,
		IdleTimeout:  serverIdleTimeout,
	}

	// Handle graceful shutdown
	go s.handleShutdownSignals(ctx, srv)

	log.Printf("Server starting on port %s\n", port)
	err := srv.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Holiday API Handlers

type GetHolidaysOutput struct {
	Body struct {
		Holidays []holiday.Holiday `json:"holidays"`
		Message  string            `json:"message"`
	}
}

func (s *Server) handleGetHolidays(ctx context.Context, input *struct{}) (*GetHolidaysOutput, error) {
	if s.holidayService == nil {
		return nil, huma.Error503ServiceUnavailable("Holiday service not available")
	}

	holidays := s.holidayService.GetUpcomingHolidays(lookaheadDays)

	resp := &GetHolidaysOutput{}
	resp.Body.Holidays = holidays
	resp.Body.Message = fmt.Sprintf("Found %d upcoming holidays", len(holidays))
	return resp, nil
}

type GetHolidayStatusOutput struct {
	Body struct {
		SchedulerRunning bool   `json:"scheduler_running"`
		LastFetch        string `json:"last_fetch,omitempty"`
		LastError        string `json:"last_error,omitempty"`
		URLCount         int    `json:"url_count"`
		HolidayCount     int    `json:"holiday_count"`
	}
}

func (s *Server) handleGetHolidayStatus(ctx context.Context, input *struct{}) (*GetHolidayStatusOutput, error) {
	if s.holidayService == nil {
		return nil, huma.Error503ServiceUnavailable("Holiday service not available")
	}

	status := s.holidayService.GetStatus()

	resp := &GetHolidayStatusOutput{}
	resp.Body.SchedulerRunning = status.SchedulerRunning
	if !status.LastFetch.IsZero() {
		resp.Body.LastFetch = status.LastFetch.Format("2006-01-02 15:04:05")
	}
	if status.LastError != nil {
		resp.Body.LastError = status.LastError.Error()
	}
	resp.Body.URLCount = status.URLCount
	resp.Body.HolidayCount = status.HolidayCount
	return resp, nil
}

type RefreshHolidaysOutput struct {
	Body struct {
		Message string `json:"message"`
		Success bool   `json:"success"`
	}
}

func (s *Server) handleRefreshHolidays(ctx context.Context, input *struct{}) (*RefreshHolidaysOutput, error) {
	// Check authentication
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	// Get user from context using middleware's context key
	userSession, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}

	// Check admin privileges
	if !auth.IsAdminSession(userSession) {
		return nil, huma.Error403Forbidden("Admin privileges required")
	}

	if s.holidayService == nil {
		return nil, huma.Error503ServiceUnavailable("Holiday service not available")
	}

	err := s.holidayService.ForceRefresh(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to refresh holidays", err)
	}

	resp := &RefreshHolidaysOutput{}
	resp.Body.Message = "Holiday refresh initiated successfully"
	resp.Body.Success = true
	return resp, nil
}

// API Token Input/Output Types

type GenerateAPITokenInput struct {
	Body struct {
		Name       string `json:"name" minLength:"1"`
		ExpiryDays int    `json:"expiry_days,omitempty" minimum:"1"`
	}
}

type GenerateAPITokenOutput struct {
	Body struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at,omitempty"`
		Message   string `json:"message"`
	}
}

type ListAPITokensOutput struct {
	Body struct {
		Tokens []APITokenInfo `json:"tokens"`
	}
}

type APITokenInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	IsActive   bool   `json:"is_active"`
	CreatedAt  string `json:"created_at"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	LastUsedAt string `json:"last_used_at,omitempty"`
}

type RevokeAPITokenInput struct {
	Path struct {
		ID string `doc:"Token ID to revoke" path:"id"`
	}
}

type RevokeAPITokenOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

type CleanupExpiredTokensOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

// handleGenerateAPIToken generates a new API token for the authenticated user.
func (s *Server) handleGenerateAPIToken(ctx context.Context, input *GenerateAPITokenInput) (*GenerateAPITokenOutput, error) {
	// Check if auth is enabled
	if s.authManager == nil || s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	// Get user from context using middleware's context key
	userSession, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}

	// Generate token
	token, err := generateSecureToken()
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to generate token", err)
	}

	// Hash token for storage (using hex encoding like session.go)
	hashedToken := hashTokenHex(token)

	// Generate unique ID for the token
	tokenID := uuid.New().String()

	// Calculate expiration if provided
	var expiresAt sql.NullTime
	if input.Body.ExpiryDays > 0 {
		expiresAt = sql.NullTime{
			Time:  time.Now().AddDate(0, 0, input.Body.ExpiryDays),
			Valid: true,
		}
	}

	// Store token in database
	_, err = s.db.GetQueries().CreateAPIToken(ctx, sqlc.CreateAPITokenParams{
		ID:        tokenID,
		UserID:    userSession.UserID,
		Name:      input.Body.Name,
		TokenHash: hashedToken,
		IsActive:  sql.NullInt64{Int64: 1, Valid: true},
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to store token", err)
	}

	// Return token to user (only time it's shown)
	resp := &GenerateAPITokenOutput{}
	resp.Body.Token = token
	if expiresAt.Valid {
		resp.Body.ExpiresAt = expiresAt.Time.Format("2006-01-02T15:04:05Z")
	}
	resp.Body.Message = "API token generated successfully. Save this token - it will not be shown again."
	return resp, nil
}

// convertTokenToInfo converts a database token to API response format.
func convertTokenToInfo(token sqlc.ApiToken) APITokenInfo {
	return APITokenInfo{
		ID:         token.ID,
		Name:       token.Name,
		IsActive:   token.IsActive.Valid && token.IsActive.Int64 == 1,
		CreatedAt:  formatNullableTime(token.CreatedAt),
		ExpiresAt:  formatNullableTime(token.ExpiresAt),
		LastUsedAt: formatNullableTime(token.LastUsedAt),
	}
}

// formatNullableTime formats a nullable time to ISO 8601 string.
func formatNullableTime(nullTime sql.NullTime) string {
	if nullTime.Valid {
		return nullTime.Time.Format("2006-01-02T15:04:05Z")
	}
	return ""
}

// handleListAPITokens lists all API tokens for the authenticated user.
func (s *Server) handleListAPITokens(ctx context.Context, input *struct{}) (*ListAPITokensOutput, error) {
	// Check if auth is enabled
	if s.authManager == nil || s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	// Get user from context using middleware's context key
	userSession, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}

	// Get tokens from database
	tokens, err := s.db.GetQueries().GetAPITokensByUser(ctx, userSession.UserID)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to get tokens", err)
	}

	// Convert to response format
	tokenInfos := make([]APITokenInfo, len(tokens))
	for i := range tokens {
		tokenInfos[i] = convertTokenToInfo(tokens[i])
	}

	resp := &ListAPITokensOutput{}
	resp.Body.Tokens = tokenInfos
	return resp, nil
}

// handleRevokeAPIToken revokes an API token.
func (s *Server) handleRevokeAPIToken(ctx context.Context, input *RevokeAPITokenInput) (*RevokeAPITokenOutput, error) {
	// Check if auth is enabled
	if s.authManager == nil || s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	// Get user from context using middleware's context key
	userSession, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}

	// Verify token belongs to user
	token, err := s.db.GetQueries().GetAPITokenByID(ctx, input.Path.ID)
	if err != nil {
		return nil, huma.Error404NotFound("Token not found")
	}

	if token.UserID != userSession.UserID {
		return nil, huma.Error403Forbidden("Not authorized")
	}

	// Delete token
	_, err = s.db.GetQueries().DeleteAPIToken(ctx, input.Path.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to delete token", err)
	}

	resp := &RevokeAPITokenOutput{}
	resp.Body.Message = "API token revoked successfully"
	return resp, nil
}

// handleCleanupExpiredTokens removes expired API tokens (admin only).
func (s *Server) handleCleanupExpiredTokens(ctx context.Context, input *struct{}) (*CleanupExpiredTokensOutput, error) {
	// Check if auth is enabled
	if s.authManager == nil || s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	// Get user from context using middleware's context key
	userSession, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}

	// Check admin privileges
	if !auth.IsAdminSession(userSession) {
		return nil, huma.Error403Forbidden("Admin privileges required")
	}

	// Cleanup expired tokens
	_, err := s.db.GetQueries().CleanupExpiredTokens(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to cleanup tokens", err)
	}

	resp := &CleanupExpiredTokensOutput{}
	resp.Body.Message = "Expired tokens cleaned up successfully"
	return resp, nil
}

// buildNotifier wires the notification system: a resolver that maps
// member_id to email, a renderer for templates, a worker that
// dispatches outbox rows to registered channels, and a ChannelNotifier
// that producer code calls. Returns the notifier and worker; the
// caller is responsible for starting the worker.
func (s *Server) buildNotifier(db *database.DB) (*notify.ChannelNotifier, *notify.Worker, error) {
	cfg := notify.LoadConfigFromEnv()
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}

	// Pick the channel set based on config.
	var chans []channels.Channel
	if cfg.Email.Enabled {
		ch := emailchannel.New(
			cfg.Email.Host,
			cfg.Email.From,
			cfg.Email.Identity,
			cfg.Email.User,
			cfg.Email.Password,
		)
		chans = append(chans, ch)
		log.Printf("notify: email channel registered (host=%s, from=%s)", cfg.Email.Host, cfg.Email.From)
	} else {
		// No email configured — register a log channel so handlers'
		// calls don't fail. This is the --development mode default.
		chans = append(chans, logchannel.New(nil))
		log.Println("notify: log channel registered (email disabled)")
	}

	// Build the worker. Its goroutine is started by Start().
	resolver := notify.NewStaticResolver(chans...)
	worker := notify.NewWorker(db, resolver, cfg.Outbox, nil)

	// Build the renderer. The unsubscribe URL factory is wired
	// when the server's signing secret is available; if the secret
	// is missing the renderer silently drops the link (templates
	// guard the footer on .UnsubscribeURL being non-empty).
	secret := os.Getenv("SESSION_SECRET")
	var unsubFn func(string) string
	if secret != "" {
		unsubFn = notify.UnsubscribeURLFactory(cfg.PublicBaseURL, secret)
	}
	r, err := notify.NewRenderer(cfg.BaseURL, unsubFn)
	if err != nil {
		return nil, nil, fmt.Errorf("build renderer: %w", err)
	}

	notifier := notify.NewChannelNotifier(
		db,
		dbRecipientResolver{db: db},
		r,
		worker,
		cfg.EnabledChannels,
		nil,
	)
	return notifier, worker, nil
}

// dbRecipientResolver resolves a member_id to an email address and
// display name by looking up team_members. Used by the production
// ChannelNotifier.
type dbRecipientResolver struct {
	db *database.DB
}

// ResolveByID implements notify.RecipientResolver.
func (r dbRecipientResolver) ResolveByID(ctx context.Context, memberID string) (string, string, error) {
	if memberID == "" {
		return "", "", errors.New("empty member id")
	}
	m, err := r.db.GetMemberByID(ctx, memberID)
	if err != nil {
		return "", "", err
	}
	if m == nil {
		return "", "", errors.New("member not found: " + memberID)
	}
	return m.Email, m.Name, nil
}

// EmailEnabled implements notify.RecipientResolver. Returns true
// when the member has not disabled email. A lookup error is
// returned to the caller, which then defaults to "enabled" (so a
// transient DB issue never silently drops notifications).
func (r dbRecipientResolver) EmailEnabled(ctx context.Context, memberID string) (bool, error) {
	if memberID == "" {
		return true, nil
	}
	return r.db.IsNotificationEmailEnabled(ctx, memberID)
}

// rotaCoverAdapter bridges rota.CoverNotifier (which uses a local
// CoverEvent) to notify.ChannelNotifier (which uses the production
// CoverEvent). The two structs have identical fields but live in
// different packages to avoid an import cycle.
type rotaCoverAdapter struct {
	inner *notify.ChannelNotifier
}

// CoverAssigned implements rota.CoverNotifier.
func (a rotaCoverAdapter) CoverAssigned(ctx context.Context, e rota.CoverEvent) {
	a.inner.CoverAssigned(ctx, notify.CoverEvent{
		LeaveID:         e.LeaveID,
		LeaveMemberID:   e.LeaveMemberID,
		LeaveMemberName: e.LeaveMemberName,
		CoverMemberID:   e.CoverMemberID,
		CoverMemberName: e.CoverMemberName,
		StartDate:       e.StartDate,
		EndDate:         e.EndDate,
		ResolvedBy:      e.ResolvedBy,
	})
}

// generateSecureToken generates a cryptographically secure API token.
func generateSecureToken() (string, error) {
	const tokenSize = 32
	bytes := make([]byte, tokenSize)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	// Add prefix to identify as Support Rota token
	return "srp_" + base64.URLEncoding.EncodeToString(bytes), nil
}

// hashTokenHex hashes a token using SHA-256 and returns hex encoding (same as session.go).
func hashTokenHex(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
