package api

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/inful/madhatter/internal/auth"
)

type GenerateScheduleInput struct {
	Body struct {
		StartDate string `format:"date" json:"start_date"`
		EndDate   string `format:"date" json:"end_date"`
	}
}

type GenerateScheduleOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

func (s *Server) handleGenerateSchedule(ctx context.Context, input *GenerateScheduleInput) (*GenerateScheduleOutput, error) {
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

	startDate, err := time.Parse("2006-01-02", input.Body.StartDate)
	if err != nil {
		return nil, huma.Error400BadRequest("Invalid start date format")
	}

	endDate, err := time.Parse("2006-01-02", input.Body.EndDate)
	if err != nil {
		return nil, huma.Error400BadRequest("Invalid end date format")
	}

	err = s.engine.GenerateSchedule(ctx, startDate, endDate)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to generate schedule", err)
	}

	resp := &GenerateScheduleOutput{}
	resp.Body.Message = "Schedule generated successfully"
	return resp, nil
}
