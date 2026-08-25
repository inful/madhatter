package rota

import (
	"context"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/require"
)

// TestReassignCovers_IdempotentWithMultiDayLeaveAndVariedWalks is
// the regression test for the production bug "distributions are no
// longer idempotent" that surfaced after commit bcf72e8 made the
// R2 cover rotation state persist the actual cover slot.
//
// The bug shape is: a forward walk's findCover walks past on-leave
// members; the resulting state stores the actual cover slot, not
// the candidate. A subsequent backward walk (used by ReassignCovers
// under the old design, which read the ad-hoc state and saw an
// end-of-leave state far past the leave) computed the candidate as
// (actual - delta), not (candidate - delta), and that recovered a
// different starting candidate than the forward walk. So the same
// ReassignCovers run produced different covers on the second
// invocation.
//
// To exercise the variable-walk shape, seed the rota directly so
// Bob is the original on five consecutive days and Alice is on
// leave on Thu-Fri only — the per-day walk amount varies because
// the candidate's neighbors differ across days. The 3-person
// idempotency tests use a single on-leave member (constant walk
// amount) which hides the asymmetry; this test reproduces the
// production shape.
//
// New contract: subsequent reassigns are idempotent. The first
// reassign may differ from the initial HandleLeaveChange pass if
// the original pass's state had moved past the leaves; the
// reassign reprocesses from a fresh in-memory seed to match the
// ad-hoc path's empty-state seeding.
func TestReassignCovers_IdempotentWithMultiDayLeaveAndVariedWalks(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Carla", "carla@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Dave", "dave@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Eve", "eve@example.com")
	require.NoError(t, err)

	maintenance := NewScheduleMaintenance(db)

	// Seed Bob as the original on Jan 15-19 (Mon-Fri). This makes
	// Bob the leave person on every day of the leave — the
	// forward walk's findCover always walks past Bob.
	for _, d := range []string{"2024-01-15", "2024-01-16", "2024-01-17", "2024-01-18", "2024-01-19"} {
		var assignErr error
		_, assignErr = db.CreateRotaAssignment(ctx, d, bobID, false, nil)
		require.NoError(t, assignErr)
	}

	// Bob on leave Mon-Fri.
	bobLeaveID, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-15", "2024-01-19", database.LeaveTypeLeave)
	require.NoError(t, err)
	require.NoError(t, maintenance.HandleLeaveChange(ctx, bobLeaveID))

	// Alice on leave Thu-Fri (Jan 18-19). This is the variation:
	// on Mon-Wed the candidate walks past Bob only (1 step); on
	// Thu-Fri the candidate walks past Bob AND Alice (2 steps).
	// Pre-fix, the backward walk subtracted only delta from the
	// actual cover slot, not (delta + walks), so it computed a
	// different candidate than the forward walk.
	aliceLeaveID, err := db.CreateLeaveRecord(ctx, aliceID, "2024-01-18", "2024-01-19", database.LeaveTypeLeave)
	require.NoError(t, err)
	require.NoError(t, maintenance.HandleLeaveChange(ctx, aliceLeaveID))

	// Sanity: covers should exist for all 5 days.
	pre := snapshotCovers(t, ctx, db, "2024-01-15", "2024-01-19")
	for d := range pre {
		require.NotEmpty(t, pre[d], "sanity: cover must exist for %s after ad-hoc processing", d)
	}

	// First reassign: anchors the algorithm's view of the rotation.
	// The change count is implementation-defined — the first
	// reassign may produce different covers than the ad-hoc path
	// if the ad-hoc path's state had advanced past the leaves
	// (the reassign restarts from the empty state to match the
	// ad-hoc path's empty-state seeding). What matters for
	// idempotency is that the SECOND and subsequent runs produce
	// the same covers as the first.
	first, err := maintenance.ReassignCovers(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, first.LeavesProcessed)

	firstCovers := snapshotCovers(t, ctx, db, "2024-01-15", "2024-01-19")

	// Second reassign: must produce the same covers.
	second, err := maintenance.ReassignCovers(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, second.LeavesProcessed)
	require.Equal(t, 0, second.CoversChanged,
		"second reassign on a steady-state rota must be a no-op")

	secondCovers := snapshotCovers(t, ctx, db, "2024-01-15", "2024-01-19")
	require.Equal(t, firstCovers, secondCovers,
		"second reassign must produce identical covers to the first")

	// Third reassign for good measure.
	third, err := maintenance.ReassignCovers(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, third.LeavesProcessed)
	require.Equal(t, 0, third.CoversChanged,
		"third reassign on a steady-state rota must be a no-op")

	thirdCovers := snapshotCovers(t, ctx, db, "2024-01-15", "2024-01-19")
	require.Equal(t, firstCovers, thirdCovers,
		"third reassign must produce identical covers to the first")
}

// TestReassignCovers_NewLeaveBetweenRunsIsPickedUp verifies that a
// leave added between two reassign runs is processed by the second
// run. The reassign reprocesses all leaves in chronological order
// starting from a fresh in-memory seed, so any newly-active leave
// gets a cover. The new leave is created WITHOUT calling
// HandleLeaveChange so the reassign creates the cover (the
// self-healing path).
func TestReassignCovers_NewLeaveBetweenRunsIsPickedUp(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	maintenance := NewScheduleMaintenance(db)

	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 19, 0, 0, 0, 0, time.UTC)
	require.NoError(t, NewEngine(db).GenerateSchedule(ctx, startDate, endDate))

	// First leave: Bob on Mon-Wed (Jan 15-17). Bob is the original
	// on Mon (Jan 15), so HandleLeaveChange creates a cover on
	// that day.
	leave1ID, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-15", "2024-01-17", database.LeaveTypeLeave)
	require.NoError(t, err)
	require.NoError(t, maintenance.HandleLeaveChange(ctx, leave1ID))

	// First reassign: anchors the rotation. The change count is
	// implementation-defined; what's important for this test is
	// that the first reassign's result is the baseline for the
	// second.
	first, err := maintenance.ReassignCovers(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, first.LeavesProcessed)
	_ = first

	// New leave added but NEVER processed by HandleLeaveChange. This
	// is the realistic "old record" case where a leave predates
	// the cover-assignment system, or an admin backfilled a
	// future leave via a database import.
	_, err = db.CreateLeaveRecord(ctx, aliceID, "2024-01-18", "2024-01-18", database.LeaveTypeLeave)
	require.NoError(t, err)

	// Second reassign: must process both leaves and create the
	// cover for Alice's new (unprocessed) leave. The change count
	// is 1 because Alice's leave goes from no-cover to having a
	// cover; Bob's leave is unchanged because the reassign
	// reproduces the same covers for it.
	second, err := maintenance.ReassignCovers(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, second.LeavesProcessed)
	require.Equal(t, 1, second.CoversChanged,
		"second reassign must add the cover for the new unprocessed leave")

	// Third reassign: idempotent on the now-stable data.
	third, err := maintenance.ReassignCovers(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, third.LeavesProcessed)
	require.Equal(t, 0, third.CoversChanged,
		"third reassign on the now-stable rota must be a no-op")
}

// TestReassignCovers_DoesNotDisturbAdHocState verifies that a
// reassign run does not advance the ad-hoc rotation state used by
// single-call AssignCoversForLeave. Without this decoupling, a
// periodic reassign would shift the rotation position for new
// ad-hoc leaves, changing the covers a user sees when they submit
// a new leave between reassigns.
//
// Note: when a NEW leave is added between reassigns, the rotation
// DOES advance — but it advances during the ad-hoc
// HandleLeaveChange for that new leave, not during the reassign.
// The reassign only writes its own in-memory and persisted
// reassign-anchor state.
func TestReassignCovers_DoesNotDisturbAdHocState(t *testing.T) {
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

	// Bob is the original on Tue (Jan 16) and Fri (Jan 19) in this
	// 3-person rotation. Use Jan 16 so HandleLeaveChange has a
	// non-trivial effect (advances the ad-hoc state, creates a cover).
	bobLeaveID, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-16", "2024-01-16", database.LeaveTypeLeave)
	require.NoError(t, err)
	require.NoError(t, maintenance.HandleLeaveChange(ctx, bobLeaveID))

	// Snapshot the ad-hoc state.
	adHocDate, adHocIndex, adHocErr := db.GetCoverRotationState(ctx)
	require.NoError(t, adHocErr)

	// Reassign.
	_, err = maintenance.ReassignCovers(ctx)
	require.NoError(t, err)

	// Ad-hoc state must be unchanged. The reassign only writes the
	// reassign anchor; it must leave the ad-hoc anchor (which
	// AssignCoversForLeave reads on the next ad-hoc call) alone.
	afterDate, afterIndex, afterErr := db.GetCoverRotationState(ctx)
	require.NoError(t, afterErr)
	require.True(t, adHocDate.Equal(afterDate),
		"reassign must not advance the ad-hoc last_date")
	require.Equal(t, adHocIndex, afterIndex,
		"reassign must not advance the ad-hoc last_index")

	// Sanity: the reassign anchor was written.
	anchorDate, anchorIndex, valid, err := db.GetReassignmentAnchor(ctx)
	require.NoError(t, err)
	require.True(t, valid, "reassign anchor must be populated after a reassign")
	require.NotZero(t, anchorDate, "reassign anchor date must be set")
	_ = anchorIndex
}

// TestReassignCovers_AdHocBetweenRunsShiftsCoversAsExpected documents
// the behavior when a new leave is added between two reassign
// runs. The new leave consumes a rotation slot, so the second
// reassign — which reprocesses all leaves in chronological order
// starting from index 0 — produces different covers than the
// first. This is the same behavior as adding a new leave through
// the web form: it shifts the rotation. What matters for
// idempotency is that the SECOND and THIRD reassigns produce the
// same covers (the rotation has stabilized after the new leave).
func TestReassignCovers_AdHocBetweenRunsShiftsCoversAsExpected(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)
	maintenance := NewScheduleMaintenance(db)

	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 19, 0, 0, 0, 0, time.UTC)
	require.NoError(t, engine.GenerateSchedule(ctx, startDate, endDate))

	// Bob is the original on Tue (Jan 16) and Fri (Jan 19) in this
	// 3-person rotation. Use Tue-Fri so HandleLeaveChange has
	// something to do for Bob on his original days.
	bobLeaveID, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-16", "2024-01-19", database.LeaveTypeLeave)
	require.NoError(t, err)
	require.NoError(t, maintenance.HandleLeaveChange(ctx, bobLeaveID))

	// First reassign: anchors the rotation state.
	_, err = maintenance.ReassignCovers(ctx)
	require.NoError(t, err)

	// Add a new leave via the ad-hoc path: Alice on Mon (Jan 15),
	// where she IS the original. This advances the rotation by
	// one slot (the algorithm picks a new candidate for Alice's
	// cover on Jan 15, which shifts subsequent covers).
	aliceLeaveID, err := db.CreateLeaveRecord(ctx, aliceID, "2024-01-15", "2024-01-15", database.LeaveTypeLeave)
	require.NoError(t, err)
	require.NoError(t, maintenance.HandleLeaveChange(ctx, aliceLeaveID))

	// Second reassign: Bob's covers may have shifted because the
	// reassign reprocesses from scratch starting at index 0, and
	// Alice's new leave consumes the first slot.
	_, err = maintenance.ReassignCovers(ctx)
	require.NoError(t, err)
	secondCovers := snapshotCovers(t, ctx, db, "2024-01-15", "2024-01-19")

	// Third reassign: must produce identical covers to the second
	// (the rotation has stabilized after Alice's leave).
	_, err = maintenance.ReassignCovers(ctx)
	require.NoError(t, err)
	thirdCovers := snapshotCovers(t, ctx, db, "2024-01-15", "2024-01-19")
	require.Equal(t, secondCovers, thirdCovers,
		"third reassign must produce identical covers to the second (idempotency on stable data)")
}
