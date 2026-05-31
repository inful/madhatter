package database

import (
	"context"
	"testing"
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
	db, cleanup := setupTestDB(t)
	defer cleanup()

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
}

func TestValidateSwapAssignments_TargetDatePassed_ReturnsErrSwapTargetDatePassed(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

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
	past := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	future := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	err := validateSwapAssignmentDates(past, future)
	require.ErrorIs(t, err, ErrSwapRequesterDatePassed)
}

func TestValidateSwapAssignmentDates_TargetDateInPast_ReturnsErrSwapTargetDatePassed(t *testing.T) {
	future := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	past := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	err := validateSwapAssignmentDates(future, past)
	require.ErrorIs(t, err, ErrSwapTargetDatePassed)
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
