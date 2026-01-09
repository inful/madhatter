package rota

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRegenerateScheduleWithLeave tests that regenerating a schedule from scratch
// uses fair R2 cover rotation when leave records exist.
func TestRegenerateScheduleWithLeave(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	charlieID, err := db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)
	daveID, err := db.AddTeamMember(ctx, "Dave", "dave@example.com")
	require.NoError(t, err)

	// Create leave records FIRST (before generating schedule)
	_, err = db.CreateLeaveRecord(ctx, aliceID, "sick", "2024-01-15", "2024-01-15") // Monday
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, bobID, "sick", "2024-01-16", "2024-01-16") // Tuesday
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, charlieID, "sick", "2024-01-17", "2024-01-17") // Wednesday
	require.NoError(t, err)

	// Now regenerate schedule (this simulates the "regenerate from scratch" button)
	engine := NewEngine(db)
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 19, 0, 0, 0, 0, time.UTC)
	err = engine.GenerateSchedule(ctx, startDate, endDate)
	require.NoError(t, err)

	// Check cover assignments
	members, err := db.GetActiveTeamMembers(ctx)
	require.NoError(t, err)
	memberNames := make(map[string]string)
	for _, m := range members {
		memberNames[m.ID] = m.Name
	}

	// Get assignments
	covers := make(map[string]string)
	for _, date := range []string{"2024-01-15", "2024-01-16", "2024-01-17"} {
		assignments, err := db.GetAssignmentsByDate(ctx, date)
		require.NoError(t, err)
		for _, a := range assignments {
			if a.IsCover {
				covers[date] = a.MemberID
			}
		}
	}

	t.Logf("Cover for Alice (Jan 15): %s", memberNames[covers["2024-01-15"]])
	t.Logf("Cover for Bob (Jan 16): %s", memberNames[covers["2024-01-16"]])
	t.Logf("Cover for Charlie (Jan 17): %s", memberNames[covers["2024-01-17"]])

	// Expected R2 rotation (independent):
	// Jan 15 (Alice out): R2 starts from 0, Alice on leave, next available is Bob (index 1)
	// Jan 16 (Bob out): R2 continues from Bob, Bob just covered so next is Charlie (index 2)
	// Jan 17 (Charlie out): R2 continues from Charlie, Charlie just covered so next is Dave (index 3)
	require.Equal(t, bobID, covers["2024-01-15"], "Bob should cover (R2 starts, skip Alice)")
	require.Equal(t, charlieID, covers["2024-01-16"], "Charlie should cover (R2 continues)")
	require.Equal(t, daveID, covers["2024-01-17"], "Dave should cover (R2 continues)")
}

// TestRegenerateWithMultipleLeaves tests R2 rotation wrapping.
func TestRegenerateWithMultipleLeaves(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add 4 team members
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	charlieID, err := db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)
	daveID, err := db.AddTeamMember(ctx, "Dave", "dave@example.com")
	require.NoError(t, err)

	// Create 5 leave records (more than team size to test wrapping)
	_, err = db.CreateLeaveRecord(ctx, aliceID, "sick", "2024-01-15", "2024-01-15")
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, bobID, "sick", "2024-01-16", "2024-01-16")
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, charlieID, "sick", "2024-01-17", "2024-01-17")
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, daveID, "sick", "2024-01-18", "2024-01-18")
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, aliceID, "sick", "2024-01-19", "2024-01-19")
	require.NoError(t, err)

	// Generate schedule
	engine := NewEngine(db)
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 19, 0, 0, 0, 0, time.UTC)
	err = engine.GenerateSchedule(ctx, startDate, endDate)
	require.NoError(t, err)

	// Get members for name lookup
	members, err := db.GetActiveTeamMembers(ctx)
	require.NoError(t, err)
	memberNames := make(map[string]string)
	for _, m := range members {
		memberNames[m.ID] = m.Name
	}

	// Get cover assignments
	covers := make(map[string]string)
	for _, date := range []string{"2024-01-15", "2024-01-16", "2024-01-17", "2024-01-18", "2024-01-19"} {
		assignments, err := db.GetAssignmentsByDate(ctx, date)
		require.NoError(t, err)
		for _, a := range assignments {
			if a.IsCover {
				covers[date] = a.MemberID
			}
		}
	}

	t.Logf("Jan 15 (Alice out): %s covers", memberNames[covers["2024-01-15"]])
	t.Logf("Jan 16 (Bob out): %s covers", memberNames[covers["2024-01-16"]])
	t.Logf("Jan 17 (Charlie out): %s covers", memberNames[covers["2024-01-17"]])
	t.Logf("Jan 18 (Dave out): %s covers", memberNames[covers["2024-01-18"]])
	t.Logf("Jan 19 (Alice out): %s covers", memberNames[covers["2024-01-19"]])

	// Expected R2 rotation with wrapping:
	// Team: Alice(0), Bob(1), Charlie(2), Dave(3)
	// Jan 15 (Alice out): R2 starts, Alice on leave -> Bob (1)
	// Jan 16 (Bob out): R2 continues from Bob -> Charlie (2)
	// Jan 17 (Charlie out): R2 continues from Charlie -> Dave (3)
	// Jan 18 (Dave out): R2 continues from Dave -> wrap to Alice (0) - Alice is NOT on leave today!
	// Jan 19 (Alice out again): R2 continues from Alice -> Bob (1)

	require.Equal(t, bobID, covers["2024-01-15"], "Bob covers Jan 15")
	require.Equal(t, charlieID, covers["2024-01-16"], "Charlie covers Jan 16")
	require.Equal(t, daveID, covers["2024-01-17"], "Dave covers Jan 17")
	require.Equal(t, aliceID, covers["2024-01-18"], "Alice covers Jan 18 (wraps after Dave, Alice available)")
	require.Equal(t, bobID, covers["2024-01-19"], "Bob covers Jan 19")
}
