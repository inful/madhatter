package api

import (
	"bytes"
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

	// Create server
	server := NewServer(db)

	return server, cleanup
}

func TestTeamEndpoints(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Test adding team member
	t.Run("AddTeamMember", func(t *testing.T) {
		body := `{"name":"Alice Johnson","email":"alice@example.com"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/team", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.handleAddTeam(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp AddTeamOutput
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.NotEmpty(t, resp.ID)
		assert.Equal(t, "Team member added successfully", resp.Message)
	})

	// Test listing team members
	t.Run("ListTeamMembers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/team", nil)
		w := httptest.NewRecorder()

		server.handleListTeam(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp ListTeamOutput
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Len(t, resp.Members, 1)
		assert.Equal(t, "Alice Johnson", resp.Members[0].Name)
	})
}

func TestScheduleEndpoints(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Setup: Add team members
	_, err := server.db.AddTeamMember("Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = server.db.AddTeamMember("Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = server.db.AddTeamMember("Charlie", "charlie@example.com")
	require.NoError(t, err)

	t.Run("GenerateSchedule", func(t *testing.T) {
		body := `{"start_date":"2024-01-01","end_date":"2024-01-31"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/schedule/generate", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.handleGenerateSchedule(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp GenerateScheduleOutput
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Equal(t, "Schedule generated successfully", resp.Message)

		// Verify assignments were created
		assignments, err := server.db.GetAssignmentsByDate("2024-01-01")
		require.NoError(t, err)
		assert.Len(t, assignments, 1)
	})
}

func TestLeaveEndpoints(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Setup: Add team members and generate schedule
	aliceID, _ := server.db.AddTeamMember("Alice", "alice@example.com")
	_, _ = server.db.AddTeamMember("Bob", "bob@example.com")
	_, _ = server.db.AddTeamMember("Charlie", "charlie@example.com")

	engine := server.engine
	err := engine.GenerateSchedule(
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)

	t.Run("ReportLeaveWithAutoCover", func(t *testing.T) {
		body := `{"member_id":"` + aliceID + `","type":"sick","start_date":"2024-01-15","end_date":"2024-01-17"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/leave", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.handleReportLeave(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp ReportLeaveOutput
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.NotEmpty(t, resp.LeaveID)

		// Verify covers were assigned
		// Jan 15 is a Monday, Alice was assigned, now should be covered
		assignments, err := server.db.GetAssignmentsByDate("2024-01-15")
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

	// Setup: Add team member and generate schedule
	aliceID, _ := server.db.AddTeamMember("Alice", "alice@example.com")
	_, _ = server.db.AddTeamMember("Bob", "bob@example.com")

	engine := server.engine
	err := engine.GenerateSchedule(
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)

	t.Run("SubscribeCalendar", func(t *testing.T) {
		body := `{"member_id":"` + aliceID + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/calendar/subscribe", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.handleSubscribeCalendar(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp SubscribeCalendarOutput
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.NotEmpty(t, resp.Token)
		assert.Contains(t, resp.CalendarURL, "/api/v1/calendar/")
		assert.Contains(t, resp.CalendarURL, "/ics")
	})

	t.Run("GetICSFeed", func(t *testing.T) {
		// First generate a schedule for the current month
		now := time.Now().UTC()
		startDate := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		endDate := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC)
		err := server.engine.GenerateSchedule(startDate, endDate)
		require.NoError(t, err)

		// Then create subscription
		token, err := server.db.CreateCalendarSubscription(aliceID)
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
