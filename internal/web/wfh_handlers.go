package web

import (
	"errors"
	"fmt"
	"net/http"
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

	h.renderWFHRequestForm(w, r, data, memberID, "")
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
		h.renderWFHRequestForm(w, r, data, memberID, "Date is required.")
		return
	}

	if _, err := time.Parse("2006-01-02", date); err != nil {
		h.renderWFHRequestForm(w, r, data, memberID, "Invalid date format, expected YYYY-MM-DD.")
		return
	}

	// Enforce the request horizon. The date was already validated as parseable
	// above, so ValidateRequestDate can only fail with ErrWFHDateTooFar here.
	if h.wfhService != nil {
		if err := h.wfhService.ValidateRequestDate(date); err != nil {
			horizon := h.wfhService.Config().RequestHorizonDays
			h.renderWFHRequestForm(w, r, data, memberID, wfhBeyondHorizonMessage(horizon))
			return
		}
	}

	// Check quota.
	if h.wfhService != nil {
		hasQuota, err := h.wfhService.CheckQuota(ctx, memberID, date)
		if err != nil {
			h.renderWFHRequestForm(w, r, data, memberID, "Failed to check WFH quota.")
			return
		}
		if !hasQuota {
			h.renderWFHRequestForm(w, r, data, memberID, "You have reached your WFH quota for this period.")
			return
		}
	}

	if _, err := h.db.CreateWFHRequest(ctx, memberID, date); err != nil {
		h.renderWFHRequestForm(w, r, data, memberID, wfhWebErrorMessage(err))
		return
	}

	http.Redirect(w, r, "/wfh", http.StatusSeeOther)
}

func (h *Handler) renderWFHRequestForm(w http.ResponseWriter, r *http.Request, data map[string]any, memberID, errMsg string) {
	ctx := r.Context()

	if errMsg != "" {
		data["Error"] = errMsg
	}
	// Use UTC for "today" so the form's min attribute matches the server's
	// UTC-based date comparisons. Mismatch would let a user in a positive
	// offset pick a date the server then rejects (or vice versa).
	now := time.Now().UTC()
	data["Today"] = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Format("2006-01-02")

	if h.wfhService != nil {
		data["MaxRequestDate"] = h.wfhService.MaxRequestDate().Format("2006-01-02")
		quota, err := h.wfhService.GetQuotaStatus(ctx, memberID)
		if err == nil {
			data["Quota"] = quota
		}
	}

	if err := h.tmpl.ExecuteTemplate(w, "wfh_request.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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

	requests, err := h.db.GetAllWFHRequests(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Enrich with member names.
	members, _ := h.db.GetActiveTeamMembers(ctx)
	memberMap := make(map[string]string, len(members))
	for _, m := range members {
		memberMap[m.ID] = m.Name
	}
	for i := range requests {
		if name, ok := memberMap[requests[i].MemberID]; ok {
			requests[i].MemberName = name
		}
	}

	data["Requests"] = enrichWFHRequests(requests, h.wfhService)

	if err := h.tmpl.ExecuteTemplate(w, "wfh_manage.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

// wfhWebErrorMessage returns a user-facing message for WFH domain errors.
//
//nolint:cyclop // Exhaustive domain-to-message mapping; each sentinel is a case.
func wfhWebErrorMessage(err error) string {
	switch {
	case errors.Is(err, database.ErrWFHNotFound):
		return "WFH request not found."
	case errors.Is(err, database.ErrWFHNotOwner):
		return "You can only modify your own WFH requests."
	case errors.Is(err, database.ErrWFHAlreadySettled):
		return "This WFH request has already been settled and cannot be cancelled."
	case errors.Is(err, database.ErrWFHDuplicateRequest):
		return "A WFH request already exists for this date."
	case errors.Is(err, database.ErrWFHDatePassed):
		return "The selected date has already passed."
	case errors.Is(err, database.ErrWFHDateTooFar):
		return "WFH requests can only be made up to a limited number of days in advance."
	case errors.Is(err, database.ErrWFHRecurringContractDay):
		return "This date falls on your contractual recurring WFH day."
	case errors.Is(err, database.ErrWFHOnHoliday):
		return "WFH requests cannot be made for holidays."
	case errors.Is(err, database.ErrWFHNotApproved):
		return "Only approved WFH requests can be withdrawn."
	default:
		return err.Error()
	}
}

// wfhBeyondHorizonMessage formats a user-facing message for a request that
// falls beyond the configured request horizon, with singular/plural handling.
func wfhBeyondHorizonMessage(horizonDays int) string {
	if horizonDays == 1 {
		return "WFH requests can only be made up to 1 day in advance."
	}
	return fmt.Sprintf("WFH requests can only be made up to %d days in advance.", horizonDays)
}
