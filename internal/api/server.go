package api

import (
	"context"
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
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/rota"
	"github.com/inful/madhatter/internal/web"
)

const (
	calendarDaysLookahead  = 30
	serverReadTimeout      = 15 * time.Second
	serverWriteTimeout     = 15 * time.Second
	serverIdleTimeout      = 60 * time.Second
	sessionCleanupInterval = 1 * time.Hour // Clean up expired sessions every hour
	shutdownTimeout        = 30 * time.Second
	sessionExpiryDuration  = 24 * time.Hour
)

type Server struct {
	router         *chi.Mux
	api            huma.API
	db             *database.DB
	engine         *rota.Engine
	authManager    *auth.AuthManager
	authMiddleware *auth.Middleware
	sessionManager *auth.SessionManager
	//nolint:containedctx // Context is used for graceful shutdown
	cleanupCtx    context.Context
	cleanupCancel context.CancelFunc
}

func NewServer(db *database.DB, development bool) (*Server, error) {
	router := chi.NewRouter()

	// Create HUMA API with Chi adapter
	config := huma.DefaultConfig("Support Rota API", "1.0.0")
	api := humachi.New(router, config)

	// Setup authentication components
	authManager, authMiddleware, sessionManager, err := setupAuth(db, development)
	if err != nil {
		return nil, err
	}

	s := &Server{
		router:         router,
		api:            api,
		db:             db,
		engine:         rota.NewEngine(db),
		authManager:    authManager,
		authMiddleware: authMiddleware,
		sessionManager: sessionManager,
	}

	s.registerOperations(development)
	s.registerWebRoutes(development)
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
	sessionManager := auth.NewSessionManager(db.GetQueries(), sessionExpiryDuration)

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
	}, s.handleAddTeam)

	huma.Register(s.api, huma.Operation{
		OperationID: "list-team-members",
		Method:      http.MethodGet,
		Path:        "/api/v1/team",
		Summary:     "List all active team members",
		Tags:        []string{"Team"},
	}, s.handleListTeam)

	// Leave Operations
	huma.Register(s.api, huma.Operation{
		OperationID: "report-leave",
		Method:      http.MethodPost,
		Path:        "/api/v1/leave",
		Summary:     "Report leave for a team member",
		Tags:        []string{"Leave"},
	}, s.handleReportLeave)

	// Schedule Operations
	huma.Register(s.api, huma.Operation{
		OperationID: "generate-schedule",
		Method:      http.MethodPost,
		Path:        "/api/v1/schedule/generate",
		Summary:     "Generate schedule for date range",
		Tags:        []string{"Schedule"},
	}, s.handleGenerateSchedule)

	// Calendar Operations
	huma.Register(s.api, huma.Operation{
		OperationID: "subscribe-calendar",
		Method:      http.MethodPost,
		Path:        "/api/v1/calendar/subscribe",
		Summary:     "Create calendar subscription",
		Tags:        []string{"Calendar"},
	}, s.handleSubscribeCalendar)

	// ICS Feed (no auth required)
	s.router.Get("/api/v1/calendar/{token}/ics", s.handleCalendarICS)

	// Development mode fake auth routes
	if development && s.authManager != nil {
		fakeHandler := auth.NewFakeCallbackHandler(s.authManager)
		s.router.HandleFunc("/auth/fake/login", fakeHandler.HandleLogin)
		s.router.HandleFunc("/auth/fake/callback", fakeHandler.HandleCallback)
	}
}

func (s *Server) registerWebRoutes(development bool) {
	// Create web handler with auth components
	webHandler := web.NewHandler(s.db, s.authManager, s.authMiddleware, development)

	// Development mode: The web handler's registerDevelopmentRoutes will handle the fake login view
	// No need to register it separately here

	// Mount web routes
	s.router.Mount("/", webHandler.Router())
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
	covers := []struct {
		Date   string `json:"date"`
		Member string `json:"member"`
	}{}

	// Query covers created for this leave
	startDate, _ := time.Parse("2006-01-02", input.Body.StartDate)
	endDate, _ := time.Parse("2006-01-02", input.Body.EndDate)

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

	resp := &ReportLeaveOutput{}
	resp.Body.LeaveID = leaveID
	resp.Body.Covers = covers
	resp.Body.Message = "Leave reported and covers assigned"
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
