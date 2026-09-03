package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/notify"
	"github.com/inful/madhatter/internal/wfh"
)

const (
	maxWFHFormBytes  = 1 << 20
	errNotTeamMember = "You are not registered as a team member."
)

// wfhBaseData builds the common data map for WFH templates.
func (h *Handler) wfhBaseData(r *http.Request, templateName string) map[string]any {
	ctx := r.Context()
	data := map[string]any{
		"Template": templateName,
	}
	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = auth.IsAdminSession(user)
	}
	return data
}

// handleWFHList shows the current user's WFH requests and quota status.
func (h *Handler) handleWFHList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := h.wfhBaseData(r, "wfh_list")

	user := mustGetUser(ctx)
	memberID := h.resolveMemberID(ctx, user.Email)
	if memberID == "" {
		data["Error"] = errNotTeamMember
		if err := h.tmpl.ExecuteTemplate(w, "wfh_list.html", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// Materialize any missing recurring occurrences for the current period
	// before showing the list, so contractual rows appear alongside ad-hoc
	// ones. Idempotent and bounded to one period — safe to run on each
	// page load.
	if h.wfhService != nil {
		start, end, err := h.wfhService.ComputePeriodBounds(time.Now().UTC())
		if err == nil {
			if _, mErr := h.wfhService.EnsureRecurringMaterializedForMember(ctx, memberID, start, end); mErr != nil {
				// Non-fatal: the user sees a slightly stale list.
				data["Error"] = "Failed to materialize recurring WFH days: " + mErr.Error()
			}
		}
	}

	requests, err := h.db.GetWFHRequestsByMember(ctx, memberID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data["Requests"] = enrichWFHRequests(requests, h.wfhService)

	// Quota status.
	if h.wfhService != nil {
		quota, qErr := h.wfhService.GetQuotaStatus(ctx, memberID)
		if qErr == nil {
			data["Quota"] = quota
		}
	}

	if err := h.tmpl.ExecuteTemplate(w, "wfh_list.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleWFHRequest handles GET and POST for the WFH request form.
func (h *Handler) handleWFHRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := h.wfhBaseData(r, "wfh_request")

	user := mustGetUser(ctx)
	memberID := h.resolveMemberID(ctx, user.Email)
	if memberID == "" {
		data["Error"] = errNotTeamMember
		if err := h.tmpl.ExecuteTemplate(w, "wfh_request.html", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if r.Method == http.MethodPost {
		h.handleWFHRequestPost(w, r, data, memberID)
		return
	}

	h.renderWFHRequestFormAt(w, r, data, memberID, "", r.URL.Query().Get("date"))
}

// handleWFHRequestPost processes a WFH request form submission. Validates the
// date format, horizon, quota, and delegates persistence to the DB layer.
func (h *Handler) handleWFHRequestPost(w http.ResponseWriter, r *http.Request, data map[string]any, memberID string) {
	ctx := r.Context()
	r.Body = http.MaxBytesReader(w, r.Body, maxWFHFormBytes)

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	date := r.FormValue("date")
	if date == "" {
		h.renderWFHRequestFormAt(w, r, data, memberID, "Date is required.", "")
		return
	}

	if _, err := time.Parse("2006-01-02", date); err != nil {
		// Selected date is preserved on the form so the user can
		// correct the format without re-picking the day.
		h.renderWFHRequestFormAt(w, r, data, memberID, "Invalid date format, expected YYYY-MM-DD.", date)
		return
	}

	// Enforce the request horizon. The date was already validated as parseable
	// above, so ValidateRequestDate can only fail with ErrWFHDateTooFar here.
	if h.wfhService != nil {
		if err := h.wfhService.ValidateRequestDate(date); err != nil {
			horizon := h.wfhService.Config().RequestHorizonDays
			h.renderWFHRequestFormAt(w, r, data, memberID, wfhBeyondHorizonMessage(horizon), date)
			return
		}
	}

	// Check quota.
	if h.wfhService != nil {
		hasQuota, err := h.wfhService.CheckQuota(ctx, memberID, date)
		if err != nil {
			h.renderWFHRequestFormAt(w, r, data, memberID, "Failed to check WFH quota.", date)
			return
		}
		if !hasQuota {
			h.renderWFHRequestFormAt(w, r, data, memberID, "You have reached your WFH quota for this period.", date)
			return
		}
	}

	if _, err := h.db.CreateWFHRequest(ctx, memberID, date); err != nil {
		h.renderWFHRequestFormAt(w, r, data, memberID, wfhWebErrorMessage(err), date)
		return
	}

	http.Redirect(w, r, "/wfh", http.StatusSeeOther)
}

// renderWFHRequestFormAt is the full form renderer with an explicit
// selected date. selectedDate="" falls back to today. The selected
// date drives the quota banner so it reflects the period the user
// is requesting for, not always the current period.
func (h *Handler) renderWFHRequestFormAt(w http.ResponseWriter, r *http.Request, data map[string]any, memberID, errMsg, selectedDate string) {
	ctx := r.Context()

	if errMsg != "" {
		data["Error"] = errMsg
	}
	// Use UTC for "today" so the form's min attribute matches the server's
	// UTC-based date comparisons. Mismatch would let a user in a positive
	// offset pick a date the server then rejects (or vice versa).
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	data["Today"] = today.Format("2006-01-02")

	if selectedDate == "" {
		selectedDate = data["Today"].(string)
	}
	data["SelectedDate"] = selectedDate

	if h.wfhService != nil {
		h.populateWFHRequestFormData(ctx, data, memberID, selectedDate, today)
	}

	if err := h.tmpl.ExecuteTemplate(w, "wfh_request.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// populateWFHRequestFormData fills the quota, current-period, next-period,
// and holiday fields on the form data map. Extracted from
// renderWFHRequestFormAt to keep the orchestrator cyclomatic complexity
// under the 10-branch limit — the period-bounds + quota-fetch logic
// naturally nests 3 levels deep.
//
// Errors from individual lookups are intentionally swallowed: a missing
// quota, missing current-period quota, or missing next-period quota
// each just leaves that field unset. The corresponding template block
// (wrapped in `{{if .Field}}`) hides the banner, so the form still
// renders. The server-side CheckQuota is the authoritative guard.
func (h *Handler) populateWFHRequestFormData(ctx context.Context, data map[string]any, memberID, selectedDate string, today time.Time) {
	data["MaxRequestDate"] = h.wfhService.MaxRequestDate().Format("2006-01-02")

	// Quota for the selected date's period. The form banner must
	// reflect the period the user is requesting for, not always
	// today: a user with 0 remaining in the current month can
	// still have 2 remaining in the next month, and the form
	// should show that instead of misleadingly saying "no
	// tokens anywhere".
	selectedQuota, qerr := h.wfhService.GetQuotaStatusForDate(ctx, memberID, parseDateOr(selectedDate, today))
	if qerr == nil {
		data["Quota"] = selectedQuota
		data["QuotaExhausted"] = selectedQuota.Remaining == 0
	}

	// Also precompute the current period and the next period so
	// the inline script can swap the banner without a server
	// round-trip when the user picks a date in a different
	// period. Both lookups go through the same code path so the
	// values stay consistent with what CheckQuota would compute
	// on submit.
	currentQuota, qerr := h.wfhService.GetQuotaStatus(ctx, memberID)
	if qerr == nil {
		data["CurrentPeriodQuota"] = currentQuota
	}
	currentPeriodStart, _, perr := h.wfhService.ComputePeriodBounds(today)
	if perr == nil {
		// The current period's start plus one full period length
		// is the next period's start (period bounds are aligned to
		// the period anchor, so this lands exactly on the boundary
		// even when `today` is at the edge of the current period).
		nextPeriodStart := currentPeriodStart.AddDate(0, 0, h.wfhService.Config().PeriodDays)
		nextQuota, qerr := h.wfhService.GetQuotaStatusForDate(ctx, memberID, nextPeriodStart)
		if qerr == nil {
			data["NextPeriodQuota"] = nextQuota
		}
	}

	// Holiday precheck: the form disables the submit button
	// when the selected date is a holiday, mirroring the
	// CheckQuota guard. parseDateOr returns the zero time on
	// parse failure and the holiday check is a no-op then; the
	// server still rejects invalid dates on submit.
	data["SelectedDateIsHoliday"] = h.wfhService.IsHoliday(selectedDate)
}

// parseDateOr parses a YYYY-MM-DD string or returns fallback on
// failure. Used by the form renderer to coerce the selected date
// into a time.Time for quota-period computation; we don't want a
// parse failure on the form to 500 the page, so the fallback is
// "today" and the server-side POST handler is the one that
// surfaces the parse error.
func parseDateOr(date string, fallback time.Time) time.Time {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return fallback
	}
	return t
}

// handleWFHCancel lets the current user cancel their own pending WFH request.
func (h *Handler) handleWFHCancel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	user := mustGetUser(ctx)
	memberID := h.resolveMemberID(ctx, user.Email)
	if memberID == "" {
		http.Error(w, "Not a team member", http.StatusForbidden)
		return
	}

	if err := h.db.CancelWFHRequest(ctx, id, memberID); err != nil {
		http.Error(w, wfhWebErrorMessage(err), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, "/wfh", http.StatusSeeOther)
}

// wfhReportTodayFlashKey is the query-string key the report-today
// handler uses to carry the post-action outcome back to the dashboard.
// Mirrors the wfh_purged pattern used by handleWFHPurge: stateless,
// shows exactly once after the action that produced it, and keeps the
// handler from depending on cookies or session storage.
const wfhReportTodayFlashKey = "wfh_reported"

// handleWFHReportToday is the same-day "unforeseen WFH" entry point.
// The dashboard's "WFH today" button POSTs here; the handler resolves
// the session's member, calls Service.ReportToday (which creates +
// settles inline), and redirects back to the dashboard with a flash
// banner that reports the outcome.
//
// The handler never reads a member_id from the body. Authorisation is
// implicit: the route lives under safeRequireAuth, the member comes
// from the session. A non-admin cannot report WFH for another member
// because there's no field to tamper with.
func (h *Handler) handleWFHReportToday(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user := mustGetUser(ctx)
	memberID := h.resolveMemberID(ctx, user.Email)
	if memberID == "" {
		http.Error(w, errNotTeamMember, http.StatusForbidden)
		return
	}

	if h.wfhService == nil {
		http.Error(w, "WFH service is not enabled", http.StatusServiceUnavailable)
		return
	}

	req, err := h.wfhService.ReportToday(ctx, memberID)
	if err != nil {
		// Caller-visible sentinels map to a flash banner via the
		// shared WFHErrorFor table — keeps the message text in one
		// place. Anything else is a 500.
		if info, ok := database.WFHErrorFor(err); ok {
			http.Redirect(w, r, "/?"+wfhReportTodayFlashKey+"=error&reason="+url.QueryEscape(info.Message), http.StatusSeeOther)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Flash banner key is the final status so the dashboard can show
	// approved / denied distinctly.
	outcome := req.Status
	http.Redirect(w, r, "/?"+wfhReportTodayFlashKey+"="+outcome, http.StatusSeeOther)
}

// wfhReportTodayFlash surfaces the report-today outcome on the
// dashboard. Called from handleDashboard so the Today card renders
// the banner once after a report; subsequent loads ignore it.
func wfhReportTodayFlash(r *http.Request) (outcome, message string) {
	raw := r.URL.Query().Get(wfhReportTodayFlashKey)
	if raw == "" {
		return "", ""
	}
	switch raw {
	case database.WFHStatusApproved, database.WFHStatusDenied:
		return raw, ""
	case "error":
		// Failure path: report-today sent a reason= via query string.
		return "error", r.URL.Query().Get("reason")
	default:
		return "", ""
	}
}

// handleWFHSelfWithdraw lets the current user withdraw their own approved WFH
// request. Allowed as long as the WFH date has not yet passed.
func (h *Handler) handleWFHSelfWithdraw(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	user := mustGetUser(ctx)
	memberID := h.resolveMemberID(ctx, user.Email)
	if memberID == "" {
		http.Error(w, "Not a team member", http.StatusForbidden)
		return
	}

	if err := h.db.WithdrawOwnWFHRequest(ctx, id, memberID); err != nil {
		http.Error(w, wfhWebErrorMessage(err), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, "/wfh", http.StatusSeeOther)
}

// enrichedWFHRequest wraps a WFHRequest with a CanWithdraw flag for admin display.
type enrichedWFHRequest struct {
	database.WFHRequest
	CanWithdraw bool
}

// enrichWFHRequests attaches the CanWithdraw flag to each request based on its
// status and whether the WFH date has passed. nil-safe.
func enrichWFHRequests(requests []database.WFHRequest, svc *wfh.Service) []enrichedWFHRequest {
	enriched := make([]enrichedWFHRequest, len(requests))
	for i := range requests {
		canWithdraw := false
		if requests[i].Status == database.WFHStatusApproved && svc != nil {
			d, parseErr := time.Parse("2006-01-02", requests[i].Date)
			if parseErr == nil {
				canWithdraw = svc.CanWithdraw(d)
			}
		}
		enriched[i] = enrichedWFHRequest{WFHRequest: requests[i], CanWithdraw: canWithdraw}
	}
	return enriched
}

// handleWFHAdminPage shows the admin WFH management page.
func (h *Handler) handleWFHAdminPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := h.wfhBaseData(r, "wfh_manage")
	applyPurgeFlash(data, r)
	applyMarkFlash(data, r)
	data["Requests"] = h.loadAdminActionableWFH(ctx)

	if h.wfhService != nil {
		data["PurgeEnabled"] = h.wfhService.IsPurgeEnabled()
	} else {
		data["PurgeEnabled"] = false
	}

	if err := h.tmpl.ExecuteTemplate(w, "wfh_manage.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// applyPurgeFlash copies the purge confirmation query string
// (wfh_purged=N&cutoff=YYYY-MM-DD) into data, so the template can
// render the green "purged N past WFH requests" banner. The query
// string is the only carrier — it keeps the handler stateless and
// the message is rendered once, immediately after the action that
// produced it.
func applyPurgeFlash(data map[string]any, r *http.Request) {
	purged := r.URL.Query().Get(wfhPurgeFlashKey)
	if purged == "" {
		return
	}
	n, parseErr := strconv.ParseInt(purged, 10, 64)
	if parseErr != nil || n < 0 {
		return
	}
	data["PurgeFlashCount"] = n
	data["PurgeFlashCutoff"] = r.URL.Query().Get("cutoff")
}

// loadAdminActionableWFH fetches every WFH request, filters to the
// rows an admin can act on (future or current dates, ad-hoc only),
// and enriches with member names. Extracted from handleWFHAdminPage
// so that single function doesn't carry the full complexity budget.
func (h *Handler) loadAdminActionableWFH(ctx context.Context) []enrichedWFHRequest {
	all, err := h.db.GetAllWFHRequests(ctx)
	if err != nil {
		// Caller (handleWFHAdminPage) maps this to a 500 via
		// ExecuteTemplate's error path; we surface as an empty list
		// rather than panic so the page can still render an
		// empty-state for the admin.
		return nil
	}

	filtered := filterAdminActionable(all)
	// enrichWFHRequests sets the CanWithdraw flag the template
	// needs to decide whether to render the "Withdraw" button.
	enriched := enrichWFHRequests(filtered, h.wfhService)
	for i := range enriched {
		enrichWithMemberName(ctx, h.db, &enriched[i].WFHRequest)
	}
	return enriched
}

// filterAdminActionable drops past dates and recurring-WFH rows.
// Date strings sort lexicographically the same as chronologically
// in YYYY-MM-DD format, so a string compare is sufficient.
func filterAdminActionable(requests []database.WFHRequest) []database.WFHRequest {
	today := time.Now().UTC().Format("2006-01-02")
	out := requests[:0:0]
	for i := range requests {
		r := &requests[i]
		if r.Date < today {
			continue
		}
		if r.IsRecurring {
			continue
		}
		out = append(out, *r)
	}
	return out
}

// enrichWithMemberName sets the MemberName field on a single
// request by looking up the member ID. Used by the admin page
// after enrichWFHRequests wraps each row in enrichedWFHRequest
// (which doesn't carry MemberName on its own). The pointer arg
// avoids the rangeValCopy lint when the WFHRequest struct grows.
func enrichWithMemberName(ctx context.Context, db *database.DB, req *database.WFHRequest) {
	members, _ := db.GetActiveTeamMembers(ctx)
	for _, m := range members {
		if m.ID == req.MemberID {
			req.MemberName = m.Name
			return
		}
	}
}

// handleWFHAdminWithdraw handles the admin withdrawal of an approved WFH request.
func (h *Handler) handleWFHAdminWithdraw(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	user := mustGetUser(ctx)

	// Load the request before withdrawal so the notifier has the
	// member_id and date. After the UPDATE, the request is still
	// readable but its status is already 'withdrawn'.
	req, err := h.db.GetWFHRequestByID(ctx, id)
	if err != nil {
		http.Error(w, wfhWebErrorMessage(err), http.StatusBadRequest)
		return
	}

	if err := h.db.WithdrawWFHRequest(ctx, id, user.UserID); err != nil {
		http.Error(w, wfhWebErrorMessage(err), http.StatusBadRequest)
		return
	}

	h.notifierOrNil().WFHStateChanged(ctx, notify.WFHEvent{
		RequestID:  req.ID,
		MemberID:   req.MemberID,
		MemberName: h.memberName(ctx, req.MemberID),
		Date:       req.Date,
		OldStatus:  database.WFHStatusApproved,
		NewStatus:  database.WFHStatusWithdrawn,
		ActorName:  user.Name,
	})

	http.Redirect(w, r, "/admin/wfh", http.StatusSeeOther)
}

// handleWFHAdminSettle triggers a manual settlement run.
func (h *Handler) handleWFHAdminSettle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.wfhService == nil {
		http.Error(w, "WFH service is not enabled", http.StatusServiceUnavailable)
		return
	}

	if err := h.wfhService.SettlePendingRequests(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/wfh", http.StatusSeeOther)
}

// wfhMarkFlashKey is the query-string key the mark/unmark handlers
// use to carry the post-action outcome back to the admin WFH page.
// Same stateless pattern as the report-today and purge flash keys:
// the message is rendered once, immediately after the action that
// produced it.
const wfhMarkFlashKey = "wfh_marked"

// handleAdminMarkWFHPage renders the GET form for the "Mark member
// as WFH" admin action. Lists active team members (excluding the
// current admin — admins don't typically mark themselves) and locks
// the date to today. Both fields are required on submit; the date
// is hidden because the override is today-only.
func (h *Handler) handleAdminMarkWFHPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := h.wfhBaseData(r, "wfh_mark")
	applyMarkFlash(data, r)

	currentUser := mustGetUser(ctx)
	currentMemberID := h.resolveMemberID(ctx, currentUser.Email)

	members, err := h.db.GetActiveTeamMembers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type memberOption struct {
		ID   string
		Name string
	}
	options := make([]memberOption, 0, len(members))
	for _, m := range members {
		if m.ID == currentMemberID {
			continue
		}
		options = append(options, memberOption{ID: m.ID, Name: m.Name})
	}
	data["Members"] = options
	data["Today"] = time.Now().UTC().Format("2006-01-02")

	if err := h.tmpl.ExecuteTemplate(w, "wfh_mark.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// applyMarkFlash copies the post-mark outcome from the query
// string into the template data so the form can render a one-time
// confirmation banner. The query string carries two fields:
//   - wfh_marked=ok|error|already
//   - member=<name>  (on success, the member the admin just marked)
func applyMarkFlash(data map[string]any, r *http.Request) {
	raw := r.URL.Query().Get(wfhMarkFlashKey)
	if raw == "" {
		return
	}
	data["MarkFlashOutcome"] = raw
	data["MarkFlashMember"] = r.URL.Query().Get("member")
	data["MarkFlashReason"] = r.URL.Query().Get("reason")
}

// handleAdminMarkWFH handles the POST that creates the admin-marked
// WFH row. The form supplies the member_id and a hidden date (today).
// Both are required. The handler calls Service.MarkWFH, which
// enforces: feature enabled, valid date, member exists. The service
// does NOT enforce quota or capacity floor — the mark is an
// override.
//
// Security: the route lives under safeRequireAdmin so admin status
// is implicit. The handler still validates that the date is today
// (defense in depth — a crafted body can't mark a member for a
// future date; the service will reject it as ErrWFHInvalidDate).
// A crafted body cannot mark the admin themselves: the form does
// not include the admin in the member dropdown.
func (h *Handler) handleAdminMarkWFH(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.wfhService == nil {
		http.Error(w, "WFH service is not enabled", http.StatusServiceUnavailable)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	memberID := r.FormValue("member_id")
	date := r.FormValue("date")
	if memberID == "" {
		http.Redirect(w, r, "/admin/wfh/mark?"+wfhMarkFlashKey+"=error&reason="+url.QueryEscape("Please select a member."), http.StatusSeeOther)
		return
	}

	user := mustGetUser(ctx)

	req, err := h.wfhService.MarkWFH(ctx, memberID, date, user.UserID, user.Name)
	if err != nil {
		if info, ok := database.WFHErrorFor(err); ok {
			// "Already marked" surfaces a distinct flash so the
			// admin form does not look broken.
			if errors.Is(err, database.ErrWFHDuplicateRequest) {
				http.Redirect(w, r, "/admin/wfh?"+wfhMarkFlashKey+"=already&member="+url.QueryEscape(req.MemberID), http.StatusSeeOther)
				return
			}
			http.Redirect(w, r, "/admin/wfh/mark?"+wfhMarkFlashKey+"=error&reason="+url.QueryEscape(info.Message), http.StatusSeeOther)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	memberName := h.memberName(ctx, req.MemberID)
	http.Redirect(w, r, "/admin/wfh?"+wfhMarkFlashKey+"=ok&member="+url.QueryEscape(memberName), http.StatusSeeOther)
}

// handleAdminMarkWFHUnmark handles the POST that withdraws an
// admin-marked WFH row. The row is deleted via the existing
// WithdrawWFHRequest path, which preserves the audit trail
// (withdrawn_by, withdrawn_at) and frees the quota slot. The
// "unmark" verb is just a flash wording on top of the existing
// withdraw action; the storage layer is the same.
func (h *Handler) handleAdminMarkWFHUnmark(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	user := mustGetUser(ctx)

	req, err := h.db.GetWFHRequestByID(ctx, id)
	if err != nil {
		http.Error(w, wfhWebErrorMessage(err), http.StatusBadRequest)
		return
	}
	if !req.IsAdminMarked {
		// Defense in depth: the button is only rendered for
		// admin-marked rows, but a tampered form should not be
		// able to use this endpoint to withdraw a user-requested
		// row (that goes through /admin/wfh/{id}/withdraw).
		http.Error(w, "this row is not an admin mark", http.StatusBadRequest)
		return
	}

	if err := h.db.WithdrawWFHRequest(ctx, id, user.UserID); err != nil {
		http.Error(w, wfhWebErrorMessage(err), http.StatusBadRequest)
		return
	}

	h.notifierOrNil().WFHStateChanged(ctx, notify.WFHEvent{
		RequestID:  req.ID,
		MemberID:   req.MemberID,
		MemberName: h.memberName(ctx, req.MemberID),
		Date:       req.Date,
		OldStatus:  database.WFHStatusApproved,
		NewStatus:  database.WFHStatusWithdrawn,
		ActorName:  user.Name,
	})

	http.Redirect(w, r, "/admin/wfh?"+wfhMarkFlashKey+"=unmarked&member="+url.QueryEscape(h.memberName(ctx, req.MemberID)), http.StatusSeeOther)
}

// wfhPurgeFlashKey is the query-string key used to carry the post-purge
// confirmation message from the POST handler back to the redirect target
// on the admin WFH page. Query-string transport keeps the implementation
// stateless and avoids any session/cookie plumbing — the message is
// only ever shown once, immediately after the action that produced it.
const wfhPurgeFlashKey = "wfh_purged"

// handleWFHPurge serves the admin past-period purge page (GET) and
// commits the purge when the form is confirmed (POST). Both routes are
// mounted under safeRequireAdmin middleware, so admin status is implicit.
//
// When the WFH feature or purge is disabled the page renders a "not
// available" message instead of a destructive form — keeping the route
// addressable but the button hidden, which matches how the CLI errors
// out and the scheduler skips the purge.
func (h *Handler) handleWFHPurge(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := h.wfhBaseData(r, "wfh_purge")

	if h.wfhService == nil {
		data["Error"] = "WFH service is not enabled."
		h.renderWFHPurge(w, r, data)
		return
	}
	if !h.wfhService.IsPurgeEnabled() {
		data["Error"] = "Past-period purge is disabled (WFH_ENABLED=false or WFH_PURGE_ENABLED=false)."
		h.renderWFHPurge(w, r, data)
		return
	}

	cutoff, wouldDelete, err := h.wfhService.PurgePastPeriodsDryRun(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data["Cutoff"] = cutoff
	data["WouldDelete"] = wouldDelete

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.FormValue("confirm") != "true" {
			http.Error(w, "Confirmation required.", http.StatusBadRequest)
			return
		}
		// Re-compute on POST so the dry-run count cannot drift from
		// the actual delete — if anything changed between the GET and
		// the POST (e.g. settlement ran on the scheduler tick) we want
		// the count we delete to match the value we just showed.
		cutoff, deleted, err := h.wfhService.PurgePastPeriods(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/admin/wfh?"+wfhPurgeFlashKey+"="+strconv.FormatInt(deleted, 10)+"&cutoff="+cutoff, http.StatusSeeOther)
		return
	}

	h.renderWFHPurge(w, r, data)
}

// renderWFHPurge executes the purge template. The data map is expected
// to contain either Error (when disabled) or Cutoff + WouldDelete.
func (h *Handler) renderWFHPurge(w http.ResponseWriter, _ *http.Request, data map[string]any) {
	if err := h.tmpl.ExecuteTemplate(w, "wfh_purge.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// wfhWebErrorMessage returns a user-facing message for WFH domain errors.
//
// The message comes from the shared database.WFHErrorFor table so adding
// a new ErrWFH* sentinel only requires an entry in that table. A nil
// error maps to an empty string so callers can pass through a possibly-
// nil result without first guarding the call site.
func wfhWebErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	if info, ok := database.WFHErrorFor(err); ok {
		return info.Message
	}
	return err.Error()
}

// wfhBeyondHorizonMessage formats a user-facing message for a request that
// falls beyond the configured request horizon, with singular/plural handling.
func wfhBeyondHorizonMessage(horizonDays int) string {
	if horizonDays == 1 {
		return "WFH requests can only be made up to 1 day in advance."
	}
	return fmt.Sprintf("WFH requests can only be made up to %d days in advance.", horizonDays)
}
