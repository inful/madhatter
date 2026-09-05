package web

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/inful/madhatter/internal/auth"
)

// handleDashboard renders the main authenticated dashboard. The
// per-section data loading (HAT banner, presence snapshot, schedule
// matrix, etc.) lives in dashboard_data.go and dashboard_matrix.go;
// this function orchestrates the order and surfaces the final
// template context.
//
// The orchestrator's order matters:
//  1. EnsureSchedule — the matrix below assumes a 14-day window.
//  2. loadCurrentHAT — the banner must agree with the matrix's
//     assigned row.
//  3. loadDashboardData — populates assignments + matrix.
//  4. loadPendingSwapCount, canReportWFHToday — small banners.
func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := map[string]any{
		"Today":    time.Now().Format("Monday, Jan 2, 2006"),
		"Template": "dashboard",
	}

	data["AuthEnabled"] = h.authManager != nil && h.authMiddleware != nil

	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = auth.IsAdminSession(user)
		h.loadCurrentUserPresenceStatus(ctx, data, user.Email)
	}

	members, err := h.db.GetActiveTeamMembers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if len(members) == 0 {
		data["NoTeamMessage"] = "No team members found. Please add team members to get started."
	}

	if _, err = h.maintenance.EnsureSchedule(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.loadCurrentHAT(ctx, data)

	h.loadDashboardData(ctx, data)

	h.loadPendingSwapCount(ctx, data)

	// Surface the report-today flash banner (if any) and gate the
	// "WFH today" button on business-day + WFH-feature-enabled +
	// current-user-status. The button only renders when the user is
	// currently On-site so the affordance matches its outcome.
	surfaceDashboardFlash(r, data, wfhReportTodayFlashKey, "WFHReportTodayOutcome", "WFHReportTodayReason")
	surfaceDashboardFlash(r, data, signalOnSiteFlashKey, "SignalOnSiteOutcome", "SignalOnSiteReason")
	data["CanReportWFHToday"] = h.canReportWFHToday(ctx)

	// Optional URL the HAT day badge in the Today card links to.
	// Mirrors the MEETINGS_TEAMS_URL pattern in
	// calendar_meetings_day.html: when set, the badge renders as an
	// <a target="_blank" rel="noopener"> that opens the URL in a new
	// window (useful for an on-call runbook or PagerDuty rotation).
	// When unset, the badge stays a plain <span>. Admin-only knob;
	// html/template auto-escapes the href so a `javascript:` value
	// is neutralized at render time.
	data["HatLinkURL"] = os.Getenv("HAT_LINK_URL")

	if err := h.tmpl.ExecuteTemplate(w, "dashboard.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// surfaceDashboardFlash reads the flash banner from the query
// string under key, and if present, writes the outcome and
// (optional) reason into the data map under outcomeKey and
// reasonKey. Two flash banners share this helper (report-today
// and signal-on-site) so the dashboard orchestrator stays below
// the cyclomatic-complexity cap.
func surfaceDashboardFlash(r *http.Request, data map[string]any, key, outcomeKey, reasonKey string) {
	outcome, reason := readFlashOutcome(r, key)
	if outcome == "" {
		return
	}
	data[outcomeKey] = outcome
	if reason != "" {
		data[reasonKey] = reason
	}
}

// canReportWFHToday gates the dashboard "WFH today" button. The
// button only renders when the WFH feature is enabled and today is
// a business day.
func (h *Handler) canReportWFHToday(ctx context.Context) bool {
	return h.canReportWFHTodayAt(ctx, time.Now().UTC())
}

// canReportWFHTodayAt is the time-injectable variant of
// canReportWFHToday. Tests use it to pin the gate behavior without
// depending on the wall clock.
func (h *Handler) canReportWFHTodayAt(_ context.Context, now time.Time) bool {
	if h.wfhService == nil || !h.wfhService.IsEnabled() {
		return false
	}
	date := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return h.isBusinessDay(date)
}

// handleLoginUnavailable renders the plain-text "auth disabled"
// page served when auth middleware is not wired (--development mode
// with fake login disabled, or production with no OAuth providers).
func (h *Handler) handleLoginUnavailable(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("Authentication is disabled on this server.\n\nTo enable login, configure OAuth provider environment variables (see AUTH_SETUP.md), or run the server with --development for fake login during local development.\n"))
}
