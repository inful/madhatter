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

	// Surface any flash banner from a previous form-submit redirect.
	// The defaults below make the template's `{{if .X}}` guards
	// safe (no "invalid type for comparison" errors when the flash
	// is absent); PopFlashIntoData overwrites only what the flash
	// carries.
	h.initDashboardFlashKeys(data)
	h.applyDashboardFlash(r, data)
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

// initDashboardFlashKeys seeds the data map with zero values for
// every Bug #15 form-success flash key. The template's `{{if .X}}`
// guards are safe against absent keys but not against `gt`/`eq`
// comparisons against nil — these defaults keep the dashboard
// rendering cleanly when no flash is present.
func (h *Handler) initDashboardFlashKeys(data map[string]any) {
	data["MemberAddedOK"] = false
	data["LeaveSubmittedStart"] = ""
	data["LeaveSubmittedEnd"] = ""
	data["ScheduleGeneratedCount"] = int64(0)
	data["SwapRequestedMember"] = ""
	data["WFHRequestedStart"] = ""
}

// applyDashboardFlash pops the request's flash and copies the
// relevant fields into the data map. Admin-only kinds (purge /
// mark / unmark) are silently skipped — the admin WFH page
// consumes those via its own apply*Flash helpers, so the
// dashboard doesn't double-render them.
func (h *Handler) applyDashboardFlash(r *http.Request, data map[string]any) {
	f := PopFlash(r)
	if f == nil {
		return
	}
	// Dispatch table — each case calls a small typed writer so the
	// switch itself stays under the cyclomatic-complexity cap.
	switch f.Kind {
	case FlashKindReportWFHToday:
		applyWFHReportTodayFlash(data, f)
	case FlashKindSignalOnSiteToday, FlashKindSignalOnSiteFuture:
		// Same banner surface — the today and forward-dated
		// variants share SignalOnSiteOutcome / SignalOnSiteReason.
		// The future-dated kind additionally carries a Date the
		// template can render for confirmation.
		applySignalOnSiteFlash(data, f)
		applySignalOnSiteDateIfFuture(data, f)
	case FlashKindReportMemberAdded:
		applyMemberAddedFlash(data, f)
	case FlashKindReportLeaveSubmitted:
		applyLeaveSubmittedFlash(data, f)
	case FlashKindReportScheduleGenerated:
		applyScheduleGeneratedFlash(data, f)
	case FlashKindReportSwapRequested:
		applySwapRequestedFlash(data, f)
	case FlashKindReportWFHRequested:
		applyWFHRequestedFlash(data, f)
	case FlashKindPurgeWFHPeriods, FlashKindMarkAdminWFH:
		// Admin-only surfaces; the /admin/wfh page consumes these
		// directly via applyPurgeFlash / applyMarkFlash, so the
		// dashboard doesn't need to surface them.
	}
}

// Per-kind writers. Each writes the kind's specific keys into the
// data map; small + focused so they're easy to test in isolation
// later if we want.

func applyWFHReportTodayFlash(data map[string]any, f *Flash) {
	data["WFHReportTodayOutcome"] = f.Status
	if f.Reason != "" {
		data["WFHReportTodayReason"] = f.Reason
	}
}

func applySignalOnSiteFlash(data map[string]any, f *Flash) {
	data["SignalOnSiteOutcome"] = f.Status
	if f.Reason != "" {
		data["SignalOnSiteReason"] = f.Reason
	}
}

// applySignalOnSiteDateIfFuture attaches the future-dated flash
// payload's Date to the data map, but only when the flash is the
// future-dated variant. Split out so applyDashboardFlash stays
// under the cyclomatic-complexity cap.
func applySignalOnSiteDateIfFuture(data map[string]any, f *Flash) {
	if f.Kind != FlashKindSignalOnSiteFuture {
		return
	}
	if f.Date == "" {
		return
	}
	data["SignalOnSiteDate"] = f.Date
}

func applyMemberAddedFlash(data map[string]any, _ *Flash) { data["MemberAddedOK"] = true }

func applyLeaveSubmittedFlash(data map[string]any, f *Flash) {
	data["LeaveSubmittedStart"] = f.StartDate
	data["LeaveSubmittedEnd"] = f.EndDate
}

func applyScheduleGeneratedFlash(data map[string]any, f *Flash) {
	data["ScheduleGeneratedCount"] = f.Count
}

func applySwapRequestedFlash(data map[string]any, f *Flash) {
	data["SwapRequestedMember"] = f.Member
}

func applyWFHRequestedFlash(data map[string]any, f *Flash) {
	data["WFHRequestedStart"] = f.StartDate
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
