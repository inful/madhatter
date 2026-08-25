package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCreateLeaveRecord_Success(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Use dynamic dates
	startDate := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	endDate := time.Now().AddDate(0, 0, 9).Format("2006-01-02")

	// Act
	leaveID, err := db.CreateLeaveRecord(ctx, memberID, startDate, endDate, LeaveTypeLeave)

	// Assert
	require.NoError(t, err)
	require.NotEmpty(t, leaveID)

	// Verify in database
	leaves, err := db.GetLeaveByDate(ctx, startDate)
	require.NoError(t, err)
	require.Len(t, leaves, 1)
	require.Equal(t, memberID, leaves[0].MemberID)
	require.Equal(t, startDate, leaves[0].StartDate.Format("2006-01-02"))
	require.Equal(t, endDate, leaves[0].EndDate.Format("2006-01-02"))
	require.Equal(t, "pending", leaves[0].Status)
}

func TestCreateLeaveRecord_InvalidMember(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Use dynamic dates
	startDate := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	endDate := time.Now().AddDate(0, 0, 9).Format("2006-01-02")

	// Act
	_, err := db.CreateLeaveRecord(ctx, "nonexistent", startDate, endDate, LeaveTypeLeave)

	// Assert
	require.Error(t, err)
}

func TestGetLeaveByDate_Range(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")

	// Use dynamic dates
	baseDate := time.Now().AddDate(0, 0, 7)
	startDate := baseDate.Format("2006-01-02")
	middleDate := baseDate.AddDate(0, 0, 1).Format("2006-01-02")
	endDate := baseDate.AddDate(0, 0, 2).Format("2006-01-02")
	beforeDate := baseDate.AddDate(0, 0, -1).Format("2006-01-02")
	afterDate := baseDate.AddDate(0, 0, 3).Format("2006-01-02")

	_, _ = db.CreateLeaveRecord(ctx, memberID, startDate, endDate, LeaveTypeLeave)

	// Act & Assert - Should find leave on start date
	leaves, err := db.GetLeaveByDate(ctx, startDate)
	require.NoError(t, err)
	require.Len(t, leaves, 1)

	// Should find leave on end date
	leaves, err = db.GetLeaveByDate(ctx, endDate)
	require.NoError(t, err)
	require.Len(t, leaves, 1)

	// Should find leave in middle of range
	leaves, err = db.GetLeaveByDate(ctx, middleDate)
	require.NoError(t, err)
	require.Len(t, leaves, 1)

	// Should NOT find leave outside range
	leaves, err = db.GetLeaveByDate(ctx, beforeDate)
	require.NoError(t, err)
	require.Empty(t, leaves)

	leaves, err = db.GetLeaveByDate(ctx, afterDate)
	require.NoError(t, err)
	require.Empty(t, leaves)
}

func TestGetLeaveByDate_MultipleMembers(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	member1, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	member2, _ := db.AddTeamMember(ctx, "Bob", "bob@example.com")

	// Use dynamic dates
	baseDate := time.Now().AddDate(0, 0, 7)
	aliceLeaveStart := baseDate.Format("2006-01-02")
	aliceLeaveEnd := baseDate.AddDate(0, 0, 1).Format("2006-01-02")
	bobLeaveStart := aliceLeaveEnd
	bobLeaveEnd := baseDate.AddDate(0, 0, 2).Format("2006-01-02")

	_, _ = db.CreateLeaveRecord(ctx, member1, aliceLeaveStart, aliceLeaveEnd, LeaveTypeLeave)
	_, _ = db.CreateLeaveRecord(ctx, member2, bobLeaveStart, bobLeaveEnd, LeaveTypeLeave)

	// Act - query for the overlapping date (Alice's end date = Bob's start date)
	leaves, err := db.GetLeaveByDate(ctx, aliceLeaveEnd)

	// Assert
	require.NoError(t, err)
	require.Len(t, leaves, 2)
}

func TestUpdateLeaveStatus(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")

	// Use dynamic dates
	startDate := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	endDate := time.Now().AddDate(0, 0, 9).Format("2006-01-02")

	leaveID, _ := db.CreateLeaveRecord(ctx, memberID, startDate, endDate, LeaveTypeLeave)

	// Act
	err := db.UpdateLeaveStatus(ctx, leaveID, "assigned")

	// Assert
	require.NoError(t, err)

	leaves, _ := db.GetLeaveByDate(ctx, startDate)
	require.Equal(t, "assigned", leaves[0].Status)
}

func TestGetLeaveByID(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")

	// Use dynamic dates
	startDate := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	endDate := time.Now().AddDate(0, 0, 9).Format("2006-01-02")

	leaveID, _ := db.CreateLeaveRecord(ctx, memberID, startDate, endDate, LeaveTypeLeave)

	// Act
	leave, err := db.GetLeaveByID(ctx, leaveID)

	// Assert
	require.NoError(t, err)
	require.Equal(t, leaveID, leave.ID)
	require.Equal(t, memberID, leave.MemberID)
}

func TestGetLeaveByID_NotFound(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Act
	_, err := db.GetLeaveByID(ctx, "nonexistent")

	// Assert
	require.Error(t, err)
}

func TestDeleteExpiredLeaveRecords(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	now := time.Now().UTC()

	// Create an expired leave record (ended yesterday).
	pastStart := now.AddDate(0, 0, -5).Format("2006-01-02")
	pastEnd := now.AddDate(0, 0, -1).Format("2006-01-02")
	expiredID, err := db.CreateLeaveRecord(ctx, memberID, pastStart, pastEnd, LeaveTypeLeave)
	require.NoError(t, err)

	// Create a current / future leave record.
	futureStart := now.AddDate(0, 0, 1).Format("2006-01-02")
	futureEnd := now.AddDate(0, 0, 3).Format("2006-01-02")
	futureID, err := db.CreateLeaveRecord(ctx, memberID, futureStart, futureEnd, LeaveTypeLeave)
	require.NoError(t, err)

	// Act.
	err = db.DeleteExpiredLeaveRecords(ctx)
	require.NoError(t, err)

	// Expired record should be gone.
	_, err = db.GetLeaveByID(ctx, expiredID)
	require.Error(t, err)

	// Future record should still exist.
	future, err := db.GetLeaveByID(ctx, futureID)
	require.NoError(t, err)
	require.Equal(t, futureID, future.ID)
}
