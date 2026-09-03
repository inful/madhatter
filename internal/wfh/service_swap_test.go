package wfh

import (
	"context"
	"testing"
	"time"

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
