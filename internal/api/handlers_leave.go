package api

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
)

type ReportLeaveInput struct {
	Body struct {
		MemberID  string `json:"member_id"`
		StartDate string `format:"date" json:"start_date"`
		EndDate   string `format:"date" json:"end_date"`
	}
}

type ReportLeaveOutput struct {
	Body struct {
		LeaveID string `json:"leave_id"`
		Covers  []struct {
			Date   string `json:"date"`
			Member string `json:"member"`
		} `json:"covers"`
		Message string `json:"message"`
	}
}

func (s *Server) handleReportLeave(ctx context.Context, input *ReportLeaveInput) (*ReportLeaveOutput, error) {
	// Check authentication
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	// Get user from context using middleware's context key
	_, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}

	// Create leave record
	leaveID, err := s.db.CreateLeaveRecord(ctx, input.Body.MemberID, input.Body.StartDate, input.Body.EndDate)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to create leave record", err)
	}

	// Assign covers
	err = s.engine.AssignCoversForLeave(ctx, leaveID)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to assign covers", err)
	}

	// Get cover assignments for response
	covers := s.getCoversForLeave(ctx, leaveID, input.Body.StartDate, input.Body.EndDate)

	resp := &ReportLeaveOutput{}
	resp.Body.LeaveID = leaveID
	resp.Body.Covers = covers
	resp.Body.Message = "Leave reported and covers assigned"
	return resp, nil
}

func (s *Server) getCoversForLeave(ctx context.Context, leaveID string, startDateStr, endDateStr string) []struct {
	Date   string `json:"date"`
	Member string `json:"member"`
} {
	covers := []struct {
		Date   string `json:"date"`
		Member string `json:"member"`
	}{}

	startDate, _ := time.Parse("2006-01-02", startDateStr)
	endDate, _ := time.Parse("2006-01-02", endDateStr)

	for d := startDate; d.Before(endDate.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		dateStr := d.Format("2006-01-02")
		assignments, err := s.db.GetAssignmentsByDate(ctx, dateStr)
		if err == nil {
			for _, a := range assignments {
				if a.OriginalAssignmentID != nil && *a.OriginalAssignmentID == leaveID {
					covers = append(covers, struct {
						Date   string `json:"date"`
						Member string `json:"member"`
					}{
						Date:   dateStr,
						Member: a.MemberName,
					})
				}
			}
		}
	}

	return covers
}

type ListLeaveOutput struct {
	Body struct {
		LeaveRecords []database.LeaveRecord `json:"leave_records"`
	}
}

func (s *Server) handleListLeave(ctx context.Context, input *struct{}) (*ListLeaveOutput, error) {
	_ = input

	// Check authentication
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	// Get user from context using middleware's context key
	_, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}

	// Note: Returns all leave records for all team members.
	// This is intentional as the system is designed for a single team where
	// all authenticated users need visibility into leave schedules.
	// If per-user or per-team filtering is needed in the future, the query
	// would need to filter by member_id or team membership.
	leaveRecords, err := s.db.GetLeaveRecords(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to get leave records", err)
	}

	resp := &ListLeaveOutput{}
	resp.Body.LeaveRecords = leaveRecords
	return resp, nil
}

type UpdateLeaveInput struct {
	ID   string `minLength:"1" path:"id"`
	Body struct {
		MemberID  string `json:"member_id"`
		StartDate string `format:"date" json:"start_date"`
		EndDate   string `format:"date" json:"end_date"`
	}
}

type UpdateLeaveOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

func (s *Server) handleUpdateLeave(ctx context.Context, input *UpdateLeaveInput) (*UpdateLeaveOutput, error) {
	// Check authentication
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	// Get user from context using middleware's context key
	userSession, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}

	// Check admin privileges
	if !auth.IsAdminSession(userSession) {
		return nil, huma.Error403Forbidden("Admin privileges required")
	}

	// Preserve the existing status — it is managed by the scheduling engine.
	existing, err := s.db.GetLeaveByID(ctx, input.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to fetch leave record", err)
	}

	if err := s.db.UpdateLeaveRecord(ctx, input.ID, input.Body.MemberID, input.Body.StartDate, input.Body.EndDate, existing.Status); err != nil {
		return nil, huma.Error500InternalServerError("Failed to update leave record", err)
	}

	// Update schedule after modification
	maintenance := s.newScheduleMaintenance()
	if err := maintenance.HandleLeaveChange(ctx, input.ID); err != nil {
		return nil, huma.Error500InternalServerError("Failed to update schedule", err)
	}

	resp := &UpdateLeaveOutput{}
	resp.Body.Message = "Leave record updated successfully"
	return resp, nil
}

type DeleteLeaveInput struct {
	ID string `minLength:"1" path:"id"`
}

type DeleteLeaveOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

func (s *Server) handleDeleteLeave(ctx context.Context, input *DeleteLeaveInput) (*DeleteLeaveOutput, error) {
	// Check authentication
	if s.authMiddleware == nil {
		return nil, huma.Error503ServiceUnavailable("Authentication not available")
	}

	// Get user from context using middleware's context key
	userSession, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("Authentication required")
	}

	// Check admin privileges
	if !auth.IsAdminSession(userSession) {
		return nil, huma.Error403Forbidden("Admin privileges required")
	}

	err := s.db.DeleteLeaveRecord(ctx, input.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to delete leave record", err)
	}

	// Reconcile schedule - will remove any stale covers automatically
	maintenance := s.newScheduleMaintenance()
	if err := maintenance.HandleTeamChange(ctx); err != nil {
		return nil, huma.Error500InternalServerError("Failed to update schedule", err)
	}

	resp := &DeleteLeaveOutput{}
	resp.Body.Message = "Leave record deleted successfully"
	return resp, nil
}
