package wfh

import (
	"context"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestService_AutoCancelExpiredSwaps pins step 15 of
// plans/assigned-wfh-plan.md: SettlePendingRequests calls
// AutoCancelExpiredSwaps after each tick. Pending swaps whose
// swap_date is strictly before today are flipped to
// 'cancelled' (so the conflict guard releases). Future swaps
// are untouched.
func TestService_AutoCancelExpiredSwaps(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "alice@example.com", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "bob@example.com", "bob@example.com")
	require.NoError(t, err)

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")

	// Insert the assigned rows directly via raw SQL. Avoids
	// the date guard in CreateWFHRequest (which rejects
	// past dates). Origin='assigned' matches what the picker
	// would have inserted; we don't need a real picker run
	// for this test — only the auto-cancel pass.
	_, err = db.ExecContext(ctx,
		`INSERT INTO wfh_requests (id, member_id, date, status, origin)
		 VALUES (?, ?, ?, 'approved', 'assigned')`,
		"assigned-yesterday", aliceID, yesterday)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO wfh_requests (id, member_id, date, status, origin)
		 VALUES (?, ?, ?, 'approved', 'assigned')`,
		"assigned-tomorrow", aliceID, tomorrow)
	require.NoError(t, err)

	yesterdaySwapID, err := db.CreateWFHAssignmentSwap(ctx, "assigned-yesterday", bobID, yesterday)
	require.NoError(t, err)
	tomorrowSwapID, err := db.CreateWFHAssignmentSwap(ctx, "assigned-tomorrow", bobID, tomorrow)
	require.NoError(t, err)

	svc := NewService(db, testConfig())
	require.NoError(t, svc.AutoCancelExpiredSwaps(ctx))

	// Yesterday's swap must be cancelled.
	yesterdaySwap, err := db.GetWFHAssignmentSwapByID(ctx, yesterdaySwapID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", yesterdaySwap.Status,
		"yesterday's pending swap must auto-cancel")

	// Tomorrow's swap must remain pending.
	tomorrowSwap, err := db.GetWFHAssignmentSwapByID(ctx, tomorrowSwapID)
	require.NoError(t, err)
	assert.Equal(t, "pending", tomorrowSwap.Status,
		"tomorrow's swap must not be touched")

	// The target's inbox still has the future swap pending;
	// the past swap was auto-cancelled.
	pending, err := db.GetPendingWFHSwapsForTarget(ctx, bobID)
	require.NoError(t, err)
	require.Len(t, pending, 1, "future swap should still be pending")
	assert.Equal(t, tomorrowSwapID, pending[0].ID,
		"only the future swap should remain in the inbox")
}

// TestService_AdminReassignWFH pins step 16 of
// plans/assigned-wfh-plan.md. The admin moves an assigned
// WFH from one member to another: the original row flips to
// status='withdrawn', and a new row with origin='assigned'
// lands for the replacement. The cap is preserved (1 out, 1 in).
func TestService_AdminReassignWFH(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "alice@example.com", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "bob@example.com", "bob@example.com")
	require.NoError(t, err)
	// Seed a real admin user so the FK on wfh_requests.withdrawn_by
	// is satisfied (the schema references users(id)). The fixture
	// matches what the production handler passes through.
	adminID := seedAdminUser(t, ctx, db, "admin-reassign", "Admin", "admin-reassign@example.com")

	date := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
	assignedID := "assigned-reassign-test"
	_, err = db.ExecContext(ctx,
		`INSERT INTO wfh_requests (id, member_id, date, status, origin)
		 VALUES (?, ?, ?, 'approved', 'assigned')`,
		assignedID, aliceID, date)
	require.NoError(t, err)

	svc := NewService(db, testConfig())
	newID, err := svc.AdminReassignWFH(ctx, assignedID, bobID, adminID, "Admin")
	require.NoError(t, err)
	assert.NotEmpty(t, newID, "reassignment should return the new row ID")

	// Original: status=withdrawn, withdrawn_by=reassign:<admin>
	original, err := db.GetWFHRequestByID(ctx, assignedID)
	require.NoError(t, err)
	assert.Equal(t, "withdrawn", original.Status)
	require.NotNil(t, original.WithdrawnBy)
	assert.Equal(t, adminID, *original.WithdrawnBy)

	// Replacement: a new row for Bob on the same date
	replacement, err := db.GetWFHRequestByMemberAndDate(ctx, bobID, date)
	require.NoError(t, err)
	require.NotNil(t, replacement, "replacement row must exist for Bob on the date")
	assert.Equal(t, "approved", replacement.Status)
	assert.Equal(t, "assigned", replacement.Origin)
	assert.NotEqual(t, assignedID, replacement.ID, "replacement must be a new row, not reuse the original")
}

// TestService_AdminReassignWFH_RejectsVoluntary pins the
// origin guard: only origin='assigned' can be reassigned. A
// voluntary (ad_hoc) row returns ErrWFHAssigned — "use a swap
// instead" (the same sentinel the withdraw gate uses).
func TestService_AdminReassignWFH_RejectsVoluntary(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "alice@example.com", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "bob@example.com", "bob@example.com")
	require.NoError(t, err)
	adminID := seedAdminUser(t, ctx, db, "admin-reassign-2", "Admin", "admin-reassign-2@example.com")

	date := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
	voluntary, err := db.CreateWFHRequest(ctx, aliceID, date)
	require.NoError(t, err)

	svc := NewService(db, testConfig())
	_, err = svc.AdminReassignWFH(ctx, voluntary.ID, bobID, adminID, "Admin")
	require.Error(t, err)
	assert.ErrorIs(t, err, database.ErrWFHAssigned,
		"voluntary (ad_hoc) row must not be reassignable")
}
