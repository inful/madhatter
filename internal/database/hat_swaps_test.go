package database

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateHatSwap_RejectsConflictingPendingAssignmentAcrossColumns(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	charlieID, err := db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	baseDate := time.Now().AddDate(0, 0, 7)
	aliceAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)
	bobAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 1).Format("2006-01-02"), bobID, false, nil)
	require.NoError(t, err)
	charlieAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 2).Format("2006-01-02"), charlieID, false, nil)
	require.NoError(t, err)

	_, err = db.CreateHatSwap(ctx, aliceAssignmentID, bobAssignmentID, aliceID, bobID)
	require.NoError(t, err)

	_, err = db.CreateHatSwap(ctx, charlieAssignmentID, aliceAssignmentID, charlieID, aliceID)
	require.Error(t, err)
}

func TestExecuteSwap_RejectsPastAssignments(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	baseDate := time.Now().AddDate(0, 0, -3)
	aliceAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)
	bobAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 1).Format("2006-01-02"), bobID, false, nil)
	require.NoError(t, err)

	swapID, err := db.CreateHatSwap(ctx, aliceAssignmentID, bobAssignmentID, aliceID, bobID)
	require.NoError(t, err)

	err = db.ExecuteSwap(ctx, swapID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "passed")

	aliceAssignment, err := db.GetAssignmentByID(ctx, aliceAssignmentID)
	require.NoError(t, err)
	assert.Equal(t, aliceID, aliceAssignment.MemberID)

	bobAssignment, err := db.GetAssignmentByID(ctx, bobAssignmentID)
	require.NoError(t, err)
	assert.Equal(t, bobID, bobAssignment.MemberID)
}

func TestExecuteSwap_SwapsMembersAndSetsIsSwapped(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	baseDate := time.Now().AddDate(0, 0, 7)
	aliceAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)
	bobAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 1).Format("2006-01-02"), bobID, false, nil)
	require.NoError(t, err)

	swapID, err := db.CreateHatSwap(ctx, aliceAssignmentID, bobAssignmentID, aliceID, bobID)
	require.NoError(t, err)

	err = db.ExecuteSwap(ctx, swapID)
	require.NoError(t, err)

	aliceAssignment, err := db.GetAssignmentByID(ctx, aliceAssignmentID)
	require.NoError(t, err)
	assert.Equal(t, bobID, aliceAssignment.MemberID)
	assert.True(t, aliceAssignment.IsSwapped)

	bobAssignment, err := db.GetAssignmentByID(ctx, bobAssignmentID)
	require.NoError(t, err)
	assert.Equal(t, aliceID, bobAssignment.MemberID)
	assert.True(t, bobAssignment.IsSwapped)

	swap, err := db.GetHatSwapByID(ctx, swapID)
	require.NoError(t, err)
	assert.Equal(t, SwapStatusAccepted, swap.Status)
}

func TestCleanupExpiredPendingSwaps_CancelsExpiredPending(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	pastDate := time.Now().AddDate(0, 0, -3)
	futureDate := time.Now().AddDate(0, 0, 7)

	alicePastID, err := db.CreateRotaAssignment(ctx, pastDate.Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)
	bobFutureID, err := db.CreateRotaAssignment(ctx, futureDate.Format("2006-01-02"), bobID, false, nil)
	require.NoError(t, err)

	swapID, err := db.CreateHatSwap(ctx, alicePastID, bobFutureID, aliceID, bobID)
	require.NoError(t, err)

	affected, err := db.CleanupExpiredPendingSwaps(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected)

	swap, err := db.GetHatSwapByID(ctx, swapID)
	require.NoError(t, err)
	assert.Equal(t, SwapStatusCancelled, swap.Status)
}

func TestExecuteSwap_DoesNotMarkUnrelatedAssignments(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	charlieID, err := db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	baseDate := time.Now().AddDate(0, 0, 7)
	aliceAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)
	bobAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 1).Format("2006-01-02"), bobID, false, nil)
	require.NoError(t, err)
	charlieAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 2).Format("2006-01-02"), charlieID, false, nil)
	require.NoError(t, err)

	swapID, err := db.CreateHatSwap(ctx, aliceAssignmentID, bobAssignmentID, aliceID, bobID)
	require.NoError(t, err)

	err = db.ExecuteSwap(ctx, swapID)
	require.NoError(t, err)

	charlieAssignment, err := db.GetAssignmentByID(ctx, charlieAssignmentID)
	require.NoError(t, err)
	assert.False(t, charlieAssignment.IsSwapped)
	assert.Equal(t, charlieID, charlieAssignment.MemberID)
}

// ---------------------------------------------------------------------------
// ValidateSwapAssignments
// ---------------------------------------------------------------------------

func TestValidateSwapAssignments_SameAssignment_ReturnsErrSwapSameAssignment(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	date := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	assignmentID, err := db.CreateRotaAssignment(ctx, date, aliceID, false, nil)
	require.NoError(t, err)

	_, _, err = db.ValidateSwapAssignments(ctx, assignmentID, assignmentID, aliceID)
	require.ErrorIs(t, err, ErrSwapSameAssignment)
}

func TestValidateSwapAssignments_RequesterNotFound_ReturnsErrRequesterAssignmentNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	date := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	bobAssignmentID, err := db.CreateRotaAssignment(ctx, date, bobID, false, nil)
	require.NoError(t, err)

	_, _, err = db.ValidateSwapAssignments(ctx, "nonexistent-id", bobAssignmentID, aliceID)
	require.ErrorIs(t, err, ErrRequesterAssignmentNotFound)
}

func TestValidateSwapAssignments_TargetNotFound_ReturnsErrTargetAssignmentNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	date := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	aliceAssignmentID, err := db.CreateRotaAssignment(ctx, date, aliceID, false, nil)
	require.NoError(t, err)

	_, _, err = db.ValidateSwapAssignments(ctx, aliceAssignmentID, "nonexistent-id", aliceID)
	require.ErrorIs(t, err, ErrTargetAssignmentNotFound)
}

func TestValidateSwapAssignments_NotOwner_ReturnsErrSwapNotOwner(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	charlieID, err := db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	baseDate := time.Now().AddDate(0, 0, 7)
	aliceAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)
	bobAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 1).Format("2006-01-02"), bobID, false, nil)
	require.NoError(t, err)

	// Charlie tries to swap Alice's assignment
	_, _, err = db.ValidateSwapAssignments(ctx, aliceAssignmentID, bobAssignmentID, charlieID)
	require.ErrorIs(t, err, ErrSwapNotOwner)
}

func TestValidateSwapAssignments_SelfTarget_ReturnsErrSwapTargetSelf(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	baseDate := time.Now().AddDate(0, 0, 7)
	a1, err := db.CreateRotaAssignment(ctx, baseDate.Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)
	a2, err := db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 1).Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)

	_, _, err = db.ValidateSwapAssignments(ctx, a1, a2, aliceID)
	require.ErrorIs(t, err, ErrSwapTargetSelf)
}

func TestValidateSwapAssignments_RequesterDatePassed_ReturnsErrSwapRequesterDatePassed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db, cleanup := setupTestDB(t)
		t.Cleanup(cleanup)

		ctx := context.Background()
		aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
		require.NoError(t, err)
		bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
		require.NoError(t, err)

		pastDate := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
		futureDate := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
		aliceAssignmentID, err := db.CreateRotaAssignment(ctx, pastDate, aliceID, false, nil)
		require.NoError(t, err)
		bobAssignmentID, err := db.CreateRotaAssignment(ctx, futureDate, bobID, false, nil)
		require.NoError(t, err)

		_, _, err = db.ValidateSwapAssignments(ctx, aliceAssignmentID, bobAssignmentID, aliceID)
		require.ErrorIs(t, err, ErrSwapRequesterDatePassed)
	})
}

func TestValidateSwapAssignments_TargetDatePassed_ReturnsErrSwapTargetDatePassed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db, cleanup := setupTestDB(t)
		t.Cleanup(cleanup)

		ctx := context.Background()
		aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
		require.NoError(t, err)
		bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
		require.NoError(t, err)

		futureDate := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
		pastDate := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
		aliceAssignmentID, err := db.CreateRotaAssignment(ctx, futureDate, aliceID, false, nil)
		require.NoError(t, err)
		bobAssignmentID, err := db.CreateRotaAssignment(ctx, pastDate, bobID, false, nil)
		require.NoError(t, err)

		_, _, err = db.ValidateSwapAssignments(ctx, aliceAssignmentID, bobAssignmentID, aliceID)
		require.ErrorIs(t, err, ErrSwapTargetDatePassed)
	})
}

func TestValidateSwapAssignments_Valid_ReturnsAssignments(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	baseDate := time.Now().AddDate(0, 0, 7)
	aliceAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)
	bobAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 1).Format("2006-01-02"), bobID, false, nil)
	require.NoError(t, err)

	reqA, tgtA, err := db.ValidateSwapAssignments(ctx, aliceAssignmentID, bobAssignmentID, aliceID)
	require.NoError(t, err)
	assert.Equal(t, aliceAssignmentID, reqA.ID)
	assert.Equal(t, bobAssignmentID, tgtA.ID)
}

// ---------------------------------------------------------------------------
// validateSwapAssignmentDates
// ---------------------------------------------------------------------------

func TestValidateSwapAssignmentDates_InvalidRequesterDate_ReturnsErrSwapRequesterDateInvalid(t *testing.T) {
	err := validateSwapAssignmentDates("not-a-date", time.Now().AddDate(0, 0, 7).Format("2006-01-02"))
	require.ErrorIs(t, err, ErrSwapRequesterDateInvalid)
}

func TestValidateSwapAssignmentDates_InvalidTargetDate_ReturnsErrSwapTargetDateInvalid(t *testing.T) {
	err := validateSwapAssignmentDates(time.Now().AddDate(0, 0, 7).Format("2006-01-02"), "not-a-date")
	require.ErrorIs(t, err, ErrSwapTargetDateInvalid)
}

func TestValidateSwapAssignmentDates_RequesterDateInPast_ReturnsErrSwapRequesterDatePassed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Use -3 days rather than -1 to keep the test robust against the
		// boundary where local time and UTC differ by a day. synctest
		// freezes time inside the bubble, so the comparison is also
		// deterministic.
		past := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
		future := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
		err := validateSwapAssignmentDates(past, future)
		require.ErrorIs(t, err, ErrSwapRequesterDatePassed)
	})
}

func TestValidateSwapAssignmentDates_TargetDateInPast_ReturnsErrSwapTargetDatePassed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		future := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
		past := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
		err := validateSwapAssignmentDates(future, past)
		require.ErrorIs(t, err, ErrSwapTargetDatePassed)
	})
}

func TestValidateSwapAssignmentDates_BothFuture_ReturnsNil(t *testing.T) {
	future1 := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	future2 := time.Now().AddDate(0, 0, 14).Format("2006-01-02")
	err := validateSwapAssignmentDates(future1, future2)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// CheckNoOpenSwaps
// ---------------------------------------------------------------------------

func TestCheckNoOpenSwaps_NoOpenSwap_ReturnsNil(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	date := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	assignmentID, err := db.CreateRotaAssignment(ctx, date, aliceID, false, nil)
	require.NoError(t, err)

	err = db.CheckNoOpenSwaps(ctx, assignmentID)
	require.NoError(t, err)
}

func TestCheckNoOpenSwaps_OpenSwapExists_ReturnsErrSwapAssignmentBusy(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	baseDate := time.Now().AddDate(0, 0, 7)
	aliceAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)
	bobAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 1).Format("2006-01-02"), bobID, false, nil)
	require.NoError(t, err)

	_, err = db.CreateHatSwap(ctx, aliceAssignmentID, bobAssignmentID, aliceID, bobID)
	require.NoError(t, err)

	err = db.CheckNoOpenSwaps(ctx, aliceAssignmentID)
	require.ErrorIs(t, err, ErrSwapAssignmentBusy)
}

func TestCheckNoOpenSwaps_SecondAssignmentBusy_ReturnsErrSwapAssignmentBusy(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	charlieID, err := db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	baseDate := time.Now().AddDate(0, 0, 7)
	aliceAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)
	bobAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 1).Format("2006-01-02"), bobID, false, nil)
	require.NoError(t, err)
	charlieAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 2).Format("2006-01-02"), charlieID, false, nil)
	require.NoError(t, err)

	// Bob's assignment has an open swap with Charlie.
	_, err = db.CreateHatSwap(ctx, bobAssignmentID, charlieAssignmentID, bobID, charlieID)
	require.NoError(t, err)

	// Alice tries a swap involving her (free) assignment and Bob's (busy) assignment.
	err = db.CheckNoOpenSwaps(ctx, aliceAssignmentID, bobAssignmentID)
	require.ErrorIs(t, err, ErrSwapAssignmentBusy)
}

// TestExecuteSwap_RejectsSelfSwapAtExecutionTime pins the defense
// against the production anomaly: a self-swap row (requester and
// target owned by the same member) was created via some non-API
// path and reached ExecuteSwap with status='pending'. The API-level
// CreateHatSwap would have rejected the row at INSERT time, but a
// future bug (or a migration that bypassed the check) could put a
// self-swap row on disk. Without this guard, ExecuteSwap would
// happily "swap" each row to the same member_id (a wasted
// transaction) and set is_swapped=1 — leaving the dashboard showing
// a false swap badge. The guard returns ErrSwapTargetSelf so the
// caller can surface the anomaly rather than silently committing
// a no-op swap.
//
// The DB-level CHECK constraint added in migration 000028 prevents
// any FUTURE self-swap INSERT. This test guards against the
// existing-data case (pre-migration or non-API-INSERTed rows) by
// re-checking at execute time, before any side-effect can land in
// the transaction.
func TestExecuteSwap_RejectsSelfSwapAtExecutionTime(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	baseDate := time.Now().AddDate(0, 0, 7)
	aliceAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)
	bobAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 1).Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)

	// Insert a self-swap row directly via SQL, bypassing CreateHatSwap
	// (the API path that has the check) AND the migration 000028
	// trigger (the storage-layer guard). We temporarily drop the
	// triggers, insert the bypass row, then recreate them. This
	// simulates a real production scenario where the self-swap row
	// somehow got into the DB — the only thing we can verify is
	// that executeSwapTx's in-transaction re-check catches it.
	_, err = db.ExecContext(ctx, `DROP TRIGGER IF EXISTS trg_hat_swaps_no_self_swap_insert`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `DROP TRIGGER IF EXISTS trg_hat_swaps_no_self_swap_update`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `
			CREATE TRIGGER IF NOT EXISTS trg_hat_swaps_no_self_swap_insert
			BEFORE INSERT ON hat_swaps
			WHEN NEW.requester_member_id = NEW.target_member_id
			BEGIN
			    SELECT RAISE(ABORT, 'hat_swaps: requester_member_id and target_member_id must differ');
			END
		`)
		_, _ = db.ExecContext(context.Background(), `
			CREATE TRIGGER IF NOT EXISTS trg_hat_swaps_no_self_swap_update
			BEFORE UPDATE OF requester_member_id, target_member_id ON hat_swaps
			WHEN NEW.requester_member_id = NEW.target_member_id
			BEGIN
			    SELECT RAISE(ABORT, 'hat_swaps: requester_member_id and target_member_id must differ');
			END
		`)
	})

	swapID := "self-swap-bypass"
	_, err = db.ExecContext(ctx, `
		INSERT INTO hat_swaps (id, requester_assignment_id, target_assignment_id,
		                     requester_member_id, target_member_id, status)
		VALUES (?, ?, ?, ?, ?, 'pending')
	`, swapID, aliceAssignmentID, bobAssignmentID, aliceID, aliceID)
	require.NoError(t, err)

	// ExecuteSwap must refuse rather than commit a no-op swap that
	// leaves is_swapped=1 set on both rows.
	err = db.ExecuteSwap(ctx, swapID)
	require.ErrorIs(t, err, ErrSwapTargetSelf)

	// Confirm the rows were NOT touched: no is_swapped=1 set, member_ids
	// unchanged.
	row1, err := db.GetAssignmentByID(ctx, aliceAssignmentID)
	require.NoError(t, err)
	assert.False(t, row1.IsSwapped, "row1 must not be marked swapped on a rejected self-swap")
	row2, err := db.GetAssignmentByID(ctx, bobAssignmentID)
	require.NoError(t, err)
	assert.False(t, row2.IsSwapped, "row2 must not be marked swapped on a rejected self-swap")
}

// TestCreateHatSwap_SelfSwapRejectedAtAPI pins the API-level
// invariant: CreateHatSwap must return ErrSwapTargetSelf when the
// requester and target member_ids are equal. Pairs with the DB-level
// CHECK constraint added in migration 000028 — the API check is
// defense in depth at the storage layer's API boundary.
func TestCreateHatSwap_SelfSwapRejectedAtAPI(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	baseDate := time.Now().AddDate(0, 0, 7)
	a1, err := db.CreateRotaAssignment(ctx, baseDate.Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)
	a2, err := db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 1).Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)

	_, err = db.CreateHatSwap(ctx, a1, a2, aliceID, aliceID)
	require.ErrorIs(t, err, ErrSwapTargetSelf)
}
