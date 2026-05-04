package api

import (
	"context"
	"errors"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
)

const (
	apiSwapStatusPending = "pending"
)

// -- Input/Output types -------------------------------------------------------

type CreateSwapInput struct {
	Body struct {
		RequesterAssignmentID string `json:"requester_assignment_id" minLength:"1"`
		TargetAssignmentID    string `json:"target_assignment_id" minLength:"1"`
	}
}

type SwapOutput struct {
	Body struct {
		ID                    string    `json:"id"`
		RequesterAssignmentID string    `json:"requester_assignment_id"`
		TargetAssignmentID    string    `json:"target_assignment_id"`
		RequesterMemberID     string    `json:"requester_member_id"`
		TargetMemberID        string    `json:"target_member_id"`
		Status                string    `json:"status"`
		CreatedAt             time.Time `json:"created_at"`
		UpdatedAt             time.Time `json:"updated_at"`
	}
}

type ListSwapsOutput struct {
	Body struct {
		Swaps []database.HatSwap `json:"swaps"`
	}
}

type SwapIDInput struct {
	ID string `minLength:"1" path:"id"`
}

type SwapMessageOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

// -- Helpers ------------------------------------------------------------------

// resolveAPIMemberID resolves the team member ID for the logged-in user.
func (s *Server) resolveAPIMemberID(ctx context.Context) (string, error) {
	user, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return "", huma.Error401Unauthorized("Authentication required")
	}

	member, err := s.db.GetMemberByEmail(ctx, user.Email)
	if err != nil || member == nil {
		return "", huma.Error403Forbidden("You are not registered as a team member")
	}

	return member.ID, nil
}

// -- Handlers -----------------------------------------------------------------

// validateSwapAssignmentsAPI validates that the assignments exist, the requester
// owns their assignment, and both dates are in the future.
func (s *Server) validateSwapAssignmentsAPI(
	ctx context.Context,
	reqAssignmentID, tgtAssignmentID, memberID string,
) (*database.RotaAssignment, *database.RotaAssignment, error) {
	if reqAssignmentID == tgtAssignmentID {
		return nil, nil, huma.Error422UnprocessableEntity("Cannot swap an assignment with itself", nil)
	}

	reqAssignment, err := s.db.GetAssignmentByID(ctx, reqAssignmentID)
	if err != nil {
		return nil, nil, huma.Error422UnprocessableEntity("Requester assignment not found", nil)
	}

	tgtAssignment, err := s.db.GetAssignmentByID(ctx, tgtAssignmentID)
	if err != nil {
		return nil, nil, huma.Error422UnprocessableEntity("Target assignment not found", nil)
	}

	if reqAssignment.MemberID != memberID {
		return nil, nil, huma.Error403Forbidden("You can only swap your own assignments")
	}

	if tgtAssignment.MemberID == reqAssignment.MemberID {
		return nil, nil, huma.Error422UnprocessableEntity("Swap target must belong to another member", nil)
	}

	nowUTC := time.Now().UTC()
	today := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)

	reqDate, parseErr := time.Parse("2006-01-02", reqAssignment.Date)
	if parseErr != nil || reqDate.Before(today) {
		return nil, nil, huma.Error422UnprocessableEntity("Your HAT day has already passed and cannot be swapped", nil)
	}

	tgtDate, parseErr := time.Parse("2006-01-02", tgtAssignment.Date)
	if parseErr != nil || tgtDate.Before(today) {
		return nil, nil, huma.Error422UnprocessableEntity("The target HAT day has already passed and cannot be swapped", nil)
	}

	return reqAssignment, tgtAssignment, nil
}

// checkNoOpenSwaps returns an error if either assignment already has a pending swap.
func (s *Server) checkNoOpenSwaps(ctx context.Context, reqAssignmentID, tgtAssignmentID string) error {
	for _, aid := range []string{reqAssignmentID, tgtAssignmentID} {
		existing, lookupErr := s.db.GetOpenSwapForAssignment(ctx, aid)
		if lookupErr == nil && existing != nil {
			return huma.Error409Conflict("One of the assignments already has an open swap request")
		}
	}

	return nil
}

func (s *Server) handleCreateSwap(ctx context.Context, input *CreateSwapInput) (*SwapOutput, error) {
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	memberID, err := s.resolveAPIMemberID(ctx)
	if err != nil {
		return nil, err
	}

	reqAssignment, tgtAssignment, err := s.validateSwapAssignmentsAPI(
		ctx,
		input.Body.RequesterAssignmentID,
		input.Body.TargetAssignmentID,
		memberID,
	)
	if err != nil {
		return nil, err
	}

	if err = s.checkNoOpenSwaps(ctx, input.Body.RequesterAssignmentID, input.Body.TargetAssignmentID); err != nil {
		return nil, err
	}

	swapID, err := s.db.CreateHatSwap(
		ctx,
		input.Body.RequesterAssignmentID,
		input.Body.TargetAssignmentID,
		reqAssignment.MemberID,
		tgtAssignment.MemberID,
	)
	if err != nil {
		if errors.Is(err, database.ErrSwapSameMember) {
			return nil, huma.Error422UnprocessableEntity(err.Error(), nil)
		}
		if errors.Is(err, database.ErrSwapAssignmentBusy) {
			return nil, huma.Error409Conflict(err.Error())
		}

		return nil, huma.Error500InternalServerError("Failed to create swap request", err)
	}

	swap, err := s.db.GetHatSwapByID(ctx, swapID)
	if err != nil || swap == nil {
		return nil, huma.Error500InternalServerError("Failed to retrieve created swap", err)
	}

	resp := &SwapOutput{}
	resp.Body.ID = swap.ID
	resp.Body.RequesterAssignmentID = swap.RequesterAssignmentID
	resp.Body.TargetAssignmentID = swap.TargetAssignmentID
	resp.Body.RequesterMemberID = swap.RequesterMemberID
	resp.Body.TargetMemberID = swap.TargetMemberID
	resp.Body.Status = swap.Status
	resp.Body.CreatedAt = swap.CreatedAt
	resp.Body.UpdatedAt = swap.UpdatedAt

	return resp, nil
}

func (s *Server) handleListSwaps(ctx context.Context, _ *struct{}) (*ListSwapsOutput, error) {
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	memberID, err := s.resolveAPIMemberID(ctx)
	if err != nil {
		return nil, err
	}

	swaps, err := s.db.GetSwapsForMember(ctx, memberID)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to list swaps", err)
	}

	swaps, err = s.db.GetEnrichedSwaps(ctx, swaps)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to enrich swaps", err)
	}

	resp := &ListSwapsOutput{}
	resp.Body.Swaps = swaps

	return resp, nil
}

func (s *Server) handleAcceptSwap(ctx context.Context, input *SwapIDInput) (*SwapMessageOutput, error) {
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	memberID, err := s.resolveAPIMemberID(ctx)
	if err != nil {
		return nil, err
	}

	swap, err := s.db.GetHatSwapByID(ctx, input.ID)
	if err != nil || swap == nil {
		return nil, huma.Error404NotFound("Swap not found")
	}

	if swap.TargetMemberID != memberID {
		return nil, huma.Error403Forbidden("Only the target member can accept this swap")
	}

	if swap.Status != apiSwapStatusPending {
		return nil, huma.Error409Conflict("Swap is no longer pending")
	}

	if err := s.db.ExecuteSwap(ctx, input.ID); err != nil {
		if errors.Is(err, database.ErrSwapDatePassed) || errors.Is(err, database.ErrSwapNotPending) {
			return nil, huma.Error409Conflict(err.Error())
		}

		return nil, huma.Error500InternalServerError("Failed to execute swap", err)
	}

	resp := &SwapMessageOutput{}
	resp.Body.Message = "Swap accepted successfully"

	return resp, nil
}

func (s *Server) handleRejectSwap(ctx context.Context, input *SwapIDInput) (*SwapMessageOutput, error) {
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	memberID, err := s.resolveAPIMemberID(ctx)
	if err != nil {
		return nil, err
	}

	swap, err := s.db.GetHatSwapByID(ctx, input.ID)
	if err != nil || swap == nil {
		return nil, huma.Error404NotFound("Swap not found")
	}

	if swap.TargetMemberID != memberID {
		return nil, huma.Error403Forbidden("Only the target member can reject this swap")
	}

	if swap.Status != apiSwapStatusPending {
		return nil, huma.Error409Conflict("Swap is no longer pending")
	}

	if err := s.db.UpdateHatSwapStatus(ctx, input.ID, "rejected"); err != nil {
		if errors.Is(err, database.ErrSwapNotPending) {
			return nil, huma.Error409Conflict("Swap is no longer pending")
		}

		return nil, huma.Error500InternalServerError("Failed to reject swap", err)
	}

	resp := &SwapMessageOutput{}
	resp.Body.Message = "Swap rejected"

	return resp, nil
}

func (s *Server) handleCancelSwap(ctx context.Context, input *SwapIDInput) (*SwapMessageOutput, error) {
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	memberID, err := s.resolveAPIMemberID(ctx)
	if err != nil {
		return nil, err
	}

	swap, err := s.db.GetHatSwapByID(ctx, input.ID)
	if err != nil || swap == nil {
		return nil, huma.Error404NotFound("Swap not found")
	}

	if swap.RequesterMemberID != memberID {
		return nil, huma.Error403Forbidden("Only the requester can cancel this swap")
	}

	if swap.Status != apiSwapStatusPending {
		return nil, huma.Error409Conflict("Swap is no longer pending")
	}

	if err := s.db.UpdateHatSwapStatus(ctx, input.ID, "cancelled"); err != nil {
		if errors.Is(err, database.ErrSwapNotPending) {
			return nil, huma.Error409Conflict("Swap is no longer pending")
		}

		return nil, huma.Error500InternalServerError("Failed to cancel swap", err)
	}

	resp := &SwapMessageOutput{}
	resp.Body.Message = "Swap cancelled"

	return resp, nil
}

func (s *Server) handleDeleteSwap(ctx context.Context, input *SwapIDInput) (*SwapMessageOutput, error) {
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	userSession, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}

	if !auth.IsAdminSession(userSession) {
		return nil, huma.Error403Forbidden("Admin privileges required")
	}

	if err := s.db.DeleteHatSwap(ctx, input.ID); err != nil {
		return nil, huma.Error500InternalServerError("Failed to delete swap", err)
	}

	resp := &SwapMessageOutput{}
	resp.Body.Message = "Swap deleted"

	return resp, nil
}
