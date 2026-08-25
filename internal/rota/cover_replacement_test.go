package rota

import (
	"context"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/require"
)

// TestEngine_CoverReplacement tests that when a cover person takes leave,
// their cover assignment is updated to point to a new person.
func TestEngine_CoverReplacement(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Setup: Alice, Bob, Charlie
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	charlieID, err := db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	// Generate schedule: Monday Jan 15 = Alice
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	err = engine.GenerateSchedule(ctx, startDate, endDate)
	require.NoError(t, err)

	// Verify Alice is scheduled
	assignments, err := db.GetAssignmentsByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	require.Equal(t, aliceID, assignments[0].MemberID)
	require.False(t, assignments[0].IsCover)

	// Alice takes leave -> Bob should cover
	aliceLeaveID, err := db.CreateLeaveRecord(ctx, aliceID, "2024-01-15", "2024-01-15", database.LeaveTypeLeave)
	require.NoError(t, err)
	err = engine.AssignCoversForLeave(ctx, aliceLeaveID)
	require.NoError(t, err)

	// Verify Bob is covering for Alice
	assignments, err = db.GetAssignmentsByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Len(t, assignments, 2)

	var original, cover *string
	for _, a := range assignments {
		if a.IsCover {
			cover = &a.MemberID
		} else {
			original = &a.MemberID
		}
	}
	require.NotNil(t, original)
	require.NotNil(t, cover)
	require.Equal(t, aliceID, *original, "Alice should still be the original assignment")
	require.Equal(t, bobID, *cover, "Bob should be covering")

	// Now Bob takes leave -> Charlie should replace Bob as cover
	bobLeaveID, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-15", "2024-01-15", database.LeaveTypeLeave)
	require.NoError(t, err)
	err = engine.AssignCoversForLeave(ctx, bobLeaveID)
	require.NoError(t, err)

	// Verify Charlie is now covering (Bob was replaced)
	assignments, err = db.GetAssignmentsByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Len(t, assignments, 2, "Should still have 2 assignments: original + cover")

	original = nil
	cover = nil
	for _, a := range assignments {
		if a.IsCover {
			cover = &a.MemberID
		} else {
			original = &a.MemberID
		}
	}
	require.NotNil(t, original)
	require.NotNil(t, cover)
	require.Equal(t, aliceID, *original, "Alice should still be the original assignment")
	require.Equal(t, charlieID, *cover, "Charlie should now be covering (replacing Bob)")
}

// TestEngine_CoverReplacement_MultiDay tests cover replacement across multiple days.
func TestEngine_CoverReplacement_MultiDay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	aliceID, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	bobID, _ := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	charlieID, _ := db.AddTeamMember(ctx, "Charlie", "charlie@example.com")

	engine := NewEngine(db)

	// Manually create schedule where Alice is assigned Mon-Wed
	// (not using GenerateSchedule which does round-robin)
	_, err := db.CreateRotaAssignment(ctx, "2024-01-15", aliceID, false, nil) // Monday
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, "2024-01-16", aliceID, false, nil) // Tuesday
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, "2024-01-17", aliceID, false, nil) // Wednesday
	require.NoError(t, err)

	// Alice takes leave Mon-Wed -> Bob should cover all three days
	aliceLeaveID, _ := db.CreateLeaveRecord(ctx, aliceID, "2024-01-15", "2024-01-17", database.LeaveTypeLeave)
	err = engine.AssignCoversForLeave(ctx, aliceLeaveID)
	require.NoError(t, err)

	// Verify Bob is covering Monday
	mondayAssignments, _ := db.GetAssignmentsByDate(ctx, "2024-01-15")
	var mondayCover string
	for _, a := range mondayAssignments {
		if a.IsCover {
			mondayCover = a.MemberID
		}
	}
	require.Equal(t, bobID, mondayCover)

	// Bob takes leave on Wednesday -> Charlie should replace Bob on Wednesday only
	bobLeaveID, _ := db.CreateLeaveRecord(ctx, bobID, "2024-01-17", "2024-01-17", database.LeaveTypeLeave)
	err = engine.AssignCoversForLeave(ctx, bobLeaveID)
	require.NoError(t, err)

	// Monday should still have Bob covering
	mondayAssignments, _ = db.GetAssignmentsByDate(ctx, "2024-01-15")
	mondayCover = ""
	for _, a := range mondayAssignments {
		if a.IsCover {
			mondayCover = a.MemberID
		}
	}
	require.Equal(t, bobID, mondayCover, "Bob should still cover Monday")

	// Wednesday should have Charlie covering (replaced Bob)
	wedAssignments, _ := db.GetAssignmentsByDate(ctx, "2024-01-17")
	var wedCover string
	for _, a := range wedAssignments {
		if a.IsCover {
			wedCover = a.MemberID
		}
	}
	require.Equal(t, charlieID, wedCover, "Charlie should cover Wednesday (replacing Bob)")
}
