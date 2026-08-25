package rota

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/require"
)

// TestR2_RecordsActualCover_NotCandidate is the regression test
// for the bug where the cover rotation state recorded the
// algorithm's *candidate* slot rather than the slot of the
// person who actually covered. When the candidate was on leave
// and findCover fell through to the next non-on-leave member,
// the state was out of sync with the actual cover, and the
// next call could land on the same person who covered
// yesterday.
//
// The user's invariant: "never pick the same cover from R2
// twice in a row (unless there is only one person available in
// R2)".
//
// Setup: 5 members [Alice, Bob, Charlie, Dave, Eve] in
// alphabetical order (slots 0-4). Charlie (slot 2) is the
// rotation candidate on day 1 and is on leave. Dave (slot 3)
// is the next non-on-leave member and actually covers day 1.
//
// Under the bug: state records slot 2 (the candidate). Day 2
// advances from slot 2 by 1 = slot 3 = Dave. Dave covers
// again. Two days in a row.
//
// Under the fix: state records slot 3 (the actual cover).
// Day 2 advances from slot 3 by 1 = slot 4 = Eve. Eve covers
// day 2. Not Dave.
//
// To force the R1 (the person normally on rota) to be on leave
// (so a cover is actually needed), Alice and Bob are on leave
// for both days. R1 is Alice on Mon, Bob on Tue.
func TestR2_RecordsActualCover_NotCandidate(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	charlieID, err := db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)
	daveID, err := db.AddTeamMember(ctx, "Dave", "dave@example.com")
	require.NoError(t, err)
	eveID, err := db.AddTeamMember(ctx, "Eve", "eve@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)
	maintenance := NewScheduleMaintenance(db)

	// Two business days: Mon Jan 15 and Tue Jan 16, 2024.
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC)
	require.NoError(t, engine.GenerateSchedule(ctx, startDate, endDate))

	// R1s on leave (so a cover is needed), plus Charlie (the R2
	// candidate) on leave. Dave and Eve are available to cover.
	for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		for _, mid := range []string{aliceID, bobID, charlieID} {
			_, err = db.CreateLeaveRecord(ctx, mid, dateStr, dateStr, database.LeaveTypeLeave)
			require.NoError(t, err)
		}
	}

	leaves, err := db.GetLeaveRecords(ctx)
	require.NoError(t, err)
	// Sort by start_date ASC: the per-leave web handler path
	// processes one leave at a time and the state always moves
	// forward, so the chronological order is implicit. This test
	// loop processes multiple leaves in one batch, so it must
	// walk them in chronological order explicitly — the same
	// order the per-leave path would process them in.
	//
	// Note: ReassignCovers (run at server startup) iterates
	// leaves in start_date DESC order and relies on the
	// walk-backward branch of computeCoverRotationIndex to avoid
	// corrupting forward-progressed state. That branch does not
	// write, so processing leaves out of order is safe — it just
	// means the state only reflects the latest-leave date in
	// the batch. The web-handler path (which is the one this
	// fix targets) is the ASC-order path.
	sort.Slice(leaves, func(i, j int) bool {
		return leaves[i].StartDate.Before(leaves[j].StartDate)
	})
	for _, l := range leaves {
		require.NoError(t, maintenance.HandleLeaveChange(ctx, l.ID))
	}

	// Day 1 (Mon Jan 15): R1 = Alice, on leave. R2 candidate =
	// slot 2 (Charlie). Charlie is on leave. findCover walks to
	// slot 3 (Dave). Dave is not on leave, so Dave covers.
	mon15Cover := getCoverMemberID(t, ctx, db, "2024-01-15")
	require.Equal(t, daveID, mon15Cover,
		"day 1: R2 candidate (Charlie) is on leave, so findCover should walk to Dave")

	// Day 2 (Tue Jan 16): R1 = Bob, on leave. Under the fix, the
	// state for day 2 starts at slot 3 (Dave, day 1's actual
	// cover). Advancing by 1 working day lands on slot 4 (Eve).
	// Eve is not on leave, so Eve covers — NOT Dave.
	//
	// Under the bug, the state for day 2 starts at slot 2 (the
	// day-1 candidate, NOT the actual cover). Advancing by 1
	// working day lands on slot 3 (Dave). Dave covers day 2
	// too — the same person twice in a row, which the user
	// said must not happen.
	tue16Cover := getCoverMemberID(t, ctx, db, "2024-01-16")
	require.Equal(t, eveID, tue16Cover,
		"day 2: state should advance from day 1's actual cover "+
			"(Dave, slot 3) by one slot to Eve (slot 4), not stay on Dave")
	require.NotEqual(t, mon15Cover, tue16Cover,
		"the same person must not cover two consecutive business days")
}

// TestR2_OnlyOnePersonAvailable_FallsBackToSameSlot covers the
// explicit exception in the user's invariant: when every team
// member except one is on leave, the same person has to cover
// every day. The state should record that person's slot every
// time (so the state doesn't drift), and findCover should
// return that person on every call.
//
// Setup: 3 members [Alice, Bob, Charlie]. Alice is the R1
// every day (R1 advanced through a single-day window would
// cycle to Bob, but we only use a single day here so Alice is
// always R1). Alice and Charlie on leave. Bob is the only
// available cover.
func TestR2_OnlyOnePersonAvailable_FallsBackToSameSlot(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	charlieID, err := db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)
	maintenance := NewScheduleMaintenance(db)

	// Single business day to keep the R1 stable on Alice.
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	require.NoError(t, engine.GenerateSchedule(ctx, startDate, endDate))

	// Alice (R1) is on leave. Charlie (the R2 candidate, slot 2)
	// is on leave. Bob is the only available cover.
	dateStr := "2024-01-15"
	for _, mid := range []string{aliceID, charlieID} {
		_, err = db.CreateLeaveRecord(ctx, mid, dateStr, dateStr, database.LeaveTypeLeave)
		require.NoError(t, err)
	}

	leaves, err := db.GetLeaveRecords(ctx)
	require.NoError(t, err)
	sort.Slice(leaves, func(i, j int) bool {
		return leaves[i].StartDate.Before(leaves[j].StartDate)
	})
	for _, l := range leaves {
		require.NoError(t, maintenance.HandleLeaveChange(ctx, l.ID))
	}

	cover := getCoverMemberID(t, ctx, db, dateStr)
	require.Equal(t, bobID, cover,
		"with only one available cover, that person must be picked")
}
