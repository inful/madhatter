package api

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
)

type AddTeamInput struct {
	Body struct {
		Name  string `json:"name" minLength:"1"`
		Email string `format:"email" json:"email"`
	}
}

type AddTeamOutput struct {
	Body struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
}

func (s *Server) handleAddTeam(ctx context.Context, input *AddTeamInput) (*AddTeamOutput, error) {
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
	if !userSession.IsAdmin.Valid || userSession.IsAdmin.Int64 == 0 {
		return nil, huma.Error403Forbidden("Admin privileges required")
	}

	id, err := s.db.AddTeamMember(ctx, input.Body.Name, input.Body.Email)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to add team member", err)
	}

	resp := &AddTeamOutput{}
	resp.Body.ID = id
	resp.Body.Message = "Team member added successfully"
	return resp, nil
}

type ListTeamOutput struct {
	Body struct {
		Members []database.TeamMember `json:"members"`
	}
}

func (s *Server) handleListTeam(ctx context.Context, input *struct{}) (*ListTeamOutput, error) {
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

	members, err := s.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to get team members", err)
	}

	resp := &ListTeamOutput{}
	resp.Body.Members = members
	return resp, nil
}

type UpdateTeamInput struct {
	ID   string `minLength:"1" path:"id"`
	Body struct {
		Name  string `json:"name" minLength:"1"`
		Email string `format:"email" json:"email"`
	}
}

type UpdateTeamOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

func (s *Server) handleUpdateTeam(ctx context.Context, input *UpdateTeamInput) (*UpdateTeamOutput, error) {
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
	if !userSession.IsAdmin.Valid || userSession.IsAdmin.Int64 == 0 {
		return nil, huma.Error403Forbidden("Admin privileges required")
	}

	err := s.db.UpdateTeamMember(ctx, input.ID, input.Body.Name, input.Body.Email)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to update team member", err)
	}

	resp := &UpdateTeamOutput{}
	resp.Body.Message = "Team member updated successfully"
	return resp, nil
}

type DeleteTeamInput struct {
	ID string `minLength:"1" path:"id"`
}

type DeleteTeamOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

func (s *Server) handleDeleteTeam(ctx context.Context, input *DeleteTeamInput) (*DeleteTeamOutput, error) {
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
	if !userSession.IsAdmin.Valid || userSession.IsAdmin.Int64 == 0 {
		return nil, huma.Error403Forbidden("Admin privileges required")
	}

	err := s.db.DeleteTeamMember(ctx, input.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to delete team member", err)
	}

	// Update schedule after deletion
	maintenance := s.newScheduleMaintenance()
	if err := maintenance.HandleTeamChange(ctx); err != nil {
		return nil, huma.Error500InternalServerError("Failed to update schedule", err)
	}

	resp := &DeleteTeamOutput{}
	resp.Body.Message = "Team member deleted successfully"
	return resp, nil
}
