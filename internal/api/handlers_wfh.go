package api

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/wfh"
)

// -- Input/Output types -------------------------------------------------------

type CreateWFHInput struct {
	Body struct {
		Date string `doc:"Date in YYYY-MM-DD format" json:"date" maxLength:"10" minLength:"10"`
	}
}

type WFHRequestOutput struct {
	Body database.WFHRequest `json:""`
}

type ListWFHOutput struct {
	Body struct {
		Requests []database.WFHRequest `json:"requests"`
	}
}

type WFHIDInput struct {
	ID string `minLength:"1" path:"id"`
}

type WFHMessageOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

type WFHQuotaOutput struct {
	Body wfh.QuotaStatus `json:""`
}

type WFHSettleOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

// -- Helpers ------------------------------------------------------------------

// wfhDomainToHumaError maps WFH domain errors to appropriate HUMA HTTP errors.
func wfhDomainToHumaError(err error) error {
	switch {
	case errors.Is(err, database.ErrWFHNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, database.ErrWFHNotOwner):
		return huma.Error403Forbidden(err.Error())
	case errors.Is(err, database.ErrWFHAlreadySettled):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, database.ErrWFHDuplicateRequest):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, database.ErrWFHDatePassed):
		return huma.Error422UnprocessableEntity(err.Error(), nil)
	case errors.Is(err, database.ErrWFHMemberNotFound):
		return huma.Error422UnprocessableEntity(err.Error(), nil)
	case errors.Is(err, database.ErrWFHNotApproved):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, database.ErrWFHWithdrawalDeadlinePassed):
		return huma.Error409Conflict(err.Error())
	default:
		return huma.Error500InternalServerError("An unexpected error occurred", err)
	}
}

// resolveWFHMemberID resolves the team member ID for the logged-in user; returns
// the empty string when the user is not a team member.
func (s *Server) resolveWFHMemberID(ctx context.Context) (string, error) {
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

// handleRequestWFH creates a new pending WFH request for the authenticated user.
func (s *Server) handleRequestWFH(ctx context.Context, input *CreateWFHInput) (*WFHRequestOutput, error) {
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	memberID, err := s.resolveWFHMemberID(ctx)
	if err != nil {
		return nil, err
	}

	// Enforce quota before creating the request.
	if s.wfhService != nil {
		hasQuota, quotaErr := s.wfhService.CheckQuota(ctx, memberID, input.Body.Date)
		if quotaErr != nil {
			return nil, huma.Error500InternalServerError("Failed to check WFH quota", quotaErr)
		}
		if !hasQuota {
			return nil, huma.Error422UnprocessableEntity("WFH quota for this period has been reached", nil)
		}
	}

	req, err := s.db.CreateWFHRequest(ctx, memberID, input.Body.Date)
	if err != nil {
		return nil, wfhDomainToHumaError(err)
	}

	return &WFHRequestOutput{Body: *req}, nil
}

// handleListWFH lists WFH requests. Regular users see their own; admins see all.
func (s *Server) handleListWFH(ctx context.Context, _ *struct{}) (*ListWFHOutput, error) {
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	user, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}

	var requests []database.WFHRequest
	var err error

	if auth.IsAdminSession(user) {
		requests, err = s.db.GetAllWFHRequests(ctx)
	} else {
		member, mErr := s.db.GetMemberByEmail(ctx, user.Email)
		if mErr != nil || member == nil {
			return nil, huma.Error403Forbidden("You are not registered as a team member")
		}
		requests, err = s.db.GetWFHRequestsByMember(ctx, member.ID)
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to retrieve WFH requests", err)
	}

	// Enrich with member names.
	members, _ := s.db.GetActiveTeamMembers(ctx)
	memberMap := mapMembersByID(members)
	for i := range requests {
		if m, ok := memberMap[requests[i].MemberID]; ok {
			requests[i].MemberName = m.Name
		}
	}

	resp := &ListWFHOutput{}
	resp.Body.Requests = requests
	return resp, nil
}

// handleCancelWFH lets the authenticated user cancel their own pending WFH request.
func (s *Server) handleCancelWFH(ctx context.Context, input *WFHIDInput) (*WFHMessageOutput, error) {
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	memberID, err := s.resolveWFHMemberID(ctx)
	if err != nil {
		return nil, err
	}

	if err := s.db.CancelWFHRequest(ctx, input.ID, memberID); err != nil {
		return nil, wfhDomainToHumaError(err)
	}

	resp := &WFHMessageOutput{}
	resp.Body.Message = "WFH request cancelled"
	return resp, nil
}

// handleWithdrawWFH lets an admin withdraw an approved WFH request before the deadline.
func (s *Server) handleWithdrawWFH(ctx context.Context, input *WFHIDInput) (*WFHMessageOutput, error) {
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	user, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}
	if !auth.IsAdminSession(user) {
		return nil, huma.Error403Forbidden("Admin access required")
	}

	withdrawalHours := 24
	if s.wfhService != nil {
		withdrawalHours = s.wfhService.Config().WithdrawalHours
	}

	if err := s.db.WithdrawWFHRequest(ctx, input.ID, user.UserID, withdrawalHours); err != nil {
		return nil, wfhDomainToHumaError(err)
	}

	resp := &WFHMessageOutput{}
	resp.Body.Message = "WFH request withdrawn"
	return resp, nil
}

// handleSettleWFH triggers a manual settlement run (admin only).
func (s *Server) handleSettleWFH(ctx context.Context, _ *struct{}) (*WFHSettleOutput, error) {
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	user, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}
	if !auth.IsAdminSession(user) {
		return nil, huma.Error403Forbidden("Admin access required")
	}

	if s.wfhService == nil {
		return nil, huma.Error503ServiceUnavailable("WFH service is not enabled")
	}

	if err := s.wfhService.SettlePendingRequests(ctx); err != nil {
		return nil, huma.Error500InternalServerError("Settlement failed", err)
	}

	resp := &WFHSettleOutput{}
	resp.Body.Message = "WFH settlement completed"
	return resp, nil
}

// handleWFHQuota returns the current user's WFH quota status for the active period.
func (s *Server) handleWFHQuota(ctx context.Context, _ *struct{}) (*WFHQuotaOutput, error) {
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	memberID, err := s.resolveWFHMemberID(ctx)
	if err != nil {
		return nil, err
	}

	if s.wfhService == nil {
		return nil, huma.Error503ServiceUnavailable("WFH service is not enabled")
	}

	quota, err := s.wfhService.GetQuotaStatus(ctx, memberID)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to get quota status", err)
	}

	return &WFHQuotaOutput{Body: quota}, nil
}

// handleGetWFHByDate returns WFH requests for a specific date (admin only).
func (s *Server) handleGetWFHByDate(ctx context.Context, input *struct {
	Date string `maxLength:"10" minLength:"10" path:"date"`
},
) (*ListWFHOutput, error) {
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	user, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}
	if !auth.IsAdminSession(user) {
		return nil, huma.Error403Forbidden("Admin access required")
	}

	requests, err := s.db.GetWFHRequestsByDate(ctx, input.Date)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to retrieve WFH requests", err)
	}

	members, _ := s.db.GetActiveTeamMembers(ctx)
	memberMap := mapMembersByID(members)
	for i := range requests {
		if m, ok2 := memberMap[requests[i].MemberID]; ok2 {
			requests[i].MemberName = m.Name
		}
	}

	resp := &ListWFHOutput{}
	resp.Body.Requests = requests
	return resp, nil
}
