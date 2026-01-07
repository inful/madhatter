package rota

import (
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) (*database.DB, func()) {
	t.Helper()
	// Create in-memory test database
	db, err := database.New(":memory:")
	require.NoError(t, err)

	// Return cleanup function
	cleanup := func() {
		_ = db.Close()
	}

	return db, cleanup
}

func TestDebugLeaveDates(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	aliceID, _ := db.AddTeamMember("Alice", "alice@example.com")

	// Create leave
	leaveID, err := db.CreateLeaveRecord(aliceID, "sick", "2024-01-15", "2024-01-15")
	require.NoError(t, err)
	t.Logf("Created leave ID: %s", leaveID)

	// Get it back
	leave, err := db.GetLeaveByID(leaveID)
	require.NoError(t, err)
	t.Logf("Retrieved leave: %+v", leave)
	t.Logf("StartDate: '%s' (len=%d)", leave.StartDate, len(leave.StartDate))
	t.Logf("EndDate: '%s' (len=%d)", leave.EndDate, len(leave.EndDate))

	// Try parsing
	parsedStart, err := time.Parse("2006-01-02", leave.StartDate)
	t.Logf("Parsed start: %v, err: %v", parsedStart, err)

	// Check what GetLeaveByDate returns
	leaves, err := db.GetLeaveByDate("2024-01-15")
	require.NoError(t, err)
	t.Logf("GetLeaveByDate result: %+v", leaves)
	if len(leaves) > 0 {
		t.Logf("First leave StartDate: '%s'", leaves[0].StartDate)
	}
}

func TestEngine_GenerateSchedule_BasicRoundRobin(t *testing.T) {
	// ... (rest of the tests)
}

// ... (rest of the file)
