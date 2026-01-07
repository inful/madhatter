package web

import (
	"html/template"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/calendar"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/rota"
)

const (
	weekDaysCount                = 5
	weekDaysOffset               = 4
	schedulePrealloc             = 5
	defaultCalendarLookaheadDays = 90
)

type Handler struct {
	db     *database.DB
	engine *rota.Engine
	tmpl   *template.Template
	router *chi.Mux
}

func NewHandler(db *database.DB) *Handler {
	// Parse templates - use absolute path based on working directory
	// Try multiple possible locations for templates
	tmpl := parseTemplates()

	router := chi.NewRouter()

	h := &Handler{
		db:     db,
		engine: rota.NewEngine(db),
		tmpl:   tmpl,
		router: router,
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
	h.router.HandleFunc("/", h.handleDashboard)
	h.router.HandleFunc("/team", h.handleTeam)
	h.router.HandleFunc("/leave/report", h.handleLeaveReport)
	h.router.HandleFunc("/schedule/current", h.handleScheduleCurrent)
	h.router.HandleFunc("/schedule/generate", h.handleScheduleGenerate)
	h.router.HandleFunc("/calendar", h.handleCalendar)
	h.router.HandleFunc("/calendar/{token}/ics", h.handleCalendarICS)
}

// Router returns the underlying chi router.
func (h *Handler) Router() *chi.Mux {
	return h.router
}

func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{
		"Today": time.Now().Format("Monday, Jan 2, 2006"),
	}

	// Get today's assignment
	today := time.Now().Format("2006-01-02")
	assignments, err := h.db.GetAssignmentsByDate(today)
	if err == nil && len(assignments) > 0 {
		data["TodayAssignment"] = assignments[0]
	}

	// Get this week's assignments
	now := time.Now()
	weekStart := now.AddDate(0, 0, -int(now.Weekday())+1) // Monday
	weekEnd := weekStart.AddDate(0, 0, weekDaysOffset)    // Friday

	var weekAssignments []database.RotaAssignment
	for d := weekStart; d.Before(weekEnd.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		dayAssignments, err := h.db.GetAssignmentsByDate(dateStr)
		if err == nil && len(dayAssignments) > 0 {
			weekAssignments = append(weekAssignments, dayAssignments[0])
		}
	}
	data["WeekAssignments"] = weekAssignments

	if err := h.tmpl.ExecuteTemplate(w, "dashboard.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) handleTeam(w http.ResponseWriter, r *http.Request) {
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
		http.Redirect(w, r, "/team", http.StatusSeeOther)
		return
	}

	members, err := h.db.GetActiveTeamMembers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.tmpl.ExecuteTemplate(w, "team.html", map[string]any{
		"Members": members,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) handleLeaveReport(w http.ResponseWriter, r *http.Request) {
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

		// Assign covers
		err = h.engine.AssignCoversForLeave(leaveID)
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

	if err := h.tmpl.ExecuteTemplate(w, "leave_report.html", map[string]any{
		"Members": members,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) handleScheduleCurrent(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	weekStart := now.AddDate(0, 0, -int(now.Weekday())+1)

	// Pre-allocate schedule slice
	schedule := make([]map[string]any, 0, schedulePrealloc)
	for i := range weekDaysCount {
		d := weekStart.AddDate(0, 0, i)
		dateStr := d.Format("2006-01-02")
		assignments, _ := h.db.GetAssignmentsByDate(dateStr)

		day := map[string]any{
			"Date":        d.Format("Jan 2 (Mon)"),
			"Assignments": assignments,
		}
		schedule = append(schedule, day)
	}

	if err := h.tmpl.ExecuteTemplate(w, "schedule_current.html", map[string]any{
		"Schedule": schedule,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) handleScheduleGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		startDate := r.FormValue("start_date")
		endDate := r.FormValue("end_date")

		start, err := time.Parse("2006-01-02", startDate)
		if err != nil {
			http.Error(w, "Invalid start date format", http.StatusBadRequest)
			return
		}

		end, err := time.Parse("2006-01-02", endDate)
		if err != nil {
			http.Error(w, "Invalid end date format", http.StatusBadRequest)
			return
		}

		err = h.engine.GenerateSchedule(start, end)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, "/schedule/current", http.StatusSeeOther)
		return
	}

	// GET request - show form
	// Get current date and next month for default values
	now := time.Now()
	defaultStart := now.Format("2006-01-02")
	defaultEnd := now.AddDate(0, 1, 0).Format("2006-01-02")

	if err := h.tmpl.ExecuteTemplate(w, "schedule_generate.html", map[string]any{
		"DefaultStart": defaultStart,
		"DefaultEnd":   defaultEnd,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) handleCalendar(w http.ResponseWriter, r *http.Request) {
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
		if err := h.tmpl.ExecuteTemplate(w, "calendar.html", map[string]any{
			"Token":       token,
			"CalendarURL": baseURL + "/calendar/" + token + "/ics",
			"ShowResult":  true,
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	members, err := h.db.GetActiveTeamMembers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.tmpl.ExecuteTemplate(w, "calendar.html", map[string]any{
		"Members": members,
	}); err != nil {
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
