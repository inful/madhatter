package web

import (
	"context"
	"net/http"
	"time"

	"github.com/inful/madhatter/internal/auth"
)

func (h *Handler) handleScheduleGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		h.handleScheduleGeneratePost(w, r)
		return
	}

	// GET request - show form.
	h.handleScheduleGenerateGet(w, r)
}

func (h *Handler) handleScheduleGeneratePost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Validate team members.
	if !h.validateTeamMembers(ctx, w) {
		return
	}

	// Parse and validate dates.
	start, end, ok := h.parseDateRange(w, r)
	if !ok {
		return
	}

	// Generate schedule based on mode.
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

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) handleScheduleGenerateGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !h.validateTeamMembers(ctx, w) {
		return
	}

	data := map[string]any{
		"Template": "schedule_generate",
	}

	// Add user info to data.
	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = auth.IsAdminSession(user)
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
