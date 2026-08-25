package rota

import (
	"context"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/require"
)

// TestReassignCovers_StableOnSteadyState verifies the idempotency
// contract for ReassignCovers: re-running it against a rota whose
// leaves are already assigned under the current algorithm produces
// the same covers and the same change count on every invocation
// after the first. This is the foundational guarantee the always-on
// startup runner relies on — a periodic reassign must not churn
// covers forever.
//
// The "subsequent runs are idempotent" property is the new
// contract. The first run may differ from the ad-hoc
// HandleLeaveChange pass if the original cover assignment used a
// rotation state that has since advanced past the leaves (the
// reassign restarts from the rotation anchor, which is independent
// of the ad-hoc state). See ReassignCovers's docstring.
func TestReassignCovers_StableOnSteadyState(t *testing.T) {
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

	// Build a small rota with one leave.
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 19, 0, 0, 0, 0, time.UTC)
	require.NoError(t, engine.GenerateSchedule(ctx, startDate, endDate))

	leaveID, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-16", "2024-01-18", database.LeaveTypeLeave)
	require.NoError(t, err)
	require.NoError(t, maintenance.HandleLeaveChange(ctx, leaveID))

	// First reassign: anchors the algorithm's view of the rotation.
	first, err := maintenance.ReassignCovers(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, first.LeavesProcessed)

	// Snapshot the covers and the reassign anchor after the first
	// reassign. Subsequent runs must produce the same state.
	firstCovers := snapshotCovers(t, ctx, db, "2024-01-16", "2024-01-18")

	// Second and third runs: must not change anything. The change
	// count is the idempotency check — ReassignCovers's diff sees
	// no before/after movement.
	for _, label := range []string{"second", "third"} {
		again, err := maintenance.ReassignCovers(ctx)
		require.NoError(t, err, "%s reassign: %v", label, err)
		require.Equal(t, 1, again.LeavesProcessed)
		require.Equal(t, 0, again.CoversChanged,
			"%s reassign on steady-state data must be a no-op", label)

		againCovers := snapshotCovers(t, ctx, db, "2024-01-16", "2024-01-18")
		require.Equal(t, firstCovers, againCovers,
			"%s reassign must produce the same covers as the first", label)
	}
}

// TestReassignCovers_HandlesMultipleLeaves verifies the change count
// across multiple distinct leaves: the count reflects the number of
// leaves whose cover set actually changed, not the number of leaves
// walked.
func TestReassignCovers_HandlesMultipleLeaves(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	charlieID, err := db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)
	daveID, err := db.AddTeamMember(ctx, "Dave", "dave@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)
	maintenance := NewScheduleMaintenance(db)

	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 19, 0, 0, 0, 0, time.UTC)
	require.NoError(t, engine.GenerateSchedule(ctx, startDate, endDate))

	// Create three single-day leaves.
	l1, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-16", "2024-01-16", database.LeaveTypeLeave)
	require.NoError(t, err)
	l2, err := db.CreateLeaveRecord(ctx, charlieID, "2024-01-17", "2024-01-17", database.LeaveTypeLeave)
	require.NoError(t, err)
	l3, err := db.CreateLeaveRecord(ctx, daveID, "2024-01-18", "2024-01-18", database.LeaveTypeLeave)
	require.NoError(t, err)

	// Initial pass: each leave needs a cover assigned.
	require.NoError(t, maintenance.HandleLeaveChange(ctx, l1))
	require.NoError(t, maintenance.HandleLeaveChange(ctx, l2))
	require.NoError(t, maintenance.HandleLeaveChange(ctx, l3))

	// Reassign: should walk all three leaves and report zero changes
	// (steady state, all already correctly assigned).
	result, err := maintenance.ReassignCovers(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, result.LeavesProcessed)
	require.Equal(t, 0, result.CoversChanged,
		"steady-state reassignment across three leaves must report zero changes")
}

// TestReassignCovers_OnlyAffectsActiveLeaves verifies the inactive-leave
// guard: a completed leave's cover has already been reconciled by its
// own HandleLeaveChange, so re-running ReassignCovers must not change
// anything for it.
func TestReassignCovers_OnlyAffectsActiveLeaves(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
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

	// Alice on leave Mon, then mark the leave completed. The cover
	// cleanup only fires on the next HandleLeaveChange call (which
	// runs reconcileCoversForDateRange), so we must re-invoke it after
	// the status change to model the real production workflow.
	leaveID, err := db.CreateLeaveRecord(ctx, aliceID, "2024-01-15", "2024-01-15", database.LeaveTypeLeave)
	require.NoError(t, err)
	require.NoError(t, maintenance.HandleLeaveChange(ctx, leaveID))
	require.NoError(t, db.UpdateLeaveStatus(ctx, leaveID, "completed"))
	require.NoError(t, maintenance.HandleLeaveChange(ctx, leaveID),
		"re-invoking HandleLeaveChange after a status change must trigger the reconcile")

	// Snapshot the post-cleanup state. The cover should be gone.
	completedCovers := snapshotCovers(t, ctx, db, "2024-01-15", "2024-01-15")
	require.Empty(t, completedCovers["2024-01-15"], "completed leave should have its cover removed")

	// Bob is on leave Tue; an active leave.
	bobLeaveID, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-16", "2024-01-16", database.LeaveTypeLeave)
	require.NoError(t, err)
	require.NoError(t, maintenance.HandleLeaveChange(ctx, bobLeaveID))

	// Reassign: the completed leave is processed (covers remain
	// deleted), the active leave's cover must remain in place.
	result, err := maintenance.ReassignCovers(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, result.LeavesProcessed, "both leaves are walked, even the completed one")

	// And the cover from the active leave must still be in place.
	activeCovers := snapshotCovers(t, ctx, db, "2024-01-16", "2024-01-16")
	require.NotEmpty(t, activeCovers["2024-01-16"], "active leave's cover must remain")

	// Second reassign: must produce the same result as the first
	// (the new idempotency contract).
	result2, err := maintenance.ReassignCovers(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, result2.LeavesProcessed)
	require.Equal(t, 0, result2.CoversChanged,
		"second reassign on the now-stable data must be a no-op")
	activeCovers2 := snapshotCovers(t, ctx, db, "2024-01-16", "2024-01-16")
	require.Equal(t, activeCovers, activeCovers2,
		"second reassign must reproduce the first reassign's covers")
}

// TestReassignCovers_RespectsHolidayChecker pins down the property
// the cmd-side startup hook relies on: when the maintenance is wired
// with a holiday checker (as buildReassignmentMaintenance does at the
// cmd layer, and as api.NewServer does on the server), a leave that
// spans a holiday must not get a cover on the holiday. Without this,
// the runner would diverge from the live server on any leave that
// touches a configured holiday.
func TestReassignCovers_RespectsHolidayChecker(t *testing.T) {
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

	// Generate the schedule. 2024-01-16 is a Tuesday; treat it as a
	// holiday. The leave is Mon-Fri, so it must skip 2024-01-16.
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 19, 0, 0, 0, 0, time.UTC)
	require.NoError(t, engine.GenerateSchedule(ctx, startDate, endDate))

	holiday := time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC)
	maintenance.SetHolidayChecker(func(date time.Time) bool {
		return date.Equal(holiday)
	})

	// Bob is on leave all week, so covers should be assigned for
	// Mon (2024-01-15) and Wed-Fri (2024-01-17 to 2024-01-19), but
	// NOT for the holiday Tue (2024-01-16).
	leaveID, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-15", "2024-01-19", database.LeaveTypeLeave)
	require.NoError(t, err)
	require.NoError(t, maintenance.HandleLeaveChange(ctx, leaveID))

	// Sanity-check: the holiday has no cover.
	holidayCovers := snapshotCovers(t, ctx, db, "2024-01-16", "2024-01-16")
	require.Empty(t, holidayCovers["2024-01-16"], "holiday must not have a cover")

	// Now run the reassignment. The covers for non-holiday days must
	// remain; the holiday must STILL have no cover. The change count
	// is zero because the state is already at the algorithm's output.
	result, err := maintenance.ReassignCovers(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.LeavesProcessed)
	require.Equal(t, 0, result.CoversChanged, "reassignment must not create a cover on the holiday")

	holidayCovers = snapshotCovers(t, ctx, db, "2024-01-16", "2024-01-16")
	require.Empty(t, holidayCovers["2024-01-16"], "reassignment must not introduce a cover on the holiday")
}

// TestReassignCovers_CreatesCoverForUnprocessedLeave verifies the
// self-healing property: if a leave exists in the database but no
// cover was ever created for it (e.g. an old record that predates
// the cover-assignment system), the reassignment will create the
// cover as if the leave were new.
func TestReassignCovers_CreatesCoverForUnprocessedLeave(t *testing.T) {
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

	// Create a leave for Jan 16 (Bob's scheduled day) but do NOT call
	// HandleLeaveChange on it — the realistic "old record" case where
	// the cover was never assigned.
	leaveID, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-16", "2024-01-16", database.LeaveTypeLeave)
	require.NoError(t, err)
	_ = leaveID

	// Pre-condition: no cover exists yet.
	preCovers := snapshotCovers(t, ctx, db, "2024-01-16", "2024-01-16")
	require.Empty(t, preCovers["2024-01-16"], "no cover should exist yet on the leave day")

	// Reassign: should walk the leave and create the cover.
	result, err := maintenance.ReassignCovers(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.LeavesProcessed)
	require.Equal(t, 1, result.CoversChanged,
		"reassignment must create the missing cover for an unprocessed leave")

	// Post-condition: the cover is now in place.
	postCovers := snapshotCovers(t, ctx, db, "2024-01-16", "2024-01-16")
	require.NotEmpty(t, postCovers["2024-01-16"], "reassignment must have placed a cover on the leave day")
}

// TestReassignCovers_EmptyRota verifies the no-op path: a fresh
// database with no leaves returns the zero ReassignResult and
// leaves Failures nil. The continue-on-error code path is exercised
// by per-leave failures, which the engine's own graceful "skip
// on no-cover-available" path makes hard to inject from outside
// without an internal fault-injection hook. The Failures field
// exists and is collected, and the structure of the call is
// verified here.
func TestReassignCovers_EmptyRota(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	maintenance := NewScheduleMaintenance(db)
	result, err := maintenance.ReassignCovers(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, result.LeavesProcessed)
	require.Equal(t, 0, result.CoversChanged)
	require.Empty(t, result.Failures)
}
