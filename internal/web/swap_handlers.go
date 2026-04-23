package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/database/sqlc"
)

const (
	swapStatusPending   = "pending"
	swapStatusAccepted  = "accepted"
	swapStatusRejected  = "rejected"
	swapStatusCancelled = "cancelled"
	hoursInDay          = 24
)

func (h *Handler) handleSwaps(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user, ok := auth.GetUserFromContext(ctx)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	data := map[string]any{
		"Template": "swaps",
		"User":     user,
		"IsAdmin":  user.IsAdmin.Valid && user.IsAdmin.Int64 == 1,
	}

	if r.Method == http.MethodPost {
		h.handleSwapRequestPost(w, r, data, user)
		return
	}

	h.renderSwapsPage(w, r, data, user.ID, "")
}

func (h *Handler) handleSwapRequestPost(w http.ResponseWriter, r *http.Request, data map[string]any, user *sqlc.GetSessionByTokenRow) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	requesterAssignmentID := r.FormValue("requester_assignment_id")
	targetAssignmentID := r.FormValue("target_assignment_id")

	if errMsg := h.validateSwapRequest(ctx, requesterAssignmentID, targetAssignmentID, user.ID); errMsg != "" {
		h.renderSwapsPage(w, r, data, user.ID, errMsg)
		return
	}

	reqAssignment, _ := h.db.GetAssignmentByID(ctx, requesterAssignmentID)
	tgtAssignment, _ := h.db.GetAssignmentByID(ctx, targetAssignmentID)

	if _, err := h.db.CreateHatSwap(ctx, requesterAssignmentID, targetAssignmentID, reqAssignment.MemberID, tgtAssignment.MemberID); err != nil {
		h.renderSwapsPage(w, r, data, user.ID, err.Error())
		return
	}

	http.Redirect(w, r, "/swaps", http.StatusSeeOther)
}

// validateSwapRequest validates the swap inputs and returns an error message if invalid.
func (h *Handler) validateSwapRequest(ctx context.Context, requesterAssignmentID, targetAssignmentID, userID string) string {
	if requesterAssignmentID == "" || targetAssignmentID == "" {
		return "Please select both assignments."
	}

	if requesterAssignmentID == targetAssignmentID {
		return "Cannot swap an assignment with itself."
	}

	if msg := h.validateSwapAssignments(ctx, requesterAssignmentID, targetAssignmentID, userID); msg != "" {
		return msg
	}

	for _, assignmentID := range []string{requesterAssignmentID, targetAssignmentID} {
		existing, lookupErr := h.db.GetOpenSwapForAssignment(ctx, assignmentID)
		if lookupErr == nil && existing != nil {
			return "One of the selected assignments already has an open swap request."
		}
	}

	return ""
}

// validateSwapAssignments validates the assignments themselves.
func (h *Handler) validateSwapAssignments(ctx context.Context, requesterAssignmentID, targetAssignmentID, userID string) string {
	reqAssignment, err := h.db.GetAssignmentByID(ctx, requesterAssignmentID)
	if err != nil {
		return "Your assignment was not found."
	}

	tgtAssignment, err := h.db.GetAssignmentByID(ctx, targetAssignmentID)
	if err != nil {
		return "Target assignment was not found."
	}

	if reqAssignment.MemberID != userID {
		return "You can only swap your own assignments."
	}

	today := time.Now().Truncate(hoursInDay * time.Hour)

	reqDate, err := time.Parse("2006-01-02", reqAssignment.Date)
	if err != nil {
		return "Invalid date on your assignment."
	}

	tgtDate, err := time.Parse("2006-01-02", tgtAssignment.Date)
	if err != nil {
		return "Invalid date on target assignment."
	}

	if dateErr := validateSwapDates(reqDate, tgtDate, today); dateErr != nil {
		return dateErr.Error()
	}

	return ""
}

func (h *Handler) handleSwapCancel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user, ok := auth.GetUserFromContext(ctx)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	swapID := chi.URLParam(r, "id")

	swap, err := h.db.GetHatSwapByID(ctx, swapID)
	if err != nil || swap == nil {
		http.Error(w, "swap not found", http.StatusNotFound)
		return
	}

	if swap.RequesterMemberID != user.ID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if swap.Status != swapStatusPending {
		http.Error(w, "swap is no longer pending", http.StatusBadRequest)
		return
	}

	if err := h.db.UpdateHatSwapStatus(ctx, swapID, swapStatusCancelled); err != nil {
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

	swapID := chi.URLParam(r, "id")

	swap, err := h.db.GetHatSwapByID(ctx, swapID)
	if err != nil || swap == nil {
		http.Error(w, "swap not found", http.StatusNotFound)
		return
	}

	if swap.TargetMemberID != user.ID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if swap.Status != swapStatusPending {
		http.Error(w, "swap is no longer pending", http.StatusBadRequest)
		return
	}

	if err := h.db.ExecuteSwap(ctx, swapID); err != nil {
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

	swapID := chi.URLParam(r, "id")

	swap, err := h.db.GetHatSwapByID(ctx, swapID)
	if err != nil || swap == nil {
		http.Error(w, "swap not found", http.StatusNotFound)
		return
	}

	if swap.TargetMemberID != user.ID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if swap.Status != swapStatusPending {
		http.Error(w, "swap is no longer pending", http.StatusBadRequest)
		return
	}

	if err := h.db.UpdateHatSwapStatus(ctx, swapID, swapStatusRejected); err != nil {
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

	if !user.IsAdmin.Valid || user.IsAdmin.Int64 != 1 {
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
		if s.TargetMemberID == memberID && s.Status == swapStatusPending {
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

// validateSwapDates checks that both dates are in the future.
func validateSwapDates(reqDate, tgtDate, today time.Time) error {
	if reqDate.Before(today) {
		return errors.New("your HAT day has already passed and cannot be swapped")
	}

	if tgtDate.Before(today) {
		return errors.New("the target HAT day has already passed and cannot be swapped")
	}

	return nil
}
