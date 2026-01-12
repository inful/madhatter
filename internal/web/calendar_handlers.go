package web

import (
	"net/http"
	"os"
	"strings"

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

		baseURL := baseURLFromRequest(r)
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

func baseURLFromRequest(r *http.Request) string {
	const maxSplitParts = 2

	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}

	scheme := schemeFromRequestTLS(r)

	// Reverse proxies typically forward the original scheme.
	if forwarded := strings.TrimSpace(r.Header.Get("Forwarded")); forwarded != "" {
		forwardedProto, forwardedHost := parseForwardedHeader(forwarded)
		if forwardedProto != "" {
			scheme = forwardedProto
		}
		if forwardedHost != "" {
			host = forwardedHost
		}
	} else if xfProto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); xfProto != "" {
		// Example: X-Forwarded-Proto: https
		scheme = strings.TrimSpace(strings.SplitN(xfProto, ",", maxSplitParts)[0])
	}

	if host == "" {
		// Best-effort fallback; should be rare.
		host = "localhost"
	}

	return scheme + "://" + host
}

func schemeFromRequestTLS(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func parseForwardedHeader(headerValue string) (proto string, host string) {
	const maxSplitParts = 2

	// Example: Forwarded: for=1.2.3.4;proto=https;host=example.com
	first := strings.SplitN(headerValue, ",", maxSplitParts)[0]
	for part := range strings.SplitSeq(first, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", maxSplitParts)
		if len(kv) != maxSplitParts {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(kv[0]))
		value := strings.Trim(strings.TrimSpace(kv[1]), "\"")
		switch key {
		case "proto":
			if value != "" {
				proto = value
			}
		case "host":
			if value != "" {
				host = value
			}
		}
	}

	return proto, host
}
