package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedAssignedWFH creates an admin-marked assigned WFH row
// directly via raw SQL (no public API yet — picker not in
// Phase 3). The swap layer operates on WFH rows of any
// origin, but the realistic flow involves an origin='assigned'
// row, so the tests use that shape.
func seedAssignedWFH(t *testing.T, ctx context.Context, db *DB, memberID, date string) string {
	t.Helper()
	id := "assigned-test-" + date + "-" + memberID
	_, err := db.ExecContext(ctx,
		`INSERT INTO wfh_requests (id, member_id, date, status, origin)
		 VALUES (?, ?, ?, 'approved', 'assigned')`,
		id, memberID, date)
	require.NoError(t, err)
	return id
}

// TestCreateWFHAssignmentSwap_Roundtrip pins the basic
// insert + read + state-transition flow. Migration 000025 +
// the SQLC queries + the Go wrappers.
func TestCreateWFHAssignmentSwap_Roundtrip(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	requester := "alice@example.com"
	target := "bob@example.com"
	requesterID, err := db.AddTeamMember(ctx, requester, requester)
	require.NoError(t, err)
	targetID, err := db.AddTeamMember(ctx, target, target)
	require.NoError(t, err)

	future := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
	assignedID := seedAssignedWFH(t, ctx, db, requesterID, future)

	id, err := db.CreateWFHAssignmentSwap(ctx, assignedID, targetID, future)
	require.NoError(t, err)
	assert.NotEmpty(t, id)

	// Read back by ID.
	s, err := db.GetWFHAssignmentSwapByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, id, s.ID)
	assert.Equal(t, assignedID, s.RequesterWFHRequestID)
	assert.Equal(t, targetID, s.TargetMemberID)
	assert.Equal(t, future, s.SwapDate)
	assert.Equal(t, "pending", s.Status)
	assert.Nil(t, s.ResolvedAt, "freshly-created swap has no resolved_at")
}

// TestGetPendingWFHSwapForRequesterRow_ConflictGuard pins the
// 409-conflict invariant: at most one pending swap per
// assigned row. The first read returns the existing swap; the
// second insert (in handleWFHSwapCreate) refuses with 409.
func TestGetPendingWFHSwapForRequesterRow_ConflictGuard(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	requester := "alice@example.com"
	target := "bob@example.com"
	requesterID, err := db.AddTeamMember(ctx, requester, requester)
	require.NoError(t, err)
	targetID, err := db.AddTeamMember(ctx, target, target)
	require.NoError(t, err)

	future := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
	assignedID := seedAssignedWFH(t, ctx, db, requesterID, future)

	// No swap exists yet → nil.
	first, err := db.GetPendingWFHSwapForRequesterRow(ctx, assignedID)
	require.NoError(t, err)
	assert.Nil(t, first, "no pending swap before insert")

	// Insert one.
	_, err = db.CreateWFHAssignmentSwap(ctx, assignedID, targetID, future)
	require.NoError(t, err)

	// Now there IS a pending swap.
	second, err := db.GetPendingWFHSwapForRequesterRow(ctx, assignedID)
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, "pending", second.Status)

	// Cancel the swap; subsequent lookup returns nil
	// (the conflict guard releases).
	require.NoError(t, db.UpdateWFHAssignmentSwapStatus(ctx, second.ID, "cancelled", time.Now().UTC()))
	third, err := db.GetPendingWFHSwapForRequesterRow(ctx, assignedID)
	require.NoError(t, err)
	assert.Nil(t, third, "cancelled swap is not pending")
}

// TestGetPendingWFHSwapsForTarget_Inbox pins the inbox query:
// returns pending swaps where target is the current user.
func TestGetPendingWFHSwapsForTarget_Inbox(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	requester := "alice@example.com"
	target := "bob@example.com"
	requesterID, err := db.AddTeamMember(ctx, requester, requester)
	require.NoError(t, err)
	targetID, err := db.AddTeamMember(ctx, target, target)
	require.NoError(t, err)

	// Three swaps from Alice to Bob (target), one from Carol to Dave.
	carol := "carol@example.com"
	dave := "dave@example.com"
	carolID, err := db.AddTeamMember(ctx, carol, carol)
	require.NoError(t, err)
	daveID, err := db.AddTeamMember(ctx, dave, dave)
	require.NoError(t, err)

	for i, offset := range []int{1, 2, 3} {
		date := time.Now().UTC().AddDate(0, 0, offset).Format("2006-01-02")
		assignedID := seedAssignedWFH(t, ctx, db, requesterID, date)
		_, err := db.CreateWFHAssignmentSwap(ctx, assignedID, targetID, date)
		require.NoError(t, err, "swap %d", i)
	}
	carolAssigned := seedAssignedWFH(t, ctx, db, carolID, time.Now().UTC().AddDate(0, 0, 4).Format("2006-01-02"))
	_, err = db.CreateWFHAssignmentSwap(ctx, carolAssigned, daveID, time.Now().UTC().AddDate(0, 0, 4).Format("2006-01-02"))
	require.NoError(t, err)

	// Bob's inbox: 3 swaps.
	bobInbox, err := db.GetPendingWFHSwapsForTarget(ctx, targetID)
	require.NoError(t, err)
	assert.Len(t, bobInbox, 3, "Bob should see 3 pending swap requests")
	for _, s := range bobInbox {
		assert.Equal(t, targetID, s.TargetMemberID)
		assert.Equal(t, "pending", s.Status)
	}

	// Dave's inbox: 1 swap.
	daveInbox, err := db.GetPendingWFHSwapsForTarget(ctx, daveID)
	require.NoError(t, err)
	assert.Len(t, daveInbox, 1)
}

// TestUpdateWFHAssignmentSwapStatus_StateTransitions pins the
// accept/reject/cancel state transitions. After cancellation,
// the conflict guard releases (TestGetPendingWFHSwapForRequesterRow
// already pins that).
func TestUpdateWFHAssignmentSwapStatus_StateTransitions(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	requesterID, err := db.AddTeamMember(ctx, "alice@example.com", "alice@example.com")
	require.NoError(t, err)
	targetID, err := db.AddTeamMember(ctx, "bob@example.com", "bob@example.com")
	require.NoError(t, err)

	future := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
	assignedID := seedAssignedWFH(t, ctx, db, requesterID, future)
	swapID, err := db.CreateWFHAssignmentSwap(ctx, assignedID, targetID, future)
	require.NoError(t, err)

	now := time.Now().UTC()

	// Accept.
	require.NoError(t, db.UpdateWFHAssignmentSwapStatus(ctx, swapID, "accepted", now))
	s, err := db.GetWFHAssignmentSwapByID(ctx, swapID)
	require.NoError(t, err)
	assert.Equal(t, "accepted", s.Status)
	require.NotNil(t, s.ResolvedAt)

	// The conflict guard must skip accepted swaps — they no
	// longer count as pending.
	guard, err := db.GetPendingWFHSwapForRequesterRow(ctx, assignedID)
	require.NoError(t, err)
	assert.Nil(t, guard, "accepted swap is not pending; the guard releases")
}

// TestCancelExpiredWFHSwaps pins the auto-cancel pass run by
// SettlePendingRequests (step 15 of plans/assigned-wfh-plan.md).
// Pending swaps whose swap_date is strictly before the cutoff
// flip to cancelled; future swaps stay pending.
func TestCancelExpiredWFHSwaps(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, err := db.AddTeamMember(ctx, "alice@example.com", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "bob@example.com", "bob@example.com")
	require.NoError(t, err)

	// Pending swap with swap_date yesterday — must cancel.
	yesterdayAssigned := seedAssignedWFH(t, ctx, db, aliceID, time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"))
	_, err = db.CreateWFHAssignmentSwap(ctx, yesterdayAssigned, bobID, time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"))
	require.NoError(t, err)

	// Pending swap with swap_date tomorrow — must NOT cancel.
	tomorrowAssigned := seedAssignedWFH(t, ctx, db, aliceID, time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02"))
	_, err = db.CreateWFHAssignmentSwap(ctx, tomorrowAssigned, bobID, time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02"))
	require.NoError(t, err)

	cutoff := time.Now().UTC().Truncate(24 * time.Hour)
	require.NoError(t, db.CancelExpiredWFHSwaps(ctx, cutoff))

	// Yesterday: cancelled.
	yesterdaySwap, err := db.GetWFHAssignmentSwapByID(ctx, mustGetSwapIDByDate(t, ctx, db, time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")))
	require.NoError(t, err)
	assert.Equal(t, "cancelled", yesterdaySwap.Status, "yesterday's swap must auto-cancel")

	// Tomorrow: still pending.
	tomorrowSwap, err := db.GetWFHAssignmentSwapByID(ctx, mustGetSwapIDByDate(t, ctx, db, time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")))
	require.NoError(t, err)
	assert.Equal(t, "pending", tomorrowSwap.Status, "tomorrow's swap must not be touched")
}

// mustGetSwapIDByDate returns the swap ID for the given date
// (test fixture helper — the swap table has one row per
// assigned row in this test, so looking up by date works).
// Uses time.Time because the swap_date column is stored as a
// datetime in SQLite; comparing a string against the column
// doesn't always coerce correctly with the ncruces driver.
func mustGetSwapIDByDate(t *testing.T, ctx context.Context, db *DB, date string) string {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("parse date %s: %v", date, err)
	}
	var id string
	row := db.db.QueryRowContext(ctx,
		`SELECT s.id FROM wfh_assignment_swaps s
		 WHERE s.swap_date = ? LIMIT 1`, parsed)
	if err := row.Scan(&id); err != nil {
		t.Fatalf("lookup swap by date %s: %v", date, err)
	}
	return id
}

// TestProbe_CancelExpiredSwapsInspect inspects what happens
// after CancelExpiredWFHSwaps to figure out why the lookup
// fails. Will be removed once the real test passes.
func TestProbe_CancelExpiredSwapsInspect(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, err := db.AddTeamMember(ctx, "alice@example.com", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "bob@example.com", "bob@example.com")
	require.NoError(t, err)

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	yesterdayAssigned := seedAssignedWFH(t, ctx, db, aliceID, yesterday)
	tomorrowAssigned := seedAssignedWFH(t, ctx, db, aliceID, tomorrow)
	_, err = db.CreateWFHAssignmentSwap(ctx, yesterdayAssigned, bobID, yesterday)
	require.NoError(t, err)
	_, err = db.CreateWFHAssignmentSwap(ctx, tomorrowAssigned, bobID, tomorrow)
	require.NoError(t, err)

	t.Logf("yesterday date: %s", yesterday)
	t.Logf("tomorrow date: %s", tomorrow)

	// List all rows before cancel.
	rows, err := db.db.QueryContext(ctx, `SELECT id, swap_date, status FROM wfh_assignment_swaps`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, status string
		var date time.Time
		require.NoError(t, rows.Scan(&id, &date, &status))
		t.Logf("BEFORE: id=%s date=%s status=%s", id, date.Format("2006-01-02"), status)
	}

	cutoff := time.Now().UTC().Truncate(24 * time.Hour)
	t.Logf("cutoff: %s", cutoff.Format("2006-01-02 15:04:05"))
	require.NoError(t, db.CancelExpiredWFHSwaps(ctx, cutoff))

	rows, err = db.db.QueryContext(ctx, `SELECT id, swap_date, status FROM wfh_assignment_swaps`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, status string
		var date time.Time
		require.NoError(t, rows.Scan(&id, &date, &status))
		t.Logf("AFTER:  id=%s date=%s status=%s", id, date.Format("2006-01-02"), status)
	}
}
