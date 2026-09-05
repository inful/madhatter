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

	// Count the days the user asked to generate, for the success
	// banner. The maintenance call doesn't return this count; we
	// compute it client-side from the start/end dates and the same
	// weekday math the picker uses. Inclusive of start, exclusive
	// of end would be the convention but we count both endpoints as
	// days the admin explicitly chose to fill.
	days := inclusiveWeekdayCount(start, end)
	SetFlash(w, r, "/", Flash{
		Kind:  FlashKindReportScheduleGenerated,
		Count: int64(days),
	})
}

// inclusiveWeekdayCount returns the number of weekdays (Mon–Fri)
// in the inclusive range [from, to]. The picker uses the same
// math so the success banner's count matches what the admin
// asked for.
func inclusiveWeekdayCount(from, to time.Time) int {
	if to.Before(from) {
		return 0
	}
	count := 0
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		switch d.Weekday() {
		case time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday:
			count++
		case time.Saturday, time.Sunday:
			// Weekends don't count toward WFH quota usage.
		}
	}
	return count
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
