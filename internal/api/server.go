package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/rota"
	"github.com/inful/madhatter/internal/web"
)

const (
	calendarDaysLookahead = 30
	serverReadTimeout     = 15 * time.Second
	serverWriteTimeout    = 15 * time.Second
	serverIdleTimeout     = 60 * time.Second
)

type Server struct {
	router *chi.Mux
	db     *database.DB
	engine *rota.Engine
}

func NewServer(db *database.DB) *Server {
	router := chi.NewRouter()
	s := &Server{
		router: router,
		db:     db,
		engine: rota.NewEngine(db),
	}

	s.registerRoutes()
	s.registerWebRoutes()
	return s
}

func (s *Server) registerRoutes() {
	// Team Operations
	s.router.Post("/api/v1/team", s.handleAddTeam)
	s.router.Get("/api/v1/team", s.handleListTeam)

	// Leave Operations
	s.router.Post("/api/v1/leave", s.handleReportLeave)

	// Schedule Operations
	s.router.Post("/api/v1/schedule/generate", s.handleGenerateSchedule)

	// Calendar Operations
	s.router.Post("/api/v1/calendar/subscribe", s.handleSubscribeCalendar)
	s.router.Get("/api/v1/calendar/{token}/ics", s.handleCalendarICS)
}

func (s *Server) registerWebRoutes() {
	// Create web handler
	webHandler := web.NewHandler(s.db)

	// Mount web routes
	s.router.Mount("/", webHandler.Router())
}

type AddTeamInput struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type AddTeamOutput struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

func (s *Server) handleAddTeam(w http.ResponseWriter, r *http.Request) {
	var input AddTeamInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	id, err := s.db.AddTeamMember(input.Name, input.Email)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(AddTeamOutput{
		ID:      id,
		Message: "Team member added successfully",
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

type ListTeamOutput struct {
	Members []database.TeamMember `json:"members"`
}

func (s *Server) handleListTeam(w http.ResponseWriter, r *http.Request) {
	members, err := s.db.GetActiveTeamMembers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(ListTeamOutput{Members: members}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

type ReportLeaveInput struct {
	MemberID  string `json:"member_id"`
	Type      string `json:"type"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

type ReportLeaveOutput struct {
	LeaveID string `json:"leave_id"`
	Covers  []struct {
		Date   string `json:"date"`
		Member string `json:"member"`
	} `json:"covers"`
	Message string `json:"message"`
}

func (s *Server) handleReportLeave(w http.ResponseWriter, r *http.Request) {
	var input ReportLeaveInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Create leave record
	leaveID, err := s.db.CreateLeaveRecord(input.MemberID, input.Type, input.StartDate, input.EndDate)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Assign covers
	err = s.engine.AssignCoversForLeave(leaveID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Get cover assignments for response
	covers := []struct {
		Date   string `json:"date"`
		Member string `json:"member"`
	}{}

	// Query covers created for this leave
	startDate, _ := time.Parse("2006-01-02", input.StartDate)
	endDate, _ := time.Parse("2006-01-02", input.EndDate)

	for d := startDate; d.Before(endDate.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		dateStr := d.Format("2006-01-02")
		assignments, err := s.db.GetAssignmentsByDate(dateStr)
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

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(ReportLeaveOutput{
		LeaveID: leaveID,
		Covers:  covers,
		Message: "Leave reported and covers assigned",
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

type GenerateScheduleInput struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

type GenerateScheduleOutput struct {
	Message string `json:"message"`
}

func (s *Server) handleGenerateSchedule(w http.ResponseWriter, r *http.Request) {
	var input GenerateScheduleInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	startDate, err := time.Parse("2006-01-02", input.StartDate)
	if err != nil {
		http.Error(w, "Invalid start date format", http.StatusBadRequest)
		return
	}

	endDate, err := time.Parse("2006-01-02", input.EndDate)
	if err != nil {
		http.Error(w, "Invalid end date format", http.StatusBadRequest)
		return
	}

	err = s.engine.GenerateSchedule(startDate, endDate)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(GenerateScheduleOutput{
		Message: "Schedule generated successfully",
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

type SubscribeCalendarInput struct {
	MemberID string `json:"member_id"`
}

type SubscribeCalendarOutput struct {
	Token       string `json:"token"`
	CalendarURL string `json:"calendar_url"`
	Message     string `json:"message"`
}

func (s *Server) handleSubscribeCalendar(w http.ResponseWriter, r *http.Request) {
	var input SubscribeCalendarInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	token, err := s.db.CreateCalendarSubscription(input.MemberID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	baseURL := "http://" + r.Host
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(SubscribeCalendarOutput{
		Token:       token,
		CalendarURL: baseURL + "/api/v1/calendar/" + token + "/ics",
		Message:     "Calendar subscription created",
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (s *Server) handleCalendarICS(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")

	member, err := s.db.GetMemberByToken(token)
	if err != nil {
		http.Error(w, "Invalid token", http.StatusNotFound)
		return
	}

	// Get upcoming assignments
	assignments, err := s.db.GetUpcomingAssignments(member.ID, calendarDaysLookahead)
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

func (s *Server) Start(port string) error {
	// Use http.Server with timeouts for production
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      s.router,
		ReadTimeout:  serverReadTimeout,
		WriteTimeout: serverWriteTimeout,
		IdleTimeout:  serverIdleTimeout,
	}
	return srv.ListenAndServe()
}
