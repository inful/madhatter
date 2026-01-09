package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateRotaAssignment_Success(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Act
	assignmentID, err := db.CreateRotaAssignment(ctx, "2024-01-15", memberID, false, nil)

	// Assert
	require.NoError(t, err)
	require.NotEmpty(t, assignmentID)

	// Verify in database
	assignments, err := db.GetAssignmentsByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	require.Equal(t, memberID, assignments[0].MemberID)
	require.False(t, assignments[0].IsCover)
	require.Nil(t, assignments[0].OriginalAssignmentID)
}

func TestCreateRotaAssignment_CoverAssignment(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")

	// First create original assignment
	originalAssignmentID, _ := db.CreateRotaAssignment(ctx, "2024-01-15", memberID, false, nil)

	// Act - Create cover assignment
	coverMemberID, _ := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	assignmentID, err := db.CreateRotaAssignment(ctx, "2024-01-15", coverMemberID, true, &originalAssignmentID)

	// Assert
	require.NoError(t, err)
	require.NotEmpty(t, assignmentID)

	assignments, err := db.GetAssignmentsByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Len(t, assignments, 2) // Both original and cover
	require.True(t, assignments[1].IsCover)
	require.Equal(t, coverMemberID, assignments[1].MemberID)
	require.Equal(t, originalAssignmentID, *assignments[1].OriginalAssignmentID)
}

func TestCreateRotaAssignment_InvalidMember(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Act
	_, err := db.CreateRotaAssignment(ctx, "2024-01-15", "nonexistent", false, nil)

	// Assert
	require.Error(t, err)
}

func TestGetAssignmentsByDate_MultipleAssignments(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	member1, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	member2, _ := db.AddTeamMember(ctx, "Bob", "bob@example.com")

	// Create original assignment and cover assignment (different is_cover values)
	originalID, _ := db.CreateRotaAssignment(ctx, "2024-01-15", member1, false, nil)
	_, _ = db.CreateRotaAssignment(ctx, "2024-01-15", member2, true, &originalID)

	// Act
	assignments, err := db.GetAssignmentsByDate(ctx, "2024-01-15")

	// Assert
	require.NoError(t, err)
	require.Len(t, assignments, 2)
}

func TestGetAssignmentsByDate_Empty(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Act
	assignments, err := db.GetAssignmentsByDate(ctx, "2024-01-15")

	// Assert
	require.NoError(t, err)
	require.Empty(t, assignments)
}

func TestGetAssignmentsByDate_DifferentDates(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	_, _ = db.CreateRotaAssignment(ctx, "2024-01-15", memberID, false, nil)

	// Act
	assignments1, _ := db.GetAssignmentsByDate(ctx, "2024-01-15")
	assignments2, _ := db.GetAssignmentsByDate(ctx, "2024-01-16")

	// Assert
	require.Len(t, assignments1, 1)
	require.Empty(t, assignments2)
}

func TestGetUpcomingAssignments(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")

	// Create assignments relative to current date (2026-01-08)
	_, _ = db.CreateRotaAssignment(ctx, "2026-01-09", memberID, false, nil) // Tomorrow (1 day away)
	_, _ = db.CreateRotaAssignment(ctx, "2026-01-12", memberID, false, nil) // 4 days away
	_, _ = db.CreateRotaAssignment(ctx, "2026-01-22", memberID, false, nil) // 14 days away (beyond 10 days)

	// Act - Get assignments for next 10 days
	assignments, err := db.GetUpcomingAssignments(ctx, memberID, 10)

	// Assert
	require.NoError(t, err)
	require.Len(t, assignments, 2) // Only 9th and 12th, not 22nd (14 days away)
}

func TestGetUpcomingAssignments_Empty(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")

	// Act
	assignments, err := db.GetUpcomingAssignments(ctx, memberID, 10)

	// Assert
	require.NoError(t, err)
	require.Empty(t, assignments)
}
