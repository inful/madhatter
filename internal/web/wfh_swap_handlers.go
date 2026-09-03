package web

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/database"
)

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
		http.Redirect(w, r, "/wfh/"+id+"/swap", http.StatusSeeOther)
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

	// 409-conflict guard: refuse if a pending swap already
	// exists for this assigned row.
	if existing, _ := h.db.GetPendingWFHSwapForRequesterRow(ctx, id); existing != nil {
		http.Redirect(w, r, "/wfh?flash=swap_exists", http.StatusSeeOther)
		return
	}

	// Confirm target is eligible. The form only listed
	// eligible targets but a tampered POST could submit any
	// ID; re-check before persisting.
	var eligible bool
	if h.wfhService != nil {
		targets, err := h.wfhService.EligibleSwapTargets(ctx, wfh.Date, memberID)
		if err != nil {
			slog.ErrorContext(ctx, "eligibility check failed", "error", err)
			http.Error(w, "Failed to validate swap target", http.StatusInternalServerError)
			return
		}
		for i := range targets {
			if targets[i].ID == targetID {
				eligible = true
				break
			}
		}
	}
	if !eligible {
		http.Error(w, "Swap target is not eligible (on leave, WFH, exempt, or self)", http.StatusConflict)
		return
	}

	if _, err := h.db.CreateWFHAssignmentSwap(ctx, id, targetID, wfh.Date); err != nil {
		slog.ErrorContext(ctx, "create swap failed", "error", err)
		http.Error(w, "Failed to create swap", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/wfh?flash=swap_requested", http.StatusSeeOther)
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

	http.Redirect(w, r, "/wfh?flash=swap_cancelled", http.StatusSeeOther)
}

// transitionWFHSwap is the shared handler for accept / reject.
// Both routes are POST /wfh/swap/{swapId}/{verb} and the
// body is empty; the verb is encoded in the route. The
// shared guard is: target must equal the current user; status
// must be pending.
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

	http.Redirect(w, r, redirectOnSuccess, http.StatusSeeOther)
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
