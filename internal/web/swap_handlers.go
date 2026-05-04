package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
)

// resolveMemberID resolves the team member ID for the currently logged-in user.
// Returns empty string if the user is not a team member.
func (h *Handler) resolveMemberID(ctx context.Context, email string) string {
	member, err := h.db.GetMemberByEmail(ctx, email)
	if err != nil || member == nil {
		return ""
	}

	return member.ID
}

func (h *Handler) handleSwaps(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user, ok := auth.GetUserFromContext(ctx)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	memberID := h.resolveMemberID(ctx, user.Email)

	data := map[string]any{
		"Template": "swaps",
		"User":     user,
		"IsAdmin":  auth.IsAdminSession(user),
		"MemberID": memberID,
	}

	if memberID == "" {
		delete(data, "MemberID")
		data["Error"] = "You are not registered as a team member."
		if err := h.tmpl.ExecuteTemplate(w, "swaps.html", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if r.Method == http.MethodPost {
		h.handleSwapRequestPost(w, r, data, memberID)
		return
	}

	h.renderSwapsPage(w, r, data, memberID, "")
}

func (h *Handler) handleSwapRequestPost(w http.ResponseWriter, r *http.Request, data map[string]any, memberID string) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	requesterAssignmentID := r.FormValue("requester_assignment_id")
	targetAssignmentID := r.FormValue("target_assignment_id")

	if requesterAssignmentID == "" || targetAssignmentID == "" {
		h.renderSwapsPage(w, r, data, memberID, "Please select both assignments.")
		return
	}

	reqAssignment, tgtAssignment, err := h.db.ValidateSwapAssignments(ctx, requesterAssignmentID, targetAssignmentID, memberID)
	if err != nil {
		h.renderSwapsPage(w, r, data, memberID, swapValidationErrorMessage(err))
		return
	}

	if err = h.db.CheckNoOpenSwaps(ctx, requesterAssignmentID, targetAssignmentID); err != nil {
		h.renderSwapsPage(w, r, data, memberID, "One of the selected assignments already has an open swap request.")
		return
	}

	if _, err = h.db.CreateHatSwap(ctx, requesterAssignmentID, targetAssignmentID, reqAssignment.MemberID, tgtAssignment.MemberID); err != nil {
		h.renderSwapsPage(w, r, data, memberID, err.Error())
		return
	}

	http.Redirect(w, r, "/swaps", http.StatusSeeOther)
}

// swapValidationErrorMessage maps domain errors from ValidateSwapAssignments to user-facing messages.
func swapValidationErrorMessage(err error) string {
	switch {
	case errors.Is(err, database.ErrSwapSameAssignment):
		return "Cannot swap an assignment with itself."
	case errors.Is(err, database.ErrRequesterAssignmentNotFound):
		return "Your assignment was not found."
	case errors.Is(err, database.ErrTargetAssignmentNotFound):
		return "Target assignment was not found."
	case errors.Is(err, database.ErrSwapNotOwner):
		return "You can only swap your own assignments."
	case errors.Is(err, database.ErrSwapTargetSelf):
		return "You can only request swaps with another member."
	case errors.Is(err, database.ErrSwapRequesterDatePassed):
		return "Your HAT day has already passed and cannot be swapped."
	case errors.Is(err, database.ErrSwapTargetDatePassed):
		return "The target HAT day has already passed and cannot be swapped."
	default:
		return err.Error()
	}
}

func (h *Handler) handleSwapCancel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user, ok := auth.GetUserFromContext(ctx)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	memberID := h.resolveMemberID(ctx, user.Email)

	swapID := chi.URLParam(r, "id")

	swap, err := h.db.GetHatSwapByID(ctx, swapID)
	if err != nil || swap == nil {
		http.Error(w, "swap not found", http.StatusNotFound)
		return
	}

	if swap.RequesterMemberID != memberID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if swap.Status != database.SwapStatusPending {
		http.Error(w, "swap is no longer pending", http.StatusBadRequest)
		return
	}

	if err := h.db.UpdateHatSwapStatus(ctx, swapID, database.SwapStatusCancelled); err != nil {
		if errors.Is(err, database.ErrSwapNotPending) {
			http.Error(w, "swap is no longer pending", http.StatusConflict)
			return
		}

		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/swaps", http.StatusSeeOther)
}

func (h *Handler) handleSwapAccept(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user, ok := auth.GetUserFromContext(ctx)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	memberID := h.resolveMemberID(ctx, user.Email)

	swapID := chi.URLParam(r, "id")

	swap, err := h.db.GetHatSwapByID(ctx, swapID)
	if err != nil || swap == nil {
		http.Error(w, "swap not found", http.StatusNotFound)
		return
	}

	if swap.TargetMemberID != memberID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if swap.Status != database.SwapStatusPending {
		http.Error(w, "swap is no longer pending", http.StatusBadRequest)
		return
	}

	if err := h.db.ExecuteSwap(ctx, swapID); err != nil {
		if errors.Is(err, database.ErrSwapDatePassed) || errors.Is(err, database.ErrSwapNotPending) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/swaps", http.StatusSeeOther)
}

func (h *Handler) handleSwapReject(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user, ok := auth.GetUserFromContext(ctx)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	memberID := h.resolveMemberID(ctx, user.Email)

	swapID := chi.URLParam(r, "id")

	swap, err := h.db.GetHatSwapByID(ctx, swapID)
	if err != nil || swap == nil {
		http.Error(w, "swap not found", http.StatusNotFound)
		return
	}

	if swap.TargetMemberID != memberID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if swap.Status != database.SwapStatusPending {
		http.Error(w, "swap is no longer pending", http.StatusBadRequest)
		return
	}

	if err := h.db.UpdateHatSwapStatus(ctx, swapID, database.SwapStatusRejected); err != nil {
		if errors.Is(err, database.ErrSwapNotPending) {
			http.Error(w, "swap is no longer pending", http.StatusConflict)
			return
		}

		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/swaps", http.StatusSeeOther)
}

func (h *Handler) handleSwapAdminDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user, ok := auth.GetUserFromContext(ctx)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if !auth.IsAdminSession(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	swapID := chi.URLParam(r, "id")

	if err := h.db.DeleteHatSwap(ctx, swapID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/swaps", http.StatusSeeOther)
}

// renderSwapsPage loads and renders the swaps page with enriched swap data.
func (h *Handler) renderSwapsPage(w http.ResponseWriter, r *http.Request, data map[string]any, memberID, errMsg string) {
	ctx := r.Context()

	if errMsg != "" {
		data["Error"] = errMsg
	}

	if err := h.loadSwapsData(ctx, data, memberID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.tmpl.ExecuteTemplate(w, "swaps.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// loadSwapsData populates data with swaps and assignment lists for a member.
func (h *Handler) loadSwapsData(ctx context.Context, data map[string]any, memberID string) error {
	swaps, err := h.db.GetSwapsForMember(ctx, memberID)
	if err != nil {
		return err
	}

	swaps, err = h.db.GetEnrichedSwaps(ctx, swaps)
	if err != nil {
		return err
	}

	var incoming, others []database.HatSwap

	for i := range swaps {
		s := &swaps[i]
		if s.TargetMemberID == memberID && s.Status == database.SwapStatusPending {
			incoming = append(incoming, *s)
		} else {
			others = append(others, *s)
		}
	}

	data["Incoming"] = incoming
	data["Others"] = others

	myAssignments, err := h.db.GetFutureAssignmentsForMember(ctx, memberID)
	if err != nil {
		return err
	}

	data["MyAssignments"] = myAssignments

	allAssignments, err := h.db.GetFutureAssignments(ctx)
	if err != nil {
		return err
	}

	var targetAssignments []database.RotaAssignment

	for i := range allAssignments {
		if allAssignments[i].MemberID != memberID {
			targetAssignments = append(targetAssignments, allAssignments[i])
		}
	}

	data["TargetAssignments"] = targetAssignments

	return nil
}
