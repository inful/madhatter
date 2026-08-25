package rota

import (
	"context"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/require"
)

// TestEngine_AssignCoversForLeave_Idempotent verifies that re-running
// AssignCoversForLeave on the same leave produces the same cover assignment.
// This matters because HandleLeaveChange is called from multiple paths
// (web form submit, API, manual reprocess) and the engine must converge
// rather than rotate the cover on every invocation.
func TestEngine_AssignCoversForLeave_Idempotent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 19, 0, 0, 0, 0, time.UTC)
	require.NoError(t, engine.GenerateSchedule(ctx, startDate, endDate))

	// Bob on leave Mon-Fri (a multi-day leave so the cover rotates
	// through the rest of the team on each day).
	leaveID, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-15", "2024-01-19", database.LeaveTypeLeave)
	require.NoError(t, err)
	require.NoError(t, engine.AssignCoversForLeave(ctx, leaveID))

	firstRun := snapshotCovers(t, ctx, db, "2024-01-15", "2024-01-19")

	// Re-run the same leave handler. The cover on each day must be
	// identical to the first run.
	require.NoError(t, engine.AssignCoversForLeave(ctx, leaveID))

	secondRun := snapshotCovers(t, ctx, db, "2024-01-15", "2024-01-19")
	require.Equal(t, firstRun, secondRun, "re-running AssignCoversForLeave must be idempotent")
}

// TestEngine_HandleLeaveChange_Idempotent verifies that re-running
// HandleLeaveChange (which includes reconcileCoversForDateRange) for a
// leave is also stable. Reconcile deletes covers for no-longer-on-leave
// originals and then AssignCoversForLeave re-creates them, so each call
// rebuilds the cover set from scratch; the rotation must be deterministic
// across that delete+recreate cycle. This is the property a periodic
// reassignment runner relies on to converge to the current algorithm's
// output on every invocation.
func TestEngine_HandleLeaveChange_Idempotent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)
	maintenance := NewScheduleMaintenance(db)

	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 19, 0, 0, 0, 0, time.UTC)
	require.NoError(t, engine.GenerateSchedule(ctx, startDate, endDate))

	leaveID, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-16", "2024-01-18", database.LeaveTypeLeave)
	require.NoError(t, err)
	require.NoError(t, maintenance.HandleLeaveChange(ctx, leaveID))

	firstRun := snapshotCovers(t, ctx, db, "2024-01-16", "2024-01-18")

	// Re-run the full HandleLeaveChange pipeline (reconcile + assign).
	require.NoError(t, maintenance.HandleLeaveChange(ctx, leaveID))

	secondRun := snapshotCovers(t, ctx, db, "2024-01-16", "2024-01-18")
	require.Equal(t, firstRun, secondRun, "re-running HandleLeaveChange must be idempotent")

	// And a third run, for good measure.
	require.NoError(t, maintenance.HandleLeaveChange(ctx, leaveID))
	thirdRun := snapshotCovers(t, ctx, db, "2024-01-16", "2024-01-18")
	require.Equal(t, firstRun, thirdRun, "HandleLeaveChange must remain stable across many runs")
}

// TestEngine_AssignCoversForLeave_StableAcrossMultipleRuns verifies that
// the cover assignment is deterministic given the same input: with no
// prior covers, the same three back-to-back leaves always produce the
// same three covers in the same order. (The R2 rotation is anchored on
// the most recent past cover, so the "starting" position is fixed when
// the rota is empty.)
func TestEngine_AssignCoversForLeave_StableAcrossMultipleRuns(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	charlieID, err := db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Dave", "dave@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	// Generate the schedule for the same window both runs.
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 19, 0, 0, 0, 0, time.UTC)
	require.NoError(t, engine.GenerateSchedule(ctx, startDate, endDate))

	// Bob/Tue, Charlie/Wed, Dave/Thu — everyone in this test is scheduled
	// on the day they're on leave. The test runs the same three leaves
	// twice; between the runs we delete the assignments for those dates
	// so the second run starts from the same empty state.
	pattern := []struct {
		date   string
		member string
	}{
		{"2024-01-16", bobID},
		{"2024-01-17", charlieID},
		{"2024-01-18", memberIDByName(t, db, "Dave")},
	}

	run := func() []string {
		covers := make([]string, 0, len(pattern))
		for _, p := range pattern {
			id, err := db.CreateLeaveRecord(ctx, p.member, p.date, p.date, database.LeaveTypeLeave)
			require.NoError(t, err)
			require.NoError(t, engine.AssignCoversForLeave(ctx, id))
			covers = append(covers, getCoverMemberID(t, ctx, db, p.date))
		}
		return covers
	}

	// First pass.
	firstCovers := run()

	// Reset by deleting every assignment for the dates in the pattern so
	// the second pass starts from the same empty state, and reset the
	// leave statuses by deleting them and re-creating.
	for _, p := range pattern {
		_, err := db.ExecContext(ctx, `DELETE FROM rota_assignments WHERE date = ?`, p.date)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `DELETE FROM leave_records WHERE start_date = ? AND end_date = ?`, p.date, p.date)
		require.NoError(t, err)
	}

	secondCovers := run()

	require.Equal(t, firstCovers, secondCovers,
		"with the same starting state, the same leaves must produce the same covers in the same order")
}

// snapshotCovers returns a map of date -> cover member id for every
// business day in the given inclusive date range. Returns "" for a date
// with no cover.
func snapshotCovers(t *testing.T, ctx context.Context, db *database.DB, startDateStr, endDateStr string) map[string]string {
	t.Helper()
	start, err := time.Parse("2006-01-02", startDateStr)
	require.NoError(t, err)
	end, err := time.Parse("2006-01-02", endDateStr)
	require.NoError(t, err)

	out := make(map[string]string)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		out[d.Format("2006-01-02")] = getCoverMemberID(t, ctx, db, d.Format("2006-01-02"))
	}
	return out
}
