package api

import (
	"context"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
)

// apiBirthdateLayout is the wire format for #60's optional
// birthdate field. Mirrors the form-input parser in the web
// layer (parseOptionalBirthdate) so the API and form stay
// in lockstep.
const apiBirthdateLayout = "2006-01-02"

// parseAPIBirthdate mirrors web.parseOptionalBirthdate. Kept as
// a package-local helper rather than shared so the API layer
// doesn't pull in net/http. Returns the same (nil, nil) on
// empty / (nil, err) on malformed / (*time.Time, nil) on
// success contract.
func parseAPIBirthdate(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil //nolint:nilnil // empty input is a valid "no birthdate" signal.
	}
	t, err := time.Parse(apiBirthdateLayout, raw)
	if err != nil {
		return nil, err
	}
	today := time.Now().UTC().Truncate(24 * time.Hour) //nolint:mnd // 24h = 1 day; constant mirrors time.Hour convention.
	if t.After(today) {
		return nil, huma.Error400BadRequest("birthdate cannot be in the future.")
	}
	// pre-1900 dates are almost certainly typos; sanity check.
	if t.Year() < 1900 { //nolint:mnd // year-of-birth sanity floor; not a derived value.
		return nil, huma.Error400BadRequest("birthdate year must be 1900 or later.")
	}
	return &t, nil
}

type AddTeamInput struct {
	Body struct {
		Name      string `json:"name" minLength:"1"`
		Email     string `format:"email" json:"email"`
		Birthdate string `description:"Optional YYYY-MM-DD birthdate (#60)." json:"birthdate,omitempty"`
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
	if !auth.IsAdminSession(userSession) {
		return nil, huma.Error403Forbidden("Admin privileges required")
	}

	bday, err := parseAPIBirthdate(input.Body.Birthdate)
	if err != nil {
		return nil, huma.Error400BadRequest("Invalid birthdate.", err)
	}

	id, err := s.db.AddTeamMember(ctx, input.Body.Name, input.Body.Email, bday)
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
		Name      string `json:"name" minLength:"1"`
		Email     string `format:"email" json:"email"`
		Birthdate string `description:"Optional YYYY-MM-DD birthdate (#60). Pass empty string to clear." json:"birthdate,omitempty"`
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
	if !auth.IsAdminSession(userSession) {
		return nil, huma.Error403Forbidden("Admin privileges required")
	}

	bday, err := parseAPIBirthdate(input.Body.Birthdate)
	if err != nil {
		return nil, huma.Error400BadRequest("Invalid birthdate.", err)
	}

	err = s.db.UpdateTeamMember(ctx, input.ID, input.Body.Name, input.Body.Email, bday)
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
	if !auth.IsAdminSession(userSession) {
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
