package database

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateRotaAssignment_Success(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	memberID, err := db.AddTeamMember("Alice", "alice@example.com")
	require.NoError(t, err)

	// Act
	assignmentID, err := db.CreateRotaAssignment("2024-01-15", memberID, false, nil)

	// Assert
	require.NoError(t, err)
	require.NotEmpty(t, assignmentID)

	// Verify in database
	assignments, err := db.GetAssignmentsByDate("2024-01-15")
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	require.Equal(t, memberID, assignments[0].MemberID)
	require.False(t, assignments[0].IsCover)
	require.Nil(t, assignments[0].LeaveID)
}

func TestCreateRotaAssignment_CoverAssignment(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	memberID, _ := db.AddTeamMember("Alice", "alice@example.com")

	// First create a leave record
	leaveID, _ := db.CreateLeaveRecord(memberID, "sick", "2024-01-15", "2024-01-15")

	// Act - Create cover assignment
	coverMemberID, _ := db.AddTeamMember("Bob", "bob@example.com")
	assignmentID, err := db.CreateRotaAssignment("2024-01-15", coverMemberID, true, &leaveID)

	// Assert
	require.NoError(t, err)
	require.NotEmpty(t, assignmentID)

	assignments, err := db.GetAssignmentsByDate("2024-01-15")
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	require.True(t, assignments[0].IsCover)
	require.Equal(t, coverMemberID, assignments[0].MemberID)
	require.Equal(t, leaveID, *assignments[0].LeaveID)
}

func TestCreateRotaAssignment_InvalidMember(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Act
	_, err := db.CreateRotaAssignment("2024-01-15", "nonexistent", false, nil)

	// Assert
	require.Error(t, err)
}

func TestGetAssignmentsByDate_MultipleAssignments(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	member1, _ := db.AddTeamMember("Alice", "alice@example.com")
	member2, _ := db.AddTeamMember("Bob", "bob@example.com")

	_, _ = db.CreateRotaAssignment("2024-01-15", member1, false, nil)
	_, _ = db.CreateRotaAssignment("2024-01-15", member2, false, nil)

	// Act
	assignments, err := db.GetAssignmentsByDate("2024-01-15")

	// Assert
	require.NoError(t, err)
	require.Len(t, assignments, 2)
}

func TestGetAssignmentsByDate_Empty(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Act
	assignments, err := db.GetAssignmentsByDate("2024-01-15")

	// Assert
	require.NoError(t, err)
	require.Len(t, assignments, 0)
}

func TestGetAssignmentsByDate_DifferentDates(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	memberID, _ := db.AddTeamMember("Alice", "alice@example.com")
	_, _ = db.CreateRotaAssignment("2024-01-15", memberID, false, nil)

	// Act
	assignments1, _ := db.GetAssignmentsByDate("2024-01-15")
	assignments2, _ := db.GetAssignmentsByDate("2024-01-16")

	// Assert
	require.Len(t, assignments1, 1)
	require.Len(t, assignments2, 0)
}

func TestGetUpcomingAssignments(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	memberID, _ := db.AddTeamMember("Alice", "alice@example.com")

	// Create assignments relative to current date
	_, _ = db.CreateRotaAssignment("2026-01-07", memberID, false, nil) // Tomorrow
	_, _ = db.CreateRotaAssignment("2026-01-10", memberID, false, nil) // 4 days away
	_, _ = db.CreateRotaAssignment("2026-01-20", memberID, false, nil) // 14 days away (beyond 10 days)

	// Act - Get assignments for next 10 days
	assignments, err := db.GetUpcomingAssignments(memberID, 10)

	// Assert
	require.NoError(t, err)
	require.Len(t, assignments, 2) // Only 7th and 10th, not 20th (14 days away)
}

func TestGetUpcomingAssignments_Empty(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	memberID, _ := db.AddTeamMember("Alice", "alice@example.com")

	// Act
	assignments, err := db.GetUpcomingAssignments(memberID, 10)

	// Assert
	require.NoError(t, err)
	require.Len(t, assignments, 0)
}
