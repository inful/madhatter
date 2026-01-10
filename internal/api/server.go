package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
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
	"github.com/inful/madhatter/internal/rota"
	"github.com/inful/madhatter/internal/web"
)

const (
	lookaheadDays          = 365
	calendarDaysLookahead  = 30
	serverReadTimeout      = 15 * time.Second
	serverWriteTimeout     = 15 * time.Second
	serverIdleTimeout      = 60 * time.Second
	sessionCleanupInterval = 1 * time.Hour // Clean up expired sessions every hour
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

	// Apply authentication middleware to the router BEFORE creating HUMA API
	// This ensures all routes (including HUMA-registered ones) go through auth
	if s.authMiddleware != nil {
		// Use OptionalAuth so API endpoints can check auth status in handlers
		router.Use(s.authMiddleware.OptionalAuth)
	}

	// Create HUMA API with Chi adapter and security scheme documentation
	config := huma.DefaultConfig("Support Rota API", "1.0.0")

	// Configure OpenAPI security schemes
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
			Description:  "API token authentication using Bearer tokens. Generated via /api/v1/tokens/generate endpoint.",
		},
	}

	s.api = humachi.New(router, config)

	// Register all operations
	s.registerOperations(development)
	if err := s.registerWebRoutes(development); err != nil {
		return nil, err
	}

	return s, nil
}

func setupAuth(db *database.DB, development bool) (*auth.AuthManager, *auth.Middleware, *auth.SessionManager, error) {
	if development {
		return setupDevelopmentAuth(db)
	}
	return setupProductionAuth(db)
}

func setupDevelopmentAuth(db *database.DB) (*auth.AuthManager, *auth.Middleware, *auth.SessionManager, error) {
	log.Println("Development mode: Using fake OAuth provider")

	fakeConfig := auth.ProviderConfig{
		ClientID:     "dev-client-id",
		ClientSecret: "dev-client-secret",
		RedirectURL:  "http://localhost:8080/auth/callback?provider=fake",
		AuthURL:      "/auth/fake/login",
		TokenURL:     "/auth/fake/token",
		UserInfoURL:  "/auth/fake/userinfo",
		Scope:        "read:user",
	}

	encryptor, err := auth.NewTokenEncryptor()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create token encryptor: %w", err)
	}

	providerFactory := auth.NewProviderFactory(map[string]auth.ProviderConfig{
		"fake": fakeConfig,
	})
	userService := auth.NewUserService(db.GetQueries(), encryptor)
	sessionManager := auth.NewSessionManager(db.GetQueries(), auth.SessionExpiryDuration)

	authManager := auth.NewAuthManager(providerFactory, userService, sessionManager)
	authMiddleware := auth.NewMiddleware(sessionManager)

	fakeProvider := auth.NewFakeProvider(fakeConfig)
	authManager.RegisterProvider(fakeProvider)

	return authManager, authMiddleware, sessionManager, nil
}

func setupProductionAuth(db *database.DB) (*auth.AuthManager, *auth.Middleware, *auth.SessionManager, error) {
	authConfig := auth.LoadConfigFromEnv()

	if err := authConfig.Validate(); err != nil {
		log.Printf("WARNING: Authentication disabled - %v\n", err)
		log.Printf("The server will start without authentication. Features requiring login will be unavailable.\n")
		log.Printf("To enable authentication, configure OAuth provider environment variables.\n")
		return nil, nil, nil, nil
	}

	encryptor, err := auth.NewTokenEncryptor()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create token encryptor: %w", err)
	}

	providerFactory := auth.NewProviderFactory(authConfig.Providers)
	userService := auth.NewUserService(db.GetQueries(), encryptor)
	sessionManager := auth.NewSessionManager(db.GetQueries(), time.Duration(authConfig.Sessions.DurationHours)*time.Hour)

	authManager := auth.NewAuthManager(providerFactory, userService, sessionManager)
	authMiddleware := auth.NewMiddleware(sessionManager)

	for providerName := range authConfig.Providers {
		provider, err := providerFactory.Create(providerName)
		if err != nil {
			log.Printf("Failed to create auth provider %q: %v\n", providerName, err)
			continue
		}
		authManager.RegisterProvider(provider)
	}

	return authManager, authMiddleware, sessionManager, nil
}

func (s *Server) registerOperations(development bool) {
	// Team Operations
	huma.Register(s.api, huma.Operation{
		OperationID: "add-team-member",
		Method:      http.MethodPost,
		Path:        "/api/v1/team",
		Summary:     "Add a new team member",
		Tags:        []string{"Team"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleAddTeam)

	huma.Register(s.api, huma.Operation{
		OperationID: "list-team-members",
		Method:      http.MethodGet,
		Path:        "/api/v1/team",
		Summary:     "List all active team members",
		Tags:        []string{"Team"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleListTeam)

	// Leave Operations
	huma.Register(s.api, huma.Operation{
		OperationID: "report-leave",
		Method:      http.MethodPost,
		Path:        "/api/v1/leave",
		Summary:     "Report leave for a team member",
		Tags:        []string{"Leave"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleReportLeave)

	huma.Register(s.api, huma.Operation{
		OperationID: "list-leave",
		Method:      http.MethodGet,
		Path:        "/api/v1/leave",
		Summary:     "List leave records",
		Tags:        []string{"Leave"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleListLeave)

	// Schedule Operations
	huma.Register(s.api, huma.Operation{
		OperationID: "generate-schedule",
		Method:      http.MethodPost,
		Path:        "/api/v1/schedule/generate",
		Summary:     "Generate schedule for date range",
		Tags:        []string{"Schedule"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleGenerateSchedule)

	// Calendar Operations
	huma.Register(s.api, huma.Operation{
		OperationID: "subscribe-calendar",
		Method:      http.MethodPost,
		Path:        "/api/v1/calendar/subscribe",
		Summary:     "Create calendar subscription",
		Tags:        []string{"Calendar"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleSubscribeCalendar)

	// ICS Feed (no auth required - uses token in URL)
	s.router.Get("/api/v1/calendar/{token}/ics", s.handleCalendarICS)

	// Development mode fake auth routes
	if development && s.authManager != nil {
		fakeHandler := auth.NewFakeCallbackHandler()
		s.router.HandleFunc("/auth/fake/login", fakeHandler.HandleLogin)
	}

	// Holiday Operations
	huma.Register(s.api, huma.Operation{
		OperationID: "get-holidays",
		Method:      http.MethodGet,
		Path:        "/api/v1/holidays",
		Summary:     "Get upcoming holidays",
		Tags:        []string{"Holidays"},
		// Public endpoint - no auth required
	}, s.handleGetHolidays)

	huma.Register(s.api, huma.Operation{
		OperationID: "get-holiday-status",
		Method:      http.MethodGet,
		Path:        "/api/v1/holidays/status",
		Summary:     "Get holiday service status",
		Tags:        []string{"Holidays"},
		// Public endpoint - no auth required
	}, s.handleGetHolidayStatus)

	huma.Register(s.api, huma.Operation{
		OperationID: "refresh-holidays",
		Method:      http.MethodPost,
		Path:        "/api/v1/holidays/refresh",
		Summary:     "Manually refresh holiday data",
		Tags:        []string{"Holidays"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleRefreshHolidays)

	// API Token Operations
	huma.Register(s.api, huma.Operation{
		OperationID: "generate-api-token",
		Method:      http.MethodPost,
		Path:        "/api/v1/tokens/generate",
		Summary:     "Generate a new API token",
		Tags:        []string{"Authentication"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
		},
	}, s.handleGenerateAPIToken)

	huma.Register(s.api, huma.Operation{
		OperationID: "list-api-tokens",
		Method:      http.MethodGet,
		Path:        "/api/v1/tokens",
		Summary:     "List user's API tokens",
		Tags:        []string{"Authentication"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
		},
	}, s.handleListAPITokens)

	huma.Register(s.api, huma.Operation{
		OperationID: "revoke-api-token",
		Method:      http.MethodDelete,
		Path:        "/api/v1/tokens/{id}",
		Summary:     "Revoke an API token",
		Tags:        []string{"Authentication"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
		},
	}, s.handleRevokeAPIToken)

	huma.Register(s.api, huma.Operation{
		OperationID: "cleanup-expired-tokens",
		Method:      http.MethodPost,
		Path:        "/api/v1/tokens/cleanup",
		Summary:     "Cleanup expired API tokens (admin only)",
		Tags:        []string{"Authentication"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleCleanupExpiredTokens)
}

func (s *Server) registerWebRoutes(development bool) error {
	// Create web handler with auth components and holiday checker.
	var holidayChecker func(time.Time) bool
	if s.holidayService != nil {
		holidayChecker = s.holidayService.ShouldSkipDate
	}

	webHandler, err := web.NewHandler(s.db, s.authManager, s.authMiddleware, development, holidayChecker)
	if err != nil {
		return err
	}

	// Development mode: The web handler's registerDevelopmentRoutes will handle the fake login view.
	// No need to register it separately here.

	// Mount web routes.
	s.router.Mount("/", webHandler.Router())
	return nil
}

type AddTeamInput struct {
	Body struct {
		Name  string `json:"name" minLength:"1"`
		Email string `format:"email" json:"email"`
	}
}

type AddTeamOutput struct {
	Body struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
}

func (s *Server) handleAddTeam(ctx context.Context, input *AddTeamInput) (*AddTeamOutput, error) {
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
	if !userSession.IsAdmin.Valid || userSession.IsAdmin.Int64 == 0 {
		return nil, huma.Error403Forbidden("Admin privileges required")
	}

	id, err := s.db.AddTeamMember(ctx, input.Body.Name, input.Body.Email)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to add team member", err)
	}

	resp := &AddTeamOutput{}
	resp.Body.ID = id
	resp.Body.Message = "Team member added successfully"
	return resp, nil
}

type ListTeamOutput struct {
	Body struct {
		Members []database.TeamMember `json:"members"`
	}
}

func (s *Server) handleListTeam(ctx context.Context, input *struct{}) (*ListTeamOutput, error) {
	// Check authentication
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	// Get user from context using middleware's context key
	_, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}

	members, err := s.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to get team members", err)
	}

	resp := &ListTeamOutput{}
	resp.Body.Members = members
	return resp, nil
}

type ReportLeaveInput struct {
	Body struct {
		MemberID  string `json:"member_id"`
		Type      string `enum:"sick,vacation,other" json:"type"`
		StartDate string `format:"date" json:"start_date"`
		EndDate   string `format:"date" json:"end_date"`
	}
}

type ReportLeaveOutput struct {
	Body struct {
		LeaveID string `json:"leave_id"`
		Covers  []struct {
			Date   string `json:"date"`
			Member string `json:"member"`
		} `json:"covers"`
		Message string `json:"message"`
	}
}

func (s *Server) handleReportLeave(ctx context.Context, input *ReportLeaveInput) (*ReportLeaveOutput, error) {
	// Check authentication
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	// Get user from context using middleware's context key
	_, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}

	// Create leave record
	leaveID, err := s.db.CreateLeaveRecord(ctx, input.Body.MemberID, input.Body.Type, input.Body.StartDate, input.Body.EndDate)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to create leave record", err)
	}

	// Assign covers
	err = s.engine.AssignCoversForLeave(ctx, leaveID)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to assign covers", err)
	}

	// Get cover assignments for response
	covers := s.getCoversForLeave(ctx, leaveID, input.Body.StartDate, input.Body.EndDate)

	resp := &ReportLeaveOutput{}
	resp.Body.LeaveID = leaveID
	resp.Body.Covers = covers
	resp.Body.Message = "Leave reported and covers assigned"
	return resp, nil
}

func (s *Server) getCoversForLeave(ctx context.Context, leaveID string, startDateStr, endDateStr string) []struct {
	Date   string `json:"date"`
	Member string `json:"member"`
} {
	covers := []struct {
		Date   string `json:"date"`
		Member string `json:"member"`
	}{}

	startDate, _ := time.Parse("2006-01-02", startDateStr)
	endDate, _ := time.Parse("2006-01-02", endDateStr)

	for d := startDate; d.Before(endDate.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		dateStr := d.Format("2006-01-02")
		assignments, err := s.db.GetAssignmentsByDate(ctx, dateStr)
		if err == nil {
			for _, a := range assignments {
				if a.OriginalAssignmentID != nil && *a.OriginalAssignmentID == leaveID {
					covers = append(covers, struct {
						Date   string `json:"date"`
						Member string `json:"member"`
					}{
						Date:   dateStr,
						Member: a.MemberName,
					})
				}
			}
		}
	}

	return covers
}

type ListLeaveOutput struct {
	Body struct {
		LeaveRecords []database.LeaveRecord `json:"leave_records"`
	}
}

func (s *Server) handleListLeave(ctx context.Context, input *struct{}) (*ListLeaveOutput, error) {
	// Check authentication
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	// Get user from context using middleware's context key
	_, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}

	// Note: Returns all leave records for all team members.
	// This is intentional as the system is designed for a single team where
	// all authenticated users need visibility into leave schedules.
	// If per-user or per-team filtering is needed in the future, the query
	// would need to filter by member_id or team membership.
	leaveRecords, err := s.db.GetLeaveRecords(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to get leave records", err)
	}

	resp := &ListLeaveOutput{}
	resp.Body.LeaveRecords = leaveRecords
	return resp, nil
}

type GenerateScheduleInput struct {
	Body struct {
		StartDate string `format:"date" json:"start_date"`
		EndDate   string `format:"date" json:"end_date"`
	}
}

type GenerateScheduleOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

func (s *Server) handleGenerateSchedule(ctx context.Context, input *GenerateScheduleInput) (*GenerateScheduleOutput, error) {
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
	if !userSession.IsAdmin.Valid || userSession.IsAdmin.Int64 == 0 {
		return nil, huma.Error403Forbidden("Admin privileges required")
	}

	startDate, err := time.Parse("2006-01-02", input.Body.StartDate)
	if err != nil {
		return nil, huma.Error400BadRequest("Invalid start date format")
	}

	endDate, err := time.Parse("2006-01-02", input.Body.EndDate)
	if err != nil {
		return nil, huma.Error400BadRequest("Invalid end date format")
	}

	err = s.engine.GenerateSchedule(ctx, startDate, endDate)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to generate schedule", err)
	}

	resp := &GenerateScheduleOutput{}
	resp.Body.Message = "Schedule generated successfully"
	return resp, nil
}

type SubscribeCalendarInput struct {
	Body struct {
		MemberID string `json:"member_id"`
	}
}

type SubscribeCalendarOutput struct {
	Body struct {
		Token       string `json:"token"`
		CalendarURL string `json:"calendar_url"`
		Message     string `json:"message"`
	}
}

func (s *Server) handleSubscribeCalendar(ctx context.Context, input *SubscribeCalendarInput) (*SubscribeCalendarOutput, error) {
	// Check authentication
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	// Get user from context using middleware's context key
	_, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}

	token, err := s.db.CreateCalendarSubscription(ctx, input.Body.MemberID)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to create calendar subscription", err)
	}

	// Get request from context to build URL
	// HUMA doesn't expose the request directly, so we'll use a placeholder
	baseURL := "http://localhost:8080"

	resp := &SubscribeCalendarOutput{}
	resp.Body.Token = token
	resp.Body.CalendarURL = baseURL + "/api/v1/calendar/" + token + "/ics"
	resp.Body.Message = "Calendar subscription created"
	return resp, nil
}

func (s *Server) handleCalendarICS(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")

	member, err := s.db.GetMemberByToken(r.Context(), token)
	if err != nil {
		http.Error(w, "Invalid token", http.StatusNotFound)
		return
	}

	// Get upcoming assignments
	assignments, err := s.db.GetUpcomingAssignments(r.Context(), member.ID, calendarDaysLookahead)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Generate ICS content
	ics := "BEGIN:VCALENDAR\r\n"
	ics += "VERSION:2.0\r\n"
	ics += "PRODID:-//Support Rota//EN\r\n"
	ics += "CALSCALE:GREGORIAN\r\n"

	var icsSb strings.Builder
	for _, a := range assignments {
		eventDate, _ := time.Parse("2006-01-02", a.Date)
		icsSb.WriteString("BEGIN:VEVENT\r\n")
		icsSb.WriteString("UID:" + a.ID + "@supportrota\r\n")
		icsSb.WriteString("DTSTAMP:" + time.Now().Format("20060102T150405Z") + "\r\n")
		icsSb.WriteString("DTSTART;VALUE=DATE:" + eventDate.Format("20060102") + "\r\n")
		icsSb.WriteString("SUMMARY:Support Duty" + "\r\n")
		if a.IsCover {
			icsSb.WriteString("DESCRIPTION:Cover assignment" + "\r\n")
		}
		icsSb.WriteString("END:VEVENT\r\n")
	}
	ics += icsSb.String()

	ics += "END:VCALENDAR\r\n"

	w.Header().Set("Content-Type", "text/calendar")
	w.Header().Set("Content-Disposition", "attachment; filename=\"support-rota-"+member.Name+".ics\"")
	if _, err := w.Write([]byte(ics)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (s *Server) setupSessionCleanup(ctx context.Context) {
	// Start session cleanup background task (only if auth is enabled)
	if s.sessionManager != nil {
		log.Println("Starting session cleanup task...")
		cleanupCtx, cleanupCancel := context.WithCancel(ctx)
		s.cleanupCtx = cleanupCtx
		s.cleanupCancel = cleanupCancel
		//nolint:contextcheck // Cleanup context is properly managed and canceled in StopCleanup
		s.sessionManager.StartCleanup(s.cleanupCtx)
	} else {
		log.Println("Authentication disabled - skipping session cleanup")
	}
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
}

func (s *Server) Start(ctx context.Context, port string) error {
	s.setupSessionCleanup(ctx)

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
	if !userSession.IsAdmin.Valid || userSession.IsAdmin.Int64 == 0 {
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
		ID string `path:"id" doc:"Token ID to revoke"`
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
	if !userSession.IsAdmin.Valid || userSession.IsAdmin.Int64 == 0 {
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
