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
