package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/database"
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

	// Create server (development = false for tests)
	// Templates are now embedded, so this should always succeed
	server, err := NewServer(db, false)
	require.NoError(t, err, "Failed to create server")

	return server, cleanup
}

func TestTeamEndpoints(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Test adding team member
	t.Run("AddTeamMember", func(t *testing.T) {
		input := &AddTeamInput{}
		input.Body.Name = "Alice Johnson"
		input.Body.Email = "alice@example.com"

		resp, err := server.handleAddTeam(context.Background(), input)
		require.NoError(t, err)
		assert.NotEmpty(t, resp.Body.ID)
		assert.Equal(t, "Team member added successfully", resp.Body.Message)
	})

	// Test listing team members
	t.Run("ListTeamMembers", func(t *testing.T) {
		resp, err := server.handleListTeam(context.Background(), &struct{}{})
		require.NoError(t, err)
		assert.Len(t, resp.Body.Members, 1)
		assert.Equal(t, "Alice Johnson", resp.Body.Members[0].Name)
	})
}

func TestScheduleEndpoints(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Setup: Add team members
	ctx := context.Background()
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

	ctx := context.Background()

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
		input.Body.Type = "sick"
		input.Body.StartDate = "2024-01-15"
		input.Body.EndDate = "2024-01-17"

		resp, err := server.handleReportLeave(ctx, input)
		require.NoError(t, err)
		assert.NotEmpty(t, resp.Body.LeaveID)

		// Verify covers were assigned
		// Jan 15 is a Monday, Alice was assigned, now should be covered
		assignments, err := server.db.GetAssignmentsByDate(ctx, "2024-01-15")
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
}

func TestCalendarEndpoints(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := context.Background()

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

// TestHUMAAPIIntegration tests the full HUMA API integration.
func TestHUMAAPIIntegration(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Create a test request to the HUMA API
	router := server.router

	t.Run("TeamAPI", func(t *testing.T) {
		// Test POST /api/v1/team
		body := `{"name":"Test User","email":"test@example.com"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/team", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp map[string]any
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.NotEmpty(t, resp["id"])
		assert.Equal(t, "Team member added successfully", resp["message"])
	})

	t.Run("TeamAPIList", func(t *testing.T) {
		// Test GET /api/v1/team
		req := httptest.NewRequest(http.MethodGet, "/api/v1/team", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp map[string]any
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		members := resp["members"].([]any)
		assert.Len(t, members, 1)
	})
}
