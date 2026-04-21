package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/database/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestServer(t *testing.T) (*Server, func()) {
	t.Helper()

	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := database.New(dbPath)
	require.NoError(t, err, "Failed to create test database")

	cleanup := func() {
		_ = db.Close()
	}

	// Create server with development = true to bypass authentication
	// This allows tests to focus on business logic without auth complexity
	server, err := NewServer(db, true)
	require.NoError(t, err, "Failed to create server")

	return server, cleanup
}

// createTestSession creates a test session for integration testing.
// This bypasses authentication for testing purposes.
func (s *Server) createTestSession(ctx context.Context) (string, error) {
	if s.sessionManager == nil {
		return "", errors.New("session manager not available")
	}

	// Create or get test user
	user, err := s.db.GetQueries().GetUserByEmail(ctx, "test@example.com")
	if err != nil {
		// User doesn't exist, create one
		user, err = s.db.GetQueries().CreateUser(ctx, sqlc.CreateUserParams{
			Email:      "test@example.com",
			Name:       "Test User",
			Provider:   "fake",
			ProviderID: "test-user-id",
			IsAdmin:    sql.NullInt64{Int64: 1, Valid: true},
		})
		if err != nil {
			return "", err
		}
	}

	// Create session
	return s.sessionManager.CreateSession(ctx, user.ID)
}

// createTestContext creates a context with a test session for authenticated requests.
func createTestContext(t *testing.T, server *Server) context.Context {
	t.Helper()

	ctx := context.Background()
	sessionToken, err := server.createTestSession(ctx)
	require.NoError(t, err, "Failed to create test session")

	// Get the session data to add to context
	session, err := server.sessionManager.ValidateSession(ctx, sessionToken)
	require.NoError(t, err, "Failed to validate test session")

	// Add user session to context (this is what the handlers expect)
	return context.WithValue(ctx, auth.UserContextKey, session)
}

func TestTeamEndpoints(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := createTestContext(t, server)

	// Test adding team member
	t.Run("AddTeamMember", func(t *testing.T) {
		input := &AddTeamInput{}
		input.Body.Name = "Alice Johnson"
		input.Body.Email = "alice@example.com"

		resp, err := server.handleAddTeam(ctx, input)
		require.NoError(t, err)
		assert.NotEmpty(t, resp.Body.ID)
		assert.Equal(t, "Team member added successfully", resp.Body.Message)
	})

	// Test listing team members
	t.Run("ListTeamMembers", func(t *testing.T) {
		resp, err := server.handleListTeam(ctx, &struct{}{})
		require.NoError(t, err)
		assert.Len(t, resp.Body.Members, 1)
		assert.Equal(t, "Alice Johnson", resp.Body.Members[0].Name)
	})

	// Test updating team member
	t.Run("UpdateTeamMember", func(t *testing.T) {
		// Add a member first
		addInput := &AddTeamInput{}
		addInput.Body.Name = "Bob Smith"
		addInput.Body.Email = "bob@example.com"
		addResp, err := server.handleAddTeam(ctx, addInput)
		require.NoError(t, err)

		// Update the member
		updateInput := &UpdateTeamInput{
			ID: addResp.Body.ID,
		}
		updateInput.Body.Name = "Bob Johnson"
		updateInput.Body.Email = "bob.johnson@example.com"

		updateResp, err := server.handleUpdateTeam(ctx, updateInput)
		require.NoError(t, err)
		assert.Equal(t, "Team member updated successfully", updateResp.Body.Message)
	})

	// Test deleting team member
	t.Run("DeleteTeamMember", func(t *testing.T) {
		// Add a member first
		addInput := &AddTeamInput{}
		addInput.Body.Name = "Charlie Brown"
		addInput.Body.Email = "charlie@example.com"
		addResp, err := server.handleAddTeam(ctx, addInput)
		require.NoError(t, err)

		// Delete the member
		deleteInput := &DeleteTeamInput{
			ID: addResp.Body.ID,
		}

		deleteResp, err := server.handleDeleteTeam(ctx, deleteInput)
		require.NoError(t, err)
		assert.Equal(t, "Team member deleted successfully", deleteResp.Body.Message)
	})
}

func TestPresenceTodayEndpoint(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := createTestContext(t, server)

	// Create a few team members.
	aliceID, err := server.db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := server.db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = server.db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	today := time.Now().Format("2006-01-02")

	// Bob is away today.
	_, err = server.db.CreateLeaveRecord(ctx, bobID, today, today)
	require.NoError(t, err)

	// Alice is on support today.
	_, err = server.db.CreateRotaAssignment(ctx, today, aliceID, false, nil)
	require.NoError(t, err)

	resp, err := server.handleGetPresenceToday(ctx, &struct{}{})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, today, resp.Body.Date)
	require.NotNil(t, resp.Body.Support)
	assert.Equal(t, aliceID, resp.Body.Support.ID)
	assert.False(t, resp.Body.SupportIsCover)

	// Bob must be in away list.
	awayIDs := make(map[string]struct{}, len(resp.Body.Away))
	for i := range resp.Body.Away {
		awayIDs[resp.Body.Away[i].ID] = struct{}{}
	}
	_, ok := awayIDs[bobID]
	assert.True(t, ok)

	// Alice and Charlie must be present.
	presentIDs := make(map[string]struct{}, len(resp.Body.Present))
	for i := range resp.Body.Present {
		presentIDs[resp.Body.Present[i].ID] = struct{}{}
	}
	_, ok = presentIDs[aliceID]
	assert.True(t, ok)
	_, ok = presentIDs[bobID]
	assert.False(t, ok)
}

func TestScheduleEndpoints(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := createTestContext(t, server)

	// Setup: Add team members
	_, err := server.db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = server.db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = server.db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	t.Run("GenerateSchedule", func(t *testing.T) {
		input := &GenerateScheduleInput{}
		input.Body.StartDate = "2024-01-01"
		input.Body.EndDate = "2024-01-31"

		resp, err := server.handleGenerateSchedule(ctx, input)
		require.NoError(t, err)
		assert.Equal(t, "Schedule generated successfully", resp.Body.Message)

		// Verify assignments were created
		assignments, err := server.db.GetAssignmentsByDate(ctx, "2024-01-01")
		require.NoError(t, err)
		assert.Len(t, assignments, 1)
	})
}

func TestLeaveEndpoints(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := createTestContext(t, server)

	// Setup: Add team members and generate schedule
	aliceID, _ := server.db.AddTeamMember(ctx, "Alice", "alice@example.com")
	_, _ = server.db.AddTeamMember(ctx, "Bob", "bob@example.com")
	_, _ = server.db.AddTeamMember(ctx, "Charlie", "charlie@example.com")

	engine := server.engine
	err := engine.GenerateSchedule(
		ctx,
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)

	t.Run("ReportLeaveWithAutoCover", func(t *testing.T) {
		input := &ReportLeaveInput{}
		input.Body.MemberID = aliceID
		input.Body.StartDate = "2024-01-17"
		input.Body.EndDate = "2024-01-17"

		resp, err := server.handleReportLeave(ctx, input)
		require.NoError(t, err)
		assert.NotEmpty(t, resp.Body.LeaveID)

		// Verify covers were assigned
		// Jan 17 is a day Alice is scheduled, so it should be covered
		assignments, err := server.db.GetAssignmentsByDate(ctx, "2024-01-17")
		require.NoError(t, err)

		// Find the cover assignment
		var coverFound bool
		for _, a := range assignments {
			if a.IsCover {
				coverFound = true
				assert.NotNil(t, a.OriginalAssignmentID)
				break
			}
		}
		assert.True(t, coverFound, "Should have at least one cover assignment")
	})

	t.Run("UpdateLeaveRecord", func(t *testing.T) {
		// Report leave first
		input := &ReportLeaveInput{}
		input.Body.MemberID = aliceID
		input.Body.StartDate = "2024-01-20"
		input.Body.EndDate = "2024-01-22"
		resp, err := server.handleReportLeave(ctx, input)
		require.NoError(t, err)

		// Update the leave
		updateInput := &UpdateLeaveInput{ID: resp.Body.LeaveID}
		updateInput.Body.MemberID = aliceID
		updateInput.Body.StartDate = "2024-01-25"
		updateInput.Body.EndDate = "2024-01-27"

		updateResp, err := server.handleUpdateLeave(ctx, updateInput)
		require.NoError(t, err)
		assert.Equal(t, "Leave record updated successfully", updateResp.Body.Message)
	})

	t.Run("DeleteLeaveRecord", func(t *testing.T) {
		// Report leave first
		input := &ReportLeaveInput{}
		input.Body.MemberID = aliceID
		input.Body.StartDate = "2024-01-28"
		input.Body.EndDate = "2024-01-29"
		resp, err := server.handleReportLeave(ctx, input)
		require.NoError(t, err)

		// Delete the leave
		deleteInput := &DeleteLeaveInput{ID: resp.Body.LeaveID}
		deleteResp, err := server.handleDeleteLeave(ctx, deleteInput)
		require.NoError(t, err)
		assert.Equal(t, "Leave record deleted successfully", deleteResp.Body.Message)
	})
}

func TestCalendarEndpoints(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := createTestContext(t, server)

	// Setup: Add team member and generate schedule
	aliceID, _ := server.db.AddTeamMember(ctx, "Alice", "alice@example.com")
	_, _ = server.db.AddTeamMember(ctx, "Bob", "bob@example.com")

	engine := server.engine
	err := engine.GenerateSchedule(
		ctx,
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)

	t.Run("SubscribeCalendar", func(t *testing.T) {
		input := &SubscribeCalendarInput{}
		input.Body.MemberID = aliceID

		resp, err := server.handleSubscribeCalendar(ctx, input)
		require.NoError(t, err)
		assert.NotEmpty(t, resp.Body.Token)
		assert.Contains(t, resp.Body.CalendarURL, "/api/v1/calendar/")
		assert.Contains(t, resp.Body.CalendarURL, "/ics")
	})

	t.Run("GetICSFeed", func(t *testing.T) {
		// First generate a schedule for the current month
		now := time.Now().UTC()
		startDate := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		endDate := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC)
		err := server.engine.GenerateSchedule(ctx, startDate, endDate)
		require.NoError(t, err)

		// Then create subscription
		token, err := server.db.CreateCalendarSubscription(ctx, aliceID)
		require.NoError(t, err)

		// Use the full router to properly handle URL parameters
		router := chi.NewRouter()
		router.Get("/api/v1/calendar/{token}/ics", server.handleCalendarICS)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/calendar/"+token+"/ics", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "text/calendar", w.Header().Get("Content-Type"))

		// Verify ICS content
		body := w.Body.String()
		assert.Contains(t, body, "BEGIN:VCALENDAR")
		assert.Contains(t, body, "BEGIN:VEVENT")
		assert.Contains(t, body, "END:VCALENDAR")
	})
}

// TestHUMAAPIIntegration tests the full HUMA API integration using humatest.
func TestHUMAAPIIntegration(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	api := humatest.Wrap(t, server.api)

	ctx := context.Background()
	sessionToken, err := server.createTestSession(ctx)
	require.NoError(t, err)

	t.Run("TeamAPI", func(t *testing.T) {
		// Test POST /api/v1/team
		resp := api.Post("/api/v1/team",
			map[string]string{
				"name":  "John Doe",
				"email": "john@example.com",
			},
			"Cookie: session_token="+sessionToken,
		)
		assert.Equal(t, 200, resp.Code)
		var body map[string]any
		err := json.NewDecoder(resp.Body).Decode(&body)
		require.NoError(t, err)
		assert.NotEmpty(t, body["id"])
		assert.Equal(t, "Team member added successfully", body["message"])
	})

	t.Run("TeamAPIList", func(t *testing.T) {
		// Test GET /api/v1/team
		resp := api.Get("/api/v1/team",
			"Cookie: session_token="+sessionToken,
		)
		assert.Equal(t, 200, resp.Code)
		var body map[string]any
		err := json.NewDecoder(resp.Body).Decode(&body)
		require.NoError(t, err)
		members := body["members"].([]any)
		assert.Len(t, members, 1)
	})

	t.Run("TeamAPIUpdate", func(t *testing.T) {
		// First, add a team member
		addResp := api.Post("/api/v1/team",
			map[string]string{
				"name":  "Jane Doe",
				"email": "jane@example.com",
			},
			"Cookie: session_token="+sessionToken,
		)
		require.Equal(t, 200, addResp.Code)
		var addBody map[string]any
		err := json.NewDecoder(addResp.Body).Decode(&addBody)
		require.NoError(t, err)
		memberID := addBody["id"].(string)

		// Now update the team member
		updateResp := api.Put("/api/v1/team/"+memberID,
			map[string]string{
				"name":  "Jane Smith",
				"email": "jane.smith@example.com",
			},
			"Cookie: session_token="+sessionToken,
		)
		assert.Equal(t, 200, updateResp.Code)
		var updateBody map[string]any
		err = json.NewDecoder(updateResp.Body).Decode(&updateBody)
		require.NoError(t, err)
		assert.Equal(t, "Team member updated successfully", updateBody["message"])
	})

	t.Run("TeamAPIDelete", func(t *testing.T) {
		// First, add a team member to delete
		addResp := api.Post("/api/v1/team",
			map[string]string{
				"name":  "Bob Johnson",
				"email": "bob@example.com",
			},
			"Cookie: session_token="+sessionToken,
		)
		require.Equal(t, 200, addResp.Code)
		var addBody map[string]any
		err := json.NewDecoder(addResp.Body).Decode(&addBody)
		require.NoError(t, err)
		memberID := addBody["id"].(string)

		// Now delete the team member
		deleteResp := api.Delete("/api/v1/team/"+memberID,
			"Cookie: session_token="+sessionToken,
		)
		assert.Equal(t, 200, deleteResp.Code)
		var deleteBody map[string]any
		err = json.NewDecoder(deleteResp.Body).Decode(&deleteBody)
		require.NoError(t, err)
		assert.Equal(t, "Team member deleted successfully", deleteBody["message"])
	})
}
