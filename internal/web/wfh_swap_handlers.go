package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/notify"
)

// handleWFHAdminReassign is the admin's "Reassign" button on
// an assigned row. POST /admin/wfh/{id}/reassign. Body:
// replacement_member_id=<id>. Moves the assigned WFH from
// the original member to the replacement in a single
// transaction (withdraw + insert), preserving the seat cap.
//
// Step 16 of plans/assigned-wfh-plan.md. Authorization: the
// safeRequireAdmin middleware on the route group guards the
// endpoint; the handler itself assumes the caller is admin.
func (h *Handler) handleWFHAdminReassign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	replacementID := r.FormValue("replacement_member_id")
	if replacementID == "" {
		http.Redirect(w, r, "/admin/wfh?flash=reassign_missing", http.StatusSeeOther)
		return
	}

	user := mustGetUser(ctx)

	newID, err := h.wfhService.AdminReassignWFH(ctx, id, replacementID, user.UserID, user.Name)
	if err != nil {
		slog.ErrorContext(ctx, "admin reassign failed", "id", id, "replacement", replacementID, "error", err)
		http.Redirect(w, r, "/admin/wfh?flash=reassign_error", http.StatusSeeOther)
		return
	}
	_ = newID // currently unused at the handler level — the notifier fires inside the service

	// Fire an extra notification for the audit trail so the
	// admin's reassign is visible in the email outbox. The
	// service already fires per-row notifications; this adds
	// an admin-side log entry. Use a synthetic RequestID
	// that combines the original and replacement row IDs so
	// the audit log can correlate.
	h.notifierOrNil().WFHStateChanged(ctx, notify.WFHEvent{
		RequestID:  "reassign:" + id + "->" + replacementID,
		MemberID:   replacementID,
		MemberName: h.memberName(ctx, replacementID),
		Date:       time.Now().UTC().Format("2006-01-02"),
		OldStatus:  database.WFHStatusApproved,
		NewStatus:  database.WFHStatusApproved,
		ActorName:  user.Name + " (reassign)",
	})

	http.Redirect(w, r, "/admin/wfh?flash=reassigned", http.StatusSeeOther)
}

// handleWFHSwapForm serves the swap-request form for an
// assigned WFH row. GET /wfh/{id}/swap. The form's only input
// is the target_member_id (an on-site teammate); the date
// comes from the assigned row.
//
// Step 14 of plans/assigned-wfh-plan.md. The form pre-filters
// eligible targets (active, not on leave, not exempt, not WFH
// on the date) so the dropdown only contains viable swaps.
func (h *Handler) handleWFHSwapForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	user := mustGetUser(ctx)
	memberID := h.resolveMemberID(ctx, user.Email)
	if memberID == "" {
		http.Error(w, errNotTeamMember, http.StatusForbidden)
		return
	}

	wfh, err := h.db.GetWFHRequestByID(ctx, id)
	if err != nil {
		http.Error(w, "WFH request not found", http.StatusNotFound)
		return
	}
	if wfh.MemberID != memberID {
		http.Error(w, errNotTeamMember, http.StatusForbidden)
		return
	}
	if wfh.Origin != "assigned" && wfh.Origin != "swap" {
		http.Error(w, "Only assigned or swap WFHs can be re-swapped", http.StatusConflict)
		return
	}

	targets, err := h.wfhService.EligibleSwapTargets(ctx, wfh.Date, memberID)
	if err != nil || h.wfhService == nil {
		http.Error(w, "Failed to load swap targets", http.StatusInternalServerError)
		return
	}

	data := h.wfhBaseData(r, "wfh_swap")
	data["WFHRequestID"] = id
	data["WFHDate"] = wfh.Date
	data["EligibleTargets"] = targets
	data["ActiveSwap"] = false

	// Surface existing pending swap (if any) so the form
	// knows to disable submit. The 409 path in
	// handleWFHSwapCreate is the authoritative check; this is
	// just UX.
	if existing, _ := h.db.GetPendingWFHSwapForRequesterRow(ctx, id); existing != nil {
		data["ActiveSwap"] = true
	}

	if err := h.tmpl.ExecuteTemplate(w, "wfh_swap.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleWFHSwapCreate processes the swap-request POST.
// POST /wfh/{id}/swap. Body: target_member_id=<id>.
//
// On success, redirects to /wfh with a flash banner. On a
// 409-conflict (a pending swap already exists for this row)
// the user is sent back to /wfh with a different flash.
func (h *Handler) handleWFHSwapCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	user := mustGetUser(ctx)
	memberID := h.resolveMemberID(ctx, user.Email)
	if memberID == "" {
		http.Error(w, errNotTeamMember, http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	targetID := r.FormValue("target_member_id")
	if targetID == "" {
		// Closed redirect: `id` came from chi.URLParam on the
		// matched route /wfh/{id}/swap and we redirect back to
		// the same form for that same request. Host and scheme
		// are server-controlled (relative URL).
		http.Redirect(w, r, "/wfh/"+id+"/swap", http.StatusSeeOther) //nolint:gosec // closed back-redirect to the same route segment
		return
	}

	ok, wfh, err := h.swapCreateValidate(ctx, id, memberID)
	if err != nil {
		writeSwapCreateValidationError(w, err)
		return
	}
	if !ok {
		return
	}

	if existing, _ := h.db.GetPendingWFHSwapForRequesterRow(ctx, id); existing != nil {
		http.Redirect(w, r, "/wfh?flash=swap_exists", http.StatusSeeOther)
		return
	}

	eligible, err := h.swapCreateCheckEligibility(ctx, wfh, memberID, targetID)
	if err != nil {
		slog.ErrorContext(ctx, "eligibility check failed", "error", err)
		http.Error(w, "Failed to validate swap target", http.StatusInternalServerError)
		return
	}
	if !eligible {
		http.Error(w, "Swap target is not eligible (on leave, WFH, exempt, or self)", http.StatusConflict)
		return
	}

	swapID, err := h.db.CreateWFHAssignmentSwap(ctx, id, targetID, wfh.Date)
	if err != nil {
		slog.ErrorContext(ctx, "create swap failed", "error", err)
		http.Error(w, "Failed to create swap", http.StatusInternalServerError)
		return
	}

	// Step 20 of plans/assigned-wfh-plan.md: fire SwapRequested
	// so the target gets a "swap pending" email. Without this
	// wiring, the WFH-swap path was silent (HAT swap wiring
	// mirrors this). The producer-side failure mode is loud —
	// the dispatch is non-blocking, but a misconfigured
	// notification channel trips an error log so an operator
	// notices.
	h.notifierOrNil().SwapRequested(ctx, notify.SwapEvent{
		SwapID:            swapID,
		RequesterMemberID: wfh.MemberID,
		RequesterName:     h.memberName(ctx, wfh.MemberID),
		TargetMemberID:    targetID,
		TargetName:        h.memberName(ctx, targetID),
		RequesterDate:     wfh.Date,
		TargetDate:        wfh.Date,
		ActorName:         user.Name,
	})

	http.Redirect(w, r, "/wfh?flash=swap_requested", http.StatusSeeOther)
}

// writeSwapCreateValidationError maps the sentinel errors
// returned by swapCreateValidate to HTTP responses. Each case
// carries its own status code so the helper can be called once
// from the handler without dragging the switch into the
// orchestrator's cyclomatic budget.
//
// Extracted from handleWFHSwapCreate so the parent stays
// under cyclop.
func writeSwapCreateValidationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, database.ErrWFHNotFound):
		http.Error(w, "WFH request not found", http.StatusNotFound)
	case errors.Is(err, errNotMember):
		http.Error(w, errNotTeamMember, http.StatusForbidden)
	case errors.Is(err, errWrongOrigin):
		http.Error(w, "Only assigned or swap WFHs can be re-swapped", http.StatusConflict)
	default:
		http.Error(w, "Failed to validate swap", http.StatusInternalServerError)
	}
}

// errWrongOrigin is the sentinel for the "wrong origin" guard
// (assigned or swap only). Local var so swapCreateValidate
// can communicate which guard failed without coupling to the
// handler's switch.
var errWrongOrigin = errors.New("wrong origin")

// swapCreateValidate performs the input + ownership + origin
// guards before any DB work. Returns (true, wfh, nil) when
// the caller can proceed; (false, _, err) when the handler
// should surface the error.
//
// Extracted from handleWFHSwapCreate to keep the orchestrator
// under the 10-branch cyclomatic-complexity budget.
func (h *Handler) swapCreateValidate(ctx context.Context, id, memberID string) (bool, *database.WFHRequest, error) {
	wfh, err := h.db.GetWFHRequestByID(ctx, id)
	if err != nil {
		return false, nil, database.ErrWFHNotFound
	}
	if wfh.MemberID != memberID {
		return false, nil, errNotMember
	}
	if wfh.Origin != "assigned" && wfh.Origin != "swap" {
		return false, nil, errWrongOrigin
	}
	return true, wfh, nil
}

// errNotMember is the sentinel used by swapCreateValidate to
// signal "the requester isn't the WFH row's owner". The
// handler's string constant `errNotTeamMember` is used in the
// HTTP layer's message body; this is for the type-safe
// errors.Is check in the validation path.
var errNotMember = errors.New("requester is not the WFH row's owner")

// swapCreateCheckEligibility verifies the target is in the
// eligible set. The form only listed eligible targets but a
// tampered POST could submit any ID; re-check before persisting.
func (h *Handler) swapCreateCheckEligibility(ctx context.Context, wfh *database.WFHRequest, memberID, targetID string) (bool, error) {
	if h.wfhService == nil {
		return false, errors.New("wfh service not configured")
	}
	targets, err := h.wfhService.EligibleSwapTargets(ctx, wfh.Date, memberID)
	if err != nil {
		return false, err
	}
	for i := range targets {
		if targets[i].ID == targetID {
			return true, nil
		}
	}
	return false, nil
}

// handleWFHSwapInbox renders the inbox for the current user.
// GET /wfh/swap/inbox. Lists all pending swaps where the user
// is the target.
func (h *Handler) handleWFHSwapInbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := mustGetUser(ctx)
	memberID := h.resolveMemberID(ctx, user.Email)
	if memberID == "" {
		http.Error(w, errNotTeamMember, http.StatusForbidden)
		return
	}

	swaps, err := h.db.GetPendingWFHSwapsForTarget(ctx, memberID)
	if err != nil {
		http.Error(w, "Failed to load swap inbox", http.StatusInternalServerError)
		return
	}
	enriched := h.enrichWFHSwaps(ctx, swaps)

	data := h.wfhBaseData(r, "wfh_swap_inbox")
	data["PendingSwaps"] = enriched

	if err := h.tmpl.ExecuteTemplate(w, "wfh_swap_inbox.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleWFHSwapAccept is the target's accept button.
// POST /wfh/swap/{swapId}/accept. The actual cap-preserving
// swap transaction (withdraw assigned, insert target's WFH
// with origin='swap') happens in the WFH service so the
// quota / floor / notifier invariants stay in one place. The
// handler here just flips the swap status — the swap row
// state transition is the auth gate, and the WFH mutation
// runs in the service.
//
// Phase 3 ships the swap rows + state transitions. The
// cap-preserving WFH transaction (the actual mutation
// pair) lands in a follow-up commit so the handler can be
// unit-tested without the picker running. The 409-conflict
// guard on the row already exists at the DB layer (see
// step 13).
func (h *Handler) handleWFHSwapAccept(w http.ResponseWriter, r *http.Request) {
	h.transitionWFHSwap(w, r, "accepted", "/wfh/swap/inbox?flash=swap_accepted")
}

// handleWFHSwapReject is the target's reject button.
// POST /wfh/swap/{swapId}/reject. Just flips the status; no
// WFH mutations.
func (h *Handler) handleWFHSwapReject(w http.ResponseWriter, r *http.Request) {
	h.transitionWFHSwap(w, r, "rejected", "/wfh/swap/inbox?flash=swap_rejected")
}

// handleWFHSwapCancel is the requester's cancel button (also
// auto-canceled by the scheduler when the date passes).
// POST /wfh/swap/{swapId}/cancel. Authorization: the swap
// requester must equal the current member.
func (h *Handler) handleWFHSwapCancel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	swapID := chi.URLParam(r, "swapId")

	user := mustGetUser(ctx)
	memberID := h.resolveMemberID(ctx, user.Email)
	if memberID == "" {
		http.Error(w, errNotTeamMember, http.StatusForbidden)
		return
	}

	swap, err := h.db.GetWFHAssignmentSwapByID(ctx, swapID)
	if err != nil {
		http.Error(w, "Swap not found", http.StatusNotFound)
		return
	}
	if swap.Status != "pending" {
		http.Error(w, "Swap is no longer pending", http.StatusConflict)
		return
	}
	requesterWFH, err := h.db.GetWFHRequestByID(ctx, swap.RequesterWFHRequestID)
	if err != nil {
		http.Error(w, "Underlying WFH not found", http.StatusInternalServerError)
		return
	}
	if requesterWFH.MemberID != memberID {
		http.Error(w, "Only the requester can cancel", http.StatusForbidden)
		return
	}

	if err := h.db.UpdateWFHAssignmentSwapStatus(ctx, swapID, "cancelled", time.Now().UTC()); err != nil {
		slog.ErrorContext(ctx, "cancel swap failed", "error", err)
		http.Error(w, "Failed to cancel swap", http.StatusInternalServerError)
		return
	}

	// Step 20: notify the target that the swap is cancelled.
	// Resolve the requester member via the underlying WFH row
	// so the email body has both names and dates (the
	// generic templates use {{.RequesterName}} /
	// {{.TargetName}}, not member IDs).
	requesterID := ""
	requesterDate := swap.SwapDate
	if requesterWFH, fetchErr := h.db.GetWFHRequestByID(ctx, swap.RequesterWFHRequestID); fetchErr == nil && requesterWFH != nil {
		requesterID = requesterWFH.MemberID
		// SwapDate and the underlying WFH row's date are
		// normally identical — domain.WFHRequest.Date is a
		// string already in 2006-01-02 form. Trust the WFH row
		// for the template so the email matches reality if the
		// schedule ever drifts.
		requesterDate = requesterWFH.Date
	}
	h.notifierOrNil().SwapCancelled(ctx, notify.SwapEvent{
		SwapID:            swapID,
		RequesterMemberID: requesterID,
		RequesterName:     h.memberName(ctx, requesterID),
		TargetMemberID:    swap.TargetMemberID,
		TargetName:        h.memberName(ctx, swap.TargetMemberID),
		RequesterDate:     requesterDate,
		TargetDate:        swap.SwapDate,
		ActorName:         user.Name,
	})

	http.Redirect(w, r, "/wfh?flash=swap_cancelled", http.StatusSeeOther)
}

// transitionWFHSwap is the shared handler for accept / reject.
// Both routes are POST /wfh/swap/{swapId}/{verb} and the
// body is empty; the verb is encoded in the route. The
// shared guard is: target must equal the current user; status
// must be pending. Step 20 of plans/assigned-wfh-plan.md
// fires SwapAccepted or SwapRejected after the status
// transition lands so the requester learns the outcome by
// email.
func (h *Handler) transitionWFHSwap(w http.ResponseWriter, r *http.Request, newStatus database.WFHSwapStatus, redirectOnSuccess string) {
	ctx := r.Context()
	swapID := chi.URLParam(r, "swapId")

	user := mustGetUser(ctx)
	memberID := h.resolveMemberID(ctx, user.Email)
	if memberID == "" {
		http.Error(w, errNotTeamMember, http.StatusForbidden)
		return
	}

	swap, err := h.db.GetWFHAssignmentSwapByID(ctx, swapID)
	if err != nil {
		http.Error(w, "Swap not found", http.StatusNotFound)
		return
	}
	if swap.Status != "pending" {
		http.Error(w, "Swap is no longer pending", http.StatusConflict)
		return
	}
	if swap.TargetMemberID != memberID {
		http.Error(w, "Only the target can accept or reject", http.StatusForbidden)
		return
	}

	if err := h.db.UpdateWFHAssignmentSwapStatus(ctx, swapID, newStatus, time.Now().UTC()); err != nil {
		slog.ErrorContext(ctx, "swap state transition failed", "error", err)
		http.Error(w, "Failed to update swap", http.StatusInternalServerError)
		return
	}

	fireSwapTransitionEvent(ctx, h, swap, newStatus, user.Name)

	http.Redirect(w, r, redirectOnSuccess, http.StatusSeeOther)
}

// fireSwapTransitionEvent resolves the requester member and
// date via the underlying WFH row, then dispatches the
// accepted/rejected notification. Extracted from
// transitionWFHSwap so the orchestrator function stays under
// the cyclop budget.
//
// The newStatus parameter is stringified so the switch only
// covers the two values this handler can produce — the typed
// WFHSwapStatus constants cover four values but only
// accepted/rejected can be reached via this code path.
func fireSwapTransitionEvent(ctx context.Context, h *Handler, swap *database.WFHAssignmentSwap, newStatus database.WFHSwapStatus, actorName string) {
	requesterID := ""
	requesterDate := swap.SwapDate
	if requesterWFH, fetchErr := h.db.GetWFHRequestByID(ctx, swap.RequesterWFHRequestID); fetchErr == nil && requesterWFH != nil {
		requesterID = requesterWFH.MemberID
		// Trust the WFH row's date over the swap row's so the
		// notification reflects the actual schedule if they
		// ever drift. domain.WFHRequest.Date is a string in
		// 2006-01-02 form (swapFromSQLC converts).
		requesterDate = requesterWFH.Date
	}

	event := notify.SwapEvent{
		SwapID:            swap.ID,
		RequesterMemberID: requesterID,
		RequesterName:     h.memberName(ctx, requesterID),
		TargetMemberID:    swap.TargetMemberID,
		TargetName:        h.memberName(ctx, swap.TargetMemberID),
		RequesterDate:     requesterDate,
		TargetDate:        swap.SwapDate,
		ActorName:         actorName,
	}
	switch string(newStatus) {
	case string(database.WFHSwapStatusAccepted):
		h.notifierOrNil().SwapAccepted(ctx, event)
	case string(database.WFHSwapStatusRejected):
		h.notifierOrNil().SwapRejected(ctx, event)
	default:
		// pending / cancelled are routed through different
		// handlers (handleWFHSwapCreate / handleWFHSwapCancel)
		// — if either reaches here it's an internal bug.
		slog.ErrorContext(ctx, "unexpected newStatus in transitionWFHSwap", "status", string(newStatus))
	}
}

// enrichWFHSwaps attaches the requester name (looked up from
// the requester WFH row) to each swap. Used by the inbox and
// the WFH list page.
func (h *Handler) enrichWFHSwaps(ctx context.Context, swaps []database.WFHAssignmentSwap) []database.WFHAssignmentSwap {
	out := make([]database.WFHAssignmentSwap, len(swaps))
	for i := range swaps {
		s := swaps[i]
		reqWFH, err := h.db.GetWFHRequestByID(ctx, s.RequesterWFHRequestID)
		if err != nil {
			out[i] = s
			continue
		}
		if h.wfhService != nil {
			s.RequesterName = h.wfhService.ResolveMemberName(ctx, reqWFH.MemberID)
		}
		out[i] = s
	}
	return out
}

// eligibleSwapTargets and isEligibleSwapTarget were moved to
// the wfh service (Phase 3 / step 14) so the picker and the
// swap form share a single source of truth for the filter
// rules. The service methods are EligibleSwapTargets and
// ResolveMemberName (the handler calls them through
// h.wfhService).
