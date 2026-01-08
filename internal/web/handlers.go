package web

import (
	"html/template"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/calendar"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/rota"
)

const (
	weekDaysCount                = 5
	weekDaysOffset               = 4
	schedulePrealloc             = 5
	defaultCalendarLookaheadDays = 90
	weekDaysInWeek               = 7
)

type Handler struct {
	db             *database.DB
	maintenance    *rota.ScheduleMaintenance
	tmpl           *template.Template
	router         *chi.Mux
	authManager    *auth.AuthManager
	authMiddleware *auth.Middleware
}

func NewHandler(db *database.DB, authManager *auth.AuthManager, authMiddleware *auth.Middleware) *Handler {
	// Parse templates - use absolute path based on working directory
	// Try multiple possible locations for templates
	tmpl := parseTemplates()

	router := chi.NewRouter()

	h := &Handler{
		db:             db,
		maintenance:    rota.NewScheduleMaintenance(db),
		tmpl:           tmpl,
		router:         router,
		authManager:    authManager,
		authMiddleware: authMiddleware,
	}

	h.registerRoutes()
	return h
}

func parseTemplates() *template.Template {
	// Try different possible template locations
	possiblePaths := []string{
		"internal/web/templates/*.html",
		"/workspaces/madhatter/internal/web/templates/*.html",
	}

	var tmpl *template.Template
	var lastErr error

	for _, path := range possiblePaths {
		var err error
		tmpl, err = template.ParseGlob(path)
		if err == nil {
			return tmpl
		}
		lastErr = err
	}

	// If all paths failed, panic with the last error
	panic(lastErr)
}

func (h *Handler) registerRoutes() {
	// Auth routes (no authentication required)
	h.router.HandleFunc("/login", h.authManager.HandleLoginView)
	h.router.HandleFunc("/auth/login/{provider}", h.authManager.HandleLogin)
	h.router.HandleFunc("/auth/callback", h.authManager.HandleCallback)
	h.router.HandleFunc("/auth/logout", h.authManager.HandleLogout)

	// Public routes (no authentication required, but user info loaded if available)
	h.router.Group(func(r chi.Router) {
		r.Use(h.authMiddleware.OptionalAuth)
		r.HandleFunc("/", h.handleDashboard)
		r.HandleFunc("/schedule/current", h.handleScheduleCurrent)
	})
	h.router.HandleFunc("/calendar/{token}/ics", h.handleCalendarICS)

	// Protected routes (require authentication)
	h.router.Group(func(r chi.Router) {
		r.Use(h.authMiddleware.RequireAuth)

		r.HandleFunc("/team", h.handleTeam)
		r.HandleFunc("/leave/report", h.handleLeaveReport)
		r.HandleFunc("/schedule/generate", h.handleScheduleGenerate)
		r.HandleFunc("/calendar", h.handleCalendar)
	})
}

// Router returns the underlying chi router.
func (h *Handler) Router() *chi.Mux {
	return h.router
}

func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{
		"Today": time.Now().Format("Monday, Jan 2, 2006"),
	}

	// Add user info to data
	if user, ok := auth.GetUserFromContext(r.Context()); ok {
		data["User"] = user
		data["IsAdmin"] = user.IsAdmin.Valid && user.IsAdmin.Int64 == 1
	}

	// Check team members and handle no-team case
	if !h.checkTeamMembers(w, data) {
		return
	}

	// Maintain schedule (ignore return value, just check error)
	_, err := h.maintenance.EnsureSchedule()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Load dashboard data
	h.loadDashboardData(data)

	// Render template
	if err := h.tmpl.ExecuteTemplate(w, "dashboard.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// checkTeamMembers validates team exists and handles no-team case.
func (h *Handler) checkTeamMembers(w http.ResponseWriter, data map[string]any) bool {
	members, err := h.db.GetActiveTeamMembers()
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
func (h *Handler) loadDashboardData(data map[string]any) {
	// Get today's assignment
	today := time.Now().Format("2006-01-02")
	assignments, err := h.db.GetAssignmentsByDate(today)
	if err == nil && len(assignments) > 0 {
		data["TodayAssignment"] = assignments[0]
	}

	// Get current and next week assignments
	weeksData, err := h.getFullWeeks()
	if err == nil {
		// Separate into current week and next week
		now := time.Now()
		currentWeekStart := now.AddDate(0, 0, -int(now.Weekday())+1)
		currentWeekEnd := currentWeekStart.AddDate(0, 0, weekDaysOffset)
		nextWeekStart := currentWeekStart.AddDate(0, 0, weekDaysInWeek)
		nextWeekEnd := nextWeekStart.AddDate(0, 0, weekDaysOffset)

		var currentWeek []database.RotaAssignment
		var nextWeek []database.RotaAssignment

		// Build current week (Monday-Friday) - always show all days
		for d := currentWeekStart; d.Before(currentWeekEnd.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
			dateStr := d.Format("2006-01-02")
			if assignments, ok := weeksData[dateStr]; ok && len(assignments) > 0 {
				currentWeek = append(currentWeek, assignments[0])
			} else {
				// Add empty assignment for days without assignments
				currentWeek = append(currentWeek, database.RotaAssignment{
					Date:       dateStr,
					MemberName: "Unassigned",
				})
			}
		}

		// Build next week (Monday-Friday) - always show all days
		for d := nextWeekStart; d.Before(nextWeekEnd.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
			dateStr := d.Format("2006-01-02")
			if assignments, ok := weeksData[dateStr]; ok && len(assignments) > 0 {
				nextWeek = append(nextWeek, assignments[0])
			} else {
				// Add empty assignment for days without assignments
				nextWeek = append(nextWeek, database.RotaAssignment{
					Date:       dateStr,
					MemberName: "Unassigned",
				})
			}
		}

		data["CurrentWeek"] = currentWeek
		data["NextWeek"] = nextWeek
	}
}

// getFullWeeks retrieves assignments for current and next week.
func (h *Handler) getFullWeeks() (map[string][]database.RotaAssignment, error) {
	now := time.Now()

	// Current week (Monday to Friday)
	currentWeekStart := now.AddDate(0, 0, -int(now.Weekday())+1)

	// Next week (Monday to Friday)
	nextWeekStart := currentWeekStart.AddDate(0, 0, weekDaysInWeek)
	nextWeekEnd := nextWeekStart.AddDate(0, 0, weekDaysOffset)

	// Get all assignments for both weeks
	startDate := currentWeekStart.Format("2006-01-02")
	endDate := nextWeekEnd.Format("2006-01-02")

	assignments, err := h.db.GetAssignmentsByDateRange(startDate, endDate)
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
	data := make(map[string]any)

	// Add user info to data
	if user, ok := auth.GetUserFromContext(r.Context()); ok {
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

		_, err := h.db.AddTeamMember(name, email)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Handle team change - update schedule
		if err := h.maintenance.HandleTeamChange(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, "/team", http.StatusSeeOther)
		return
	}

	members, err := h.db.GetActiveTeamMembers()
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
	data := make(map[string]any)

	// Add user info to data
	if user, ok := auth.GetUserFromContext(r.Context()); ok {
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

		leaveID, err := h.db.CreateLeaveRecord(memberID, leaveType, startDate, endDate)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Handle leave change using maintenance service
		err = h.maintenance.HandleLeaveChange(leaveID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, "/schedule/current", http.StatusSeeOther)
		return
	}

	members, err := h.db.GetActiveTeamMembers()
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
	data := make(map[string]any)

	// Add user info to data
	if user, ok := auth.GetUserFromContext(r.Context()); ok {
		data["User"] = user
		data["IsAdmin"] = user.IsAdmin.Valid && user.IsAdmin.Int64 == 1
	}

	now := time.Now()

	// Get current week (Monday-Friday)
	currentWeekStart := now.AddDate(0, 0, -int(now.Weekday())+1)
	currentWeekEnd := currentWeekStart.AddDate(0, 0, weekDaysOffset)

	// Get next week (Monday-Friday)
	nextWeekStart := currentWeekStart.AddDate(0, 0, weekDaysInWeek)
	nextWeekEnd := nextWeekStart.AddDate(0, 0, weekDaysOffset)

	// Get assignments for both weeks
	startDate := currentWeekStart.Format("2006-01-02")
	endDate := nextWeekEnd.Format("2006-01-02")

	assignments, err := h.db.GetAssignmentsByDateRange(startDate, endDate)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Build a map for quick lookup
	assignmentMap := make(map[string][]database.RotaAssignment)
	for _, a := range assignments {
		assignmentMap[a.Date] = append(assignmentMap[a.Date], a)
	}

	// Build calendar view - show all weekdays for both weeks
	calendar := make([]map[string]any, 0)

	// Current week
	for d := currentWeekStart; d.Before(currentWeekEnd.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		dayAssignments := assignmentMap[dateStr]
		isToday := d.Format("2006-01-02") == now.Format("2006-01-02")

		day := map[string]any{
			"Date":        d.Format("Jan 2 (Mon)"),
			"DateISO":     dateStr,
			"Assignments": dayAssignments,
			"IsToday":     isToday,
			"IsWeekend":   d.Weekday() == 0 || d.Weekday() == 6,
			"WeekLabel":   "Current Week",
		}
		calendar = append(calendar, day)
	}

	// Next week
	for d := nextWeekStart; d.Before(nextWeekEnd.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		dayAssignments := assignmentMap[dateStr]
		isToday := d.Format("2006-01-02") == now.Format("2006-01-02")

		day := map[string]any{
			"Date":        d.Format("Jan 2 (Mon)"),
			"DateISO":     dateStr,
			"Assignments": dayAssignments,
			"IsToday":     isToday,
			"IsWeekend":   d.Weekday() == 0 || d.Weekday() == 6,
			"WeekLabel":   "Next Week",
		}
		calendar = append(calendar, day)
	}

	data["Calendar"] = calendar
	data["StartDate"] = currentWeekStart.Format("January 2, 2006")
	data["EndDate"] = nextWeekEnd.Format("January 2, 2006")

	if err := h.tmpl.ExecuteTemplate(w, "schedule_current.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Validate team members
	if !h.validateTeamMembers(w) {
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
		_, err = h.maintenance.RegenerateSchedule(start, end)
	} else {
		_, err = h.maintenance.GenerateMissingDays(start, end)
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/schedule/current", http.StatusSeeOther)
}

func (h *Handler) handleScheduleGenerateGet(w http.ResponseWriter, r *http.Request) {
	if !h.validateTeamMembers(w) {
		return
	}

	data := make(map[string]any)

	// Add user info to data
	if user, ok := auth.GetUserFromContext(r.Context()); ok {
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

func (h *Handler) validateTeamMembers(w http.ResponseWriter) bool {
	members, err := h.db.GetActiveTeamMembers()
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
	data := make(map[string]any)

	// Add user info to data
	if user, ok := auth.GetUserFromContext(r.Context()); ok {
		data["User"] = user
		data["IsAdmin"] = user.IsAdmin.Valid && user.IsAdmin.Int64 == 1
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		memberID := r.FormValue("member_id")

		token, err := h.db.CreateCalendarSubscription(memberID)
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

	members, err := h.db.GetActiveTeamMembers()
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

	// Generate ICS content
	icsContent, err := calendar.GenerateICSForToken(h.db, token, defaultCalendarLookaheadDays)
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
