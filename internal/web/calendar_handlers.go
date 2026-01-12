package web

import (
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/calendar"
)

func (h *Handler) handleCalendar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := map[string]any{
		"Template": "calendar",
	}

	// Add user info to data.
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
		data["MeetingsCalendarURL"] = baseURL + "/calendar/" + token + "/meetings.ics"
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
	// Get token from URL.
	token := chi.URLParam(r, "token")
	if token == "" {
		http.Error(w, "Token required", http.StatusBadRequest)
		return
	}

	// Generate ICS content using new calendar library.
	icsContent, err := calendar.GenerateICalForToken(r.Context(), h.db, token, defaultCalendarLookaheadDays)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// Set headers for calendar download.
	w.Header().Set("Content-Type", "text/calendar")
	w.Header().Set("Content-Disposition", "attachment; filename=\"support-rota.ics\"")
	w.Header().Set("Cache-Control", "no-cache")

	// Write ICS content.
	_, _ = w.Write([]byte(icsContent))
}

func (h *Handler) handleMeetingsCalendarICS(w http.ResponseWriter, r *http.Request) {
	// Get token from URL.
	token := chi.URLParam(r, "token")
	if token == "" {
		http.Error(w, "Token required", http.StatusBadRequest)
		return
	}

	teamsURL := os.Getenv("MEETINGS_TEAMS_URL")
	tz := os.Getenv("MEETINGS_TIMEZONE")

	icsContent, err := calendar.GenerateMeetingsICalForToken(
		r.Context(),
		h.db,
		token,
		defaultCalendarLookaheadDays,
		calendar.MeetingsOptions{Timezone: tz, TeamsURL: teamsURL},
		h.isBusinessDay,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// Set headers for calendar download.
	w.Header().Set("Content-Type", "text/calendar")
	w.Header().Set("Content-Disposition", "attachment; filename=\"support-meetings.ics\"")
	w.Header().Set("Cache-Control", "no-cache")

	_, _ = w.Write([]byte(icsContent))
}
