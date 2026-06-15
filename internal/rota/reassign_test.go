package rota

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestReassignCovers_StableOnSteadyState verifies that re-running
// ReassignCovers against a rota that was already assigned under the
// current algorithm is a no-op: the change count is zero because
// HandleLeaveChange is idempotent on the cover side (see
// TestEngine_AssignCoversForLeave_Idempotent and
// TestEngine_HandleLeaveChange_Idempotent).
//
// This is the foundational guarantee the periodic runner relies on: on
// every server startup it can re-run ReassignCovers and the change
// count tells the operator whether the algorithm did anything new.
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

	leaveID, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-16", "2024-01-18")
	require.NoError(t, err)
	require.NoError(t, maintenance.HandleLeaveChange(ctx, leaveID))

	// The first reassignment on a freshly-HandleLeaveChange'd rota must
	// be a no-op: HandleLeaveChange already created the cover, and
	// ReassignCovers diffs before/after snapshots, so a no-op is the
	// correct answer. (CoversChanged counts leaves whose cover set
	// actually moved during the run, not leaves that had a cover at
	// the start.)
	first, err := maintenance.ReassignCovers(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, first.LeavesProcessed)
	require.Equal(t, 0, first.CoversChanged, "first run on a steady-state rota must be a no-op")

	// Second run: same data, same answer.
	second, err := maintenance.ReassignCovers(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, second.LeavesProcessed)
	require.Equal(t, 0, second.CoversChanged, "second run on a steady-state rota must not change any covers")
}

// TestReassignCoversIfStale_NoOpWhenUpToDate verifies the version check:
// when the on-disk applied_version already matches CoverAlgorithmVersion
// the runner is a no-op, returns WasStale=false, and does not touch the
// state row.
func TestReassignCoversIfStale_NoOpWhenUpToDate(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	maintenance := NewScheduleMaintenance(db)

	// Pretend the on-disk state is already at the binary's version.
	require.NoError(t, db.SetCoverAlgorithmApplied(ctx, CoverAlgorithmVersion, 0))

	result, err := maintenance.ReassignCoversIfStale(ctx)
	require.NoError(t, err)
	require.False(t, result.WasStale, "WasStale must be false when on-disk matches binary")
	require.Equal(t, 0, result.LeavesProcessed, "no-op run must not walk any leaves")
	require.Equal(t, 0, result.CoversChanged)

	// And the on-disk state must be untouched.
	state, err := db.GetCoverAlgorithmState(ctx)
	require.NoError(t, err)
	require.Equal(t, CoverAlgorithmVersion, state.AppliedVersion)
}

// TestReassignCoversIfStale_RunsAndBumpsVersion verifies the trigger:
// on a fresh database (applied_version = 0) the runner walks every
// leave, computes a change count, and updates the on-disk version to
// CoverAlgorithmVersion along with the change count and timestamp.
func TestReassignCoversIfStale_RunsAndBumpsVersion(t *testing.T) {
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

	leaveID, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-16", "2024-01-18")
	require.NoError(t, err)
	// Intentionally do NOT call HandleLeaveChange here — the test wants
	// the reassignment runner itself to be the thing that materializes
	// the cover. This is the realistic case for an upgrade: the rota
	// already has leaves assigned under the old algorithm, and the
	// startup hook must do the work.
	_ = leaveID

	// The on-disk state is at the default (0). The runner must detect
	// the version gap and run.
	result, err := maintenance.ReassignCoversIfStale(ctx)
	require.NoError(t, err)
	require.True(t, result.WasStale, "WasStale must be true when on-disk is behind binary")
	require.Equal(t, 1, result.LeavesProcessed)
	require.GreaterOrEqual(t, result.CoversChanged, 1, "first run after a fresh DB must report a change")
	require.Equal(t, CoverAlgorithmVersion, result.NewVersion)

	// And the on-disk state must now be at the binary's version.
	state, err := db.GetCoverAlgorithmState(ctx)
	require.NoError(t, err)
	require.Equal(t, CoverAlgorithmVersion, state.AppliedVersion)
	require.Equal(t, result.CoversChanged, state.LastRunChanged)
	require.NotNil(t, state.LastRunAt)

	// A second call must be a no-op (already up to date).
	second, err := maintenance.ReassignCoversIfStale(ctx)
	require.NoError(t, err)
	require.False(t, second.WasStale, "second call must not re-run")
	require.Equal(t, 0, second.LeavesProcessed)
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
	l1, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-16", "2024-01-16")
	require.NoError(t, err)
	l2, err := db.CreateLeaveRecord(ctx, charlieID, "2024-01-17", "2024-01-17")
	require.NoError(t, err)
	l3, err := db.CreateLeaveRecord(ctx, daveID, "2024-01-18", "2024-01-18")
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
	leaveID, err := db.CreateLeaveRecord(ctx, aliceID, "2024-01-15", "2024-01-15")
	require.NoError(t, err)
	require.NoError(t, maintenance.HandleLeaveChange(ctx, leaveID))
	require.NoError(t, db.UpdateLeaveStatus(ctx, leaveID, "completed"))
	require.NoError(t, maintenance.HandleLeaveChange(ctx, leaveID),
		"re-invoking HandleLeaveChange after a status change must trigger the reconcile")

	// Snapshot the post-cleanup state. The cover should be gone.
	completedCovers := snapshotCovers(t, ctx, db, "2024-01-15", "2024-01-15")
	require.Empty(t, completedCovers["2024-01-15"], "completed leave should have its cover removed")

	// Bob is on leave Tue; an active leave.
	bobLeaveID, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-16", "2024-01-16")
	require.NoError(t, err)
	require.NoError(t, maintenance.HandleLeaveChange(ctx, bobLeaveID))

	// Reassign: the completed leave must be a no-op, the active leave
	// must not change.
	result, err := maintenance.ReassignCovers(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, result.LeavesProcessed, "both leaves are walked, even the completed one")
	require.Equal(t, 0, result.CoversChanged, "no covers should change on a steady-state rota")

	// And the cover from the active leave must still be in place.
	activeCovers := snapshotCovers(t, ctx, db, "2024-01-16", "2024-01-16")
	require.NotEmpty(t, activeCovers["2024-01-16"], "active leave's cover must remain")
}

// TestReassignCovers_RespectsHolidayChecker pins down the property
// the cmd-side startup hook relies on: when the maintenance is wired
// with a holiday checker (as buildReassignmentMaintenance does at the
// cmd layer, and as api.NewServer does on the server), a leave that
// spans a holiday must not get a cover on the holiday. Without this,
// the runner would diverge from the live server on any leave that
// touches a configured holiday — the very bug the code review
// caught.
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
	leaveID, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-15", "2024-01-19")
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
