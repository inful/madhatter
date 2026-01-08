package rota

import (
	"context"
	"testing"

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

	ctx := context.Background()
	aliceID, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")

	// Create leave
	leaveID, err := db.CreateLeaveRecord(ctx, aliceID, "sick", "2024-01-15", "2024-01-15")
	require.NoError(t, err)
	t.Logf("Created leave ID: %s", leaveID)

	// Get it back
	leave, err := db.GetLeaveByID(ctx, leaveID)
	require.NoError(t, err)
	t.Logf("Retrieved leave: %+v", leave)
	t.Logf("StartDate: %v", leave.StartDate)
	t.Logf("EndDate: %v", leave.EndDate)

	// Try parsing (no longer needed since it's already time.Time)
	t.Logf("StartDate is already time.Time: %v", leave.StartDate)

	// Check what GetLeaveByDate returns
	leaves, err := db.GetLeaveByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	t.Logf("GetLeaveByDate result: %+v", leaves)
	if len(leaves) > 0 {
		t.Logf("First leave StartDate: %v", leaves[0].StartDate)
	}
}

func TestEngine_GenerateSchedule_BasicRoundRobin(t *testing.T) {
	// ... (rest of the tests)
}

// ... (rest of the file)
