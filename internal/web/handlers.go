package web

import (
	"context"
	"embed"
	"html/template"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/calendar"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/rota"
)

//go:embed templates/*.html
var templateFS embed.FS

const (
	weekDaysCount                = 5
	weekDaysOffset               = 4
	schedulePrealloc             = 5
	defaultCalendarLookaheadDays = 90
	weekDaysInWeek               = 7
	defaultHolidayLookaheadDays  = 30
)

type Handler struct {
	db             *database.DB
	maintenance    *rota.ScheduleMaintenance
	tmpl           *template.Template
	router         *chi.Mux
	authManager    *auth.AuthManager
	authMiddleware *auth.Middleware
	holidayChecker func(time.Time) bool
}

type presenceDay struct {
	DateISO     string
	DateDisplay string
	Assigned    *database.TeamMember
	Present     []database.TeamMember
	Away        []presenceLeave
}

type presenceLeave struct {
	Member database.TeamMember
	Type   string
}

func NewHandler(db *database.DB, authManager *auth.AuthManager, authMiddleware *auth.Middleware, development bool, holidayChecker func(time.Time) bool) (*Handler, error) {
	// Parse templates - use absolute path based on working directory
	// Try multiple possible locations for templates
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}

	router := chi.NewRouter()

	h := &Handler{
		db:             db,
		maintenance:    rota.NewScheduleMaintenance(db),
		tmpl:           tmpl,
		router:         router,
		authManager:    authManager,
		authMiddleware: authMiddleware,
		holidayChecker: holidayChecker,
	}

	h.registerRoutes()

	// Register development-specific routes if in development mode
	if development {
		h.registerDevelopmentRoutes()
	}

	return h, nil
}

// safeAuthMiddleware wraps middleware to handle nil auth components gracefully.
func (h *Handler) safeAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.authMiddleware == nil {
			// No authentication configured, proceed without auth
			next.ServeHTTP(w, r)
			return
		}
		// Use the actual middleware
		h.authMiddleware.OptionalAuth(next).ServeHTTP(w, r)
	})
}

// safeRequireAuth wraps middleware to handle nil auth components gracefully.
func (h *Handler) safeRequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.authMiddleware == nil {
			// No authentication configured - show error
			http.Error(w, "Authentication required but not configured. Please set up OAuth provider environment variables.", http.StatusUnauthorized)
			return
		}
		// Use the actual middleware
		h.authMiddleware.RequireAuth(next).ServeHTTP(w, r)
	})
}

// safeRequireAdmin wraps middleware to handle nil auth components gracefully.
func (h *Handler) safeRequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.authMiddleware == nil {
			// No authentication configured - show error
			http.Error(w, "Admin access required but authentication not configured. Please set up OAuth provider environment variables.", http.StatusUnauthorized)
			return
		}
		// Use the actual middleware
		h.authMiddleware.RequireAdmin(next).ServeHTTP(w, r)
	})
}

func parseTemplates() (*template.Template, error) {
	// Parse templates from embedded filesystem
	return template.ParseFS(templateFS, "templates/*.html")
}

func (h *Handler) registerRoutes() {
	// Auth routes (no authentication required) - only if auth is configured
	if h.authManager != nil {
		h.router.HandleFunc("/login", h.authManager.HandleLoginView)
		h.router.HandleFunc("/auth/login/{provider}", h.authManager.HandleLogin)
		h.router.HandleFunc("/auth/callback", h.authManager.HandleCallback)
		h.router.HandleFunc("/auth/logout", h.authManager.HandleLogout)
	}

	// Public routes (no authentication required, but user info loaded if available)
	h.router.Group(func(r chi.Router) {
		r.Use(h.safeAuthMiddleware)
		r.HandleFunc("/", h.handleDashboard)
		r.HandleFunc("/schedule/current", h.handleScheduleCurrent)
	})
	h.router.HandleFunc("/calendar/{token}/ics", h.handleCalendarICS)

	// Protected routes (require authentication)
	h.router.Group(func(r chi.Router) {
		r.Use(h.safeRequireAuth)

		r.HandleFunc("/leave/report", h.handleLeaveReport)
		r.HandleFunc("/calendar", h.handleCalendar)
	})

	// Admin routes (require authentication and admin privileges)
	h.router.Group(func(r chi.Router) {
		r.Use(h.safeRequireAuth)
		r.Use(h.safeRequireAdmin)

		r.HandleFunc("/team", h.handleTeam)
		r.HandleFunc("/schedule/generate", h.handleScheduleGenerate)
	})
}

// registerDevelopmentRoutes adds development-specific routes.
func (h *Handler) registerDevelopmentRoutes() {
	if h.authManager == nil {
		return
	}

	// Check if this is a fake provider
	provider, err := h.authManager.GetProvider("fake")
	if err != nil || provider == nil {
		return
	}

	// Override the login view to show development mode message
	h.router.HandleFunc("/login", h.handleDevelopmentLogin)
}

func (h *Handler) handleDevelopmentLogin(w http.ResponseWriter, r *http.Request) {
	// Check if already logged in
	if h.isUserLoggedIn(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Show development mode login page
	h.renderDevelopmentLogin(w)
}

func (h *Handler) isUserLoggedIn(r *http.Request) bool {
	token, err := h.authManager.GetSessionManager().GetSessionCookie(r)
	if err != nil {
		return false
	}

	_, err = h.authManager.GetSessionManager().ValidateSession(r.Context(), token)
	return err == nil
}

func (h *Handler) renderDevelopmentLogin(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html")
	// Use shared HTML from auth package to eliminate duplication
	_, _ = w.Write([]byte(auth.GetDevelopmentLoginHTML()))
}

// Router returns the underlying chi router.
func (h *Handler) Router() *chi.Mux {
	return h.router
}

func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := map[string]any{
		"Today": time.Now().Format("Monday, Jan 2, 2006"),
	}

	// Add user info to data
	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = user.IsAdmin.Valid && user.IsAdmin.Int64 == 1
	}

	// Check team members and handle no-team case
	if !h.checkTeamMembers(ctx, w, data) {
		return
	}

	// Maintain schedule (ignore return value, just check error)
	_, err := h.maintenance.EnsureSchedule(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Load dashboard data
	h.loadDashboardData(ctx, data)

	// Render template
	if err := h.tmpl.ExecuteTemplate(w, "dashboard.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// checkTeamMembers validates team exists and handles no-team case.
func (h *Handler) checkTeamMembers(ctx context.Context, w http.ResponseWriter, data map[string]any) bool {
	members, err := h.db.GetActiveTeamMembers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}

	if len(members) == 0 {
		data["NoTeamMessage"] = "No team members found. Please add team members to get started."
		if execErr := h.tmpl.ExecuteTemplate(w, "dashboard.html", data); execErr != nil {
			http.Error(w, execErr.Error(), http.StatusInternalServerError)
		}
		return false
	}

	return true
}

// loadDashboardData populates the dashboard with today's and week's assignments.
func (h *Handler) loadDashboardData(ctx context.Context, data map[string]any) {
	// Get today's assignment
	today := time.Now().Format("2006-01-02")
	assignments, err := h.db.GetAssignmentsByDate(ctx, today)
	if err == nil && len(assignments) > 0 {
		data["TodayAssignment"] = assignments[0]
	}

	// Get upcoming presence for next business days
	if presence, presenceErr := h.getUpcomingPresence(ctx); presenceErr == nil {
		data["UpcomingPresence"] = presence
	}

	// Get current and next week assignments
	weeksData, err := h.getFullWeeks(ctx)
	if err == nil {
		data["CurrentWeek"] = h.buildWeekData(weeksData, true)
		data["NextWeek"] = h.buildWeekData(weeksData, false)
	}

	// Get upcoming holidays
	if h.holidayChecker != nil {
		data["UpcomingHolidays"] = h.getUpcomingHolidays()
	}
}

// getUpcomingHolidays returns upcoming holidays for the configured lookahead days.
func (h *Handler) getUpcomingHolidays() []map[string]any {
	var holidays []map[string]any
	now := time.Now()
	endDate := now.AddDate(0, 0, defaultHolidayLookaheadDays)

	for d := now; d.Before(endDate); d = d.AddDate(0, 0, 1) {
		// Skip weekends - they are not holidays
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}

		// Check if it's an actual holiday (not just a skipped date)
		if h.holidayChecker(d) {
			holidays = append(holidays, map[string]any{
				"Date": d.Format("2006-01-02"),
				"Name": "Holiday",
			})
		}
	}

	return holidays
}

func (h *Handler) isBusinessDay(date time.Time) bool {
	if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
		return false
	}

	if h.holidayChecker != nil && h.holidayChecker(date) {
		return false
	}

	return true
}

func (h *Handler) getUpcomingPresence(ctx context.Context) ([]presenceDay, error) {
	return h.getUpcomingPresenceFrom(ctx, time.Now())
}

// getAssignedMember fetches the assigned member for a given date.
func (h *Handler) getAssignedMember(ctx context.Context, dateStr string, memberMap map[string]database.TeamMember) *database.TeamMember {
	assignments, err := h.db.GetAssignmentsByDate(ctx, dateStr)
	if err != nil {
		return nil
	}

	// Return whoever is assigned (cover or original - doesn't matter for support)
	for i := range assignments {
		if member, ok := memberMap[assignments[i].MemberID]; ok {
			return &member
		}
	}

	return nil
}

// buildPresenceList creates a sorted list of present members.
func buildPresenceList(memberMap map[string]database.TeamMember, onLeave map[string]struct{}) []database.TeamMember {
	present := make([]database.TeamMember, 0, len(memberMap)-len(onLeave))

	for id, member := range memberMap {
		if _, absent := onLeave[id]; absent {
			continue
		}
		present = append(present, member)
	}

	sort.Slice(present, func(i, j int) bool {
		return present[i].Name < present[j].Name
	})

	return present
}

func (h *Handler) getUpcomingPresenceFrom(ctx context.Context, start time.Time) ([]presenceDay, error) {
	members, err := h.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return nil, err
	}

	memberMap := make(map[string]database.TeamMember, len(members))
	for _, member := range members {
		memberMap[member.ID] = member
	}

	current := start
	presence := make([]presenceDay, 0, weekDaysCount)

	for len(presence) < weekDaysCount {
		if !h.isBusinessDay(current) {
			current = current.AddDate(0, 0, 1)
			continue
		}

		dateStr := current.Format("2006-01-02")
		assigned := h.getAssignedMember(ctx, dateStr, memberMap)

		leaveRecords, leaveErr := h.db.GetLeaveByDate(ctx, dateStr)
		if leaveErr != nil {
			return nil, leaveErr
		}

		away := make([]presenceLeave, 0, len(leaveRecords))
		onLeave := make(map[string]struct{})
		for i := range leaveRecords {
			leave := &leaveRecords[i]
			member, ok := memberMap[leave.MemberID]
			if !ok {
				continue
			}
			onLeave[leave.MemberID] = struct{}{}
			away = append(away, presenceLeave{
				Member: member,
				Type:   leave.Type,
			})
		}

		present := buildPresenceList(memberMap, onLeave)

		sort.Slice(away, func(i, j int) bool {
			return away[i].Member.Name < away[j].Member.Name
		})

		presence = append(presence, presenceDay{
			DateISO:     dateStr,
			DateDisplay: current.Format("Mon, Jan 2"),
			Assigned:    assigned,
			Present:     present,
			Away:        away,
		})

		current = current.AddDate(0, 0, 1)
	}

	return presence, nil
}

// buildWeekData builds week data for dashboard display.
func (h *Handler) buildWeekData(weeksData map[string][]database.RotaAssignment, isCurrentWeek bool) []map[string]any {
	now := time.Now()
	weekStart := now.AddDate(0, 0, -int(now.Weekday())+1)
	if !isCurrentWeek {
		weekStart = weekStart.AddDate(0, 0, weekDaysInWeek)
	}
	weekEnd := weekStart.AddDate(0, 0, weekDaysOffset)

	var week []map[string]any
	for d := weekStart; d.Before(weekEnd.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		isHoliday := h.holidayChecker != nil && h.holidayChecker(d)

		var assignment database.RotaAssignment
		if assignments, ok := weeksData[dateStr]; ok && len(assignments) > 0 {
			assignment = assignments[0]
		} else {
			assignment = database.RotaAssignment{
				Date:       dateStr,
				MemberName: "Unassigned",
			}
		}

		week = append(week, map[string]any{
			"Assignment": assignment,
			"IsHoliday":  isHoliday,
		})
	}
	return week
}

// getFullWeeks retrieves assignments for current and next week.
func (h *Handler) getFullWeeks(ctx context.Context) (map[string][]database.RotaAssignment, error) {
	now := time.Now()

	// Current week (Monday to Friday)
	currentWeekStart := now.AddDate(0, 0, -int(now.Weekday())+1)

	// Next week (Monday to Friday)
	nextWeekStart := currentWeekStart.AddDate(0, 0, weekDaysInWeek)
	nextWeekEnd := nextWeekStart.AddDate(0, 0, weekDaysOffset)

	// Get all assignments for both weeks
	startDate := currentWeekStart.Format("2006-01-02")
	endDate := nextWeekEnd.Format("2006-01-02")

	assignments, err := h.db.GetAssignmentsByDateRange(ctx, startDate, endDate)
	if err != nil {
		return nil, err
	}

	// Build map by date
	result := make(map[string][]database.RotaAssignment)
	for _, a := range assignments {
		result[a.Date] = append(result[a.Date], a)
	}

	return result, nil
}

func (h *Handler) handleTeam(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := make(map[string]any)

	// Add user info to data
	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = user.IsAdmin.Valid && user.IsAdmin.Int64 == 1
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		name := r.FormValue("name")
		email := r.FormValue("email")

		_, err := h.db.AddTeamMember(ctx, name, email)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Handle team change - update schedule
		if err := h.maintenance.HandleTeamChange(ctx); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, "/team", http.StatusSeeOther)
		return
	}

	members, err := h.db.GetActiveTeamMembers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data["Members"] = members

	if err := h.tmpl.ExecuteTemplate(w, "team.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) handleLeaveReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := make(map[string]any)

	// Add user info to data
	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = user.IsAdmin.Valid && user.IsAdmin.Int64 == 1
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		memberID := r.FormValue("member_id")
		leaveType := r.FormValue("type")
		startDate := r.FormValue("start_date")
		endDate := r.FormValue("end_date")

		leaveID, err := h.db.CreateLeaveRecord(ctx, memberID, leaveType, startDate, endDate)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Handle leave change using maintenance service
		err = h.maintenance.HandleLeaveChange(ctx, leaveID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, "/schedule/current", http.StatusSeeOther)
		return
	}

	members, err := h.db.GetActiveTeamMembers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data["Members"] = members

	if err := h.tmpl.ExecuteTemplate(w, "leave_report.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) handleScheduleCurrent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := make(map[string]any)

	// Add user info to data
	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = user.IsAdmin.Valid && user.IsAdmin.Int64 == 1
	}

	// Get schedule data
	calendar, startDate, endDate, err := h.getScheduleData(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data["Calendar"] = calendar
	data["StartDate"] = startDate
	data["EndDate"] = endDate

	if err := h.tmpl.ExecuteTemplate(w, "schedule_current.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// getScheduleData retrieves and builds schedule data for current and next week.
func (h *Handler) getScheduleData(ctx context.Context) ([]map[string]any, string, string, error) {
	now := time.Now()

	// Calculate week boundaries
	currentWeekStart := now.AddDate(0, 0, -int(now.Weekday())+1)
	currentWeekEnd := currentWeekStart.AddDate(0, 0, weekDaysOffset)
	nextWeekStart := currentWeekStart.AddDate(0, 0, weekDaysInWeek)
	nextWeekEnd := nextWeekStart.AddDate(0, 0, weekDaysOffset)

	// Get assignments
	startDate := currentWeekStart.Format("2006-01-02")
	endDate := nextWeekEnd.Format("2006-01-02")
	assignments, err := h.db.GetAssignmentsByDateRange(ctx, startDate, endDate)
	if err != nil {
		return nil, "", "", err
	}

	// Build lookup map
	assignmentMap := make(map[string][]database.RotaAssignment)
	for _, a := range assignments {
		assignmentMap[a.Date] = append(assignmentMap[a.Date], a)
	}

	// Build calendar
	calendar := make([]map[string]any, 0)
	calendar = h.appendWeekToCalendar(calendar, currentWeekStart, currentWeekEnd, assignmentMap, now, "Current Week")
	calendar = h.appendWeekToCalendar(calendar, nextWeekStart, nextWeekEnd, assignmentMap, now, "Next Week")

	return calendar, currentWeekStart.Format("January 2, 2006"), nextWeekEnd.Format("January 2, 2006"), nil
}

// appendWeekToCalendar adds a week's data to the calendar.
func (h *Handler) appendWeekToCalendar(calendar []map[string]any, weekStart, weekEnd time.Time, assignmentMap map[string][]database.RotaAssignment, now time.Time, weekLabel string) []map[string]any {
	for d := weekStart; d.Before(weekEnd.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		dayAssignments := assignmentMap[dateStr]
		isToday := d.Format("2006-01-02") == now.Format("2006-01-02")
		isHoliday := h.holidayChecker != nil && h.holidayChecker(d)

		day := map[string]any{
			"Date":        d.Format("Jan 2 (Mon)"),
			"DateISO":     dateStr,
			"Assignments": dayAssignments,
			"IsToday":     isToday,
			"IsWeekend":   d.Weekday() == 0 || d.Weekday() == 6,
			"IsHoliday":   isHoliday,
			"WeekLabel":   weekLabel,
		}
		calendar = append(calendar, day)
	}
	return calendar
}

func (h *Handler) handleScheduleGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		h.handleScheduleGeneratePost(w, r)
		return
	}

	// GET request - show form
	h.handleScheduleGenerateGet(w, r)
}

func (h *Handler) handleScheduleGeneratePost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Validate team members
	if !h.validateTeamMembers(ctx, w) {
		return
	}

	// Parse and validate dates
	start, end, ok := h.parseDateRange(w, r)
	if !ok {
		return
	}

	// Generate schedule based on mode
	regenerate := r.FormValue("regenerate") == "on"
	var err error
	if regenerate {
		_, err = h.maintenance.RegenerateSchedule(ctx, start, end)
	} else {
		_, err = h.maintenance.GenerateMissingDays(ctx, start, end)
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/schedule/current", http.StatusSeeOther)
}

func (h *Handler) handleScheduleGenerateGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !h.validateTeamMembers(ctx, w) {
		return
	}

	data := make(map[string]any)

	// Add user info to data
	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = user.IsAdmin.Valid && user.IsAdmin.Int64 == 1
	}

	now := time.Now()
	data["DefaultStart"] = now.Format("2006-01-02")
	data["DefaultEnd"] = now.AddDate(0, 1, 0).Format("2006-01-02")

	if err := h.tmpl.ExecuteTemplate(w, "schedule_generate.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) validateTeamMembers(ctx context.Context, w http.ResponseWriter) bool {
	members, err := h.db.GetActiveTeamMembers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}
	if len(members) == 0 {
		http.Error(w, "No team members found. Please add team members first.", http.StatusBadRequest)
		return false
	}
	return true
}

func (h *Handler) parseDateRange(w http.ResponseWriter, r *http.Request) (time.Time, time.Time, bool) {
	startDate := r.FormValue("start_date")
	endDate := r.FormValue("end_date")

	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		http.Error(w, "Invalid start date format", http.StatusBadRequest)
		return time.Time{}, time.Time{}, false
	}

	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		http.Error(w, "Invalid end date format", http.StatusBadRequest)
		return time.Time{}, time.Time{}, false
	}

	return start, end, true
}

func (h *Handler) handleCalendar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := make(map[string]any)

	// Add user info to data
	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = user.IsAdmin.Valid && user.IsAdmin.Int64 == 1
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		memberID := r.FormValue("member_id")

		token, err := h.db.CreateCalendarSubscription(ctx, memberID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		baseURL := "http://" + r.Host
		data["Token"] = token
		data["CalendarURL"] = baseURL + "/calendar/" + token + "/ics"
		data["ShowResult"] = true

		if err := h.tmpl.ExecuteTemplate(w, "calendar.html", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	members, err := h.db.GetActiveTeamMembers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data["Members"] = members

	if err := h.tmpl.ExecuteTemplate(w, "calendar.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) handleCalendarICS(w http.ResponseWriter, r *http.Request) {
	// Get token from URL
	token := chi.URLParam(r, "token")
	if token == "" {
		http.Error(w, "Token required", http.StatusBadRequest)
		return
	}

	// Generate ICS content using new calendar library
	icsContent, err := calendar.GenerateICalForToken(r.Context(), h.db, token, defaultCalendarLookaheadDays)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// Set headers for calendar download
	w.Header().Set("Content-Type", "text/calendar")
	w.Header().Set("Content-Disposition", "attachment; filename=\"support-rota.ics\"")
	w.Header().Set("Cache-Control", "no-cache")

	// Write ICS content
	_, _ = w.Write([]byte(icsContent))
}
