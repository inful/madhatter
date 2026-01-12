package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
)

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
		icsSb.WriteString("SUMMARY:Support Duty\r\n")
		if a.IsCover {
			icsSb.WriteString("DESCRIPTION:Cover assignment\r\n")
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
