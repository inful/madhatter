package rota

import (
	"context"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegenerateSchedule_PreservesRotationAnchor(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	maintenance := NewScheduleMaintenance(db)
	fullStart := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	fullEnd := time.Date(2024, 1, 19, 0, 0, 0, 0, time.UTC)

	created, err := maintenance.GenerateMissingDays(ctx, fullStart, fullEnd)
	require.NoError(t, err)
	assert.True(t, created)

	targetStart := time.Date(2024, 1, 17, 0, 0, 0, 0, time.UTC)
	targetEnd := time.Date(2024, 1, 19, 0, 0, 0, 0, time.UTC)

	beforeAssignments, err := db.GetAssignmentsByDateRange(
		ctx,
		targetStart.Format("2006-01-02"),
		targetEnd.Format("2006-01-02"),
	)
	require.NoError(t, err)
	require.NotEmpty(t, beforeAssignments)

	beforeByDate := make(map[string]string, len(beforeAssignments))
	for _, assignment := range beforeAssignments {
		if assignment.IsCover {
			continue
		}
		beforeByDate[assignment.Date] = assignment.MemberID
	}

	count, err := maintenance.RegenerateSchedule(ctx, targetStart, targetEnd)
	require.NoError(t, err)
	assert.Equal(t, len(beforeByDate), count)

	afterAssignments, err := db.GetAssignmentsByDateRange(
		ctx,
		targetStart.Format("2006-01-02"),
		targetEnd.Format("2006-01-02"),
	)
	require.NoError(t, err)

	afterByDate := make(map[string]string, len(afterAssignments))
	for _, assignment := range afterAssignments {
		if assignment.IsCover {
			continue
		}
		afterByDate[assignment.Date] = assignment.MemberID
	}

	assert.Equal(t, beforeByDate, afterByDate, "regeneration should preserve the original rotation anchor")
}

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
	_, err = db.CreateLeaveRecord(ctx, aliceID, "2024-01-15", "2024-01-15", database.LeaveTypeLeave) // Monday
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, bobID, "2024-01-16", "2024-01-16", database.LeaveTypeLeave) // Tuesday
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, charlieID, "2024-01-17", "2024-01-17", database.LeaveTypeLeave) // Wednesday
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

	// Expected covers. The cover rotation is a persisted state
	// (last_date, last_index) that advances by one slot per working
	// day. The first call seeds the state at the call's date with
	// index 0, so:
	//   Jan 15 → state seeded at (Jan 15, 0) → Alice on leave → Bob
	//   Jan 16 → state advances to (Jan 16, 1) → Bob on leave → Charlie
	//   Jan 17 → state advances to (Jan 17, 2) → Charlie on leave → Dave
	require.Equal(t, bobID, covers["2024-01-15"], "Bob should cover (state seeded at index 0, Alice on leave → next)")
	require.Equal(t, charlieID, covers["2024-01-16"], "Charlie should cover (state advanced to index 1, Bob on leave → next)")
	require.Equal(t, daveID, covers["2024-01-17"], "Dave should cover (state advanced to index 2, Charlie on leave → next)")
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
	_, err = db.CreateLeaveRecord(ctx, aliceID, "2024-01-15", "2024-01-15", database.LeaveTypeLeave)
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, bobID, "2024-01-16", "2024-01-16", database.LeaveTypeLeave)
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, charlieID, "2024-01-17", "2024-01-17", database.LeaveTypeLeave)
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, daveID, "2024-01-18", "2024-01-18", database.LeaveTypeLeave)
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, aliceID, "2024-01-19", "2024-01-19", database.LeaveTypeLeave)
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

	// Expected covers. The cover rotation is a persisted state
	// (last_date, last_index) that advances by one slot per working
	// day. The first call seeds the state at the call's date with
	// index 0, so:
	//   Jan 15 → state seeded at (Jan 15, 0) → index 0 → Alice on leave → Bob
	//   Jan 16 → state advances to (Jan 16, 1) → Bob on leave → Charlie
	//   Jan 17 → state advances to (Jan 17, 2) → Charlie on leave → Dave
	//   Jan 18 → state advances to (Jan 18, 3) → Dave on leave → Alice
	//   Jan 19 → state advances to (Jan 19, 0) → Alice on leave → Bob
	// This is the wrap-around behavior: every team member covers
	// exactly once across the five consecutive leaves, then the
	// rotation resets at Jan 19.

	require.Equal(t, bobID, covers["2024-01-15"], "Bob covers Jan 15")
	require.Equal(t, charlieID, covers["2024-01-16"], "Charlie covers Jan 16")
	require.Equal(t, daveID, covers["2024-01-17"], "Dave covers Jan 17")
	require.Equal(t, aliceID, covers["2024-01-18"], "Alice covers Jan 18")
	require.Equal(t, bobID, covers["2024-01-19"], "Bob covers Jan 19 (rotation wraps)")
}
