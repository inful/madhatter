package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateLeaveRecord_Success(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Act
	leaveID, err := db.CreateLeaveRecord(ctx, memberID, "sick", "2024-01-15", "2024-01-17")

	// Assert
	require.NoError(t, err)
	require.NotEmpty(t, leaveID)

	// Verify in database
	leaves, err := db.GetLeaveByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Len(t, leaves, 1)
	require.Equal(t, memberID, leaves[0].MemberID)
	require.Equal(t, "sick", leaves[0].Type)
	require.Equal(t, "2024-01-15", leaves[0].StartDate.Format("2006-01-02"))
	require.Equal(t, "2024-01-17", leaves[0].EndDate.Format("2006-01-02"))
	require.Equal(t, "pending", leaves[0].Status)
}

func TestCreateLeaveRecord_InvalidMember(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Act
	_, err := db.CreateLeaveRecord(ctx, "nonexistent", "sick", "2024-01-15", "2024-01-17")

	// Assert
	require.Error(t, err)
}

func TestGetLeaveByDate_Range(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	_, _ = db.CreateLeaveRecord(ctx, memberID, "sick", "2024-01-15", "2024-01-17")

	// Act & Assert - Should find leave on start date
	leaves, err := db.GetLeaveByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Len(t, leaves, 1)

	// Should find leave on end date
	leaves, err = db.GetLeaveByDate(ctx, "2024-01-17")
	require.NoError(t, err)
	require.Len(t, leaves, 1)

	// Should find leave in middle of range
	leaves, err = db.GetLeaveByDate(ctx, "2024-01-16")
	require.NoError(t, err)
	require.Len(t, leaves, 1)

	// Should NOT find leave outside range
	leaves, err = db.GetLeaveByDate(ctx, "2024-01-14")
	require.NoError(t, err)
	require.Empty(t, leaves)

	leaves, err = db.GetLeaveByDate(ctx, "2024-01-18")
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

	_, _ = db.CreateLeaveRecord(ctx, member1, "sick", "2024-01-15", "2024-01-17")
	_, _ = db.CreateLeaveRecord(ctx, member2, "vacation", "2024-01-16", "2024-01-18")

	// Act
	leaves, err := db.GetLeaveByDate(ctx, "2024-01-16")

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
	leaveID, _ := db.CreateLeaveRecord(ctx, memberID, "sick", "2024-01-15", "2024-01-17")

	// Act
	err := db.UpdateLeaveStatus(ctx, leaveID, "assigned")

	// Assert
	require.NoError(t, err)

	leaves, _ := db.GetLeaveByDate(ctx, "2024-01-15")
	require.Equal(t, "assigned", leaves[0].Status)
}

func TestGetLeaveByID(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	leaveID, _ := db.CreateLeaveRecord(ctx, memberID, "sick", "2024-01-15", "2024-01-17")

	// Act
	leave, err := db.GetLeaveByID(ctx, leaveID)

	// Assert
	require.NoError(t, err)
	require.Equal(t, leaveID, leave.ID)
	require.Equal(t, memberID, leave.MemberID)
	require.Equal(t, "sick", leave.Type)
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
