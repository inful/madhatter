package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCreateRotaAssignment_Success(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Use dynamic date
	testDate := time.Now().AddDate(0, 0, 7).Format("2006-01-02")

	// Act
	assignmentID, err := db.CreateRotaAssignment(ctx, testDate, memberID, false, nil)

	// Assert
	require.NoError(t, err)
	require.NotEmpty(t, assignmentID)

	// Verify in database
	assignments, err := db.GetAssignmentsByDate(ctx, testDate)
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

	// Use dynamic date
	testDate := time.Now().AddDate(0, 0, 7).Format("2006-01-02")

	// First create original assignment
	originalAssignmentID, _ := db.CreateRotaAssignment(ctx, testDate, memberID, false, nil)

	// Act - Create cover assignment
	coverMemberID, _ := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	assignmentID, err := db.CreateRotaAssignment(ctx, testDate, coverMemberID, true, &originalAssignmentID)

	// Assert
	require.NoError(t, err)
	require.NotEmpty(t, assignmentID)

	assignments, err := db.GetAssignmentsByDate(ctx, testDate)
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

	// Use dynamic date
	testDate := time.Now().AddDate(0, 0, 7).Format("2006-01-02")

	// Act
	_, err := db.CreateRotaAssignment(ctx, testDate, "nonexistent", false, nil)

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

	// Use dynamic date
	testDate := time.Now().AddDate(0, 0, 7).Format("2006-01-02")

	// Create original assignment and cover assignment (different is_cover values)
	originalID, _ := db.CreateRotaAssignment(ctx, testDate, member1, false, nil)
	_, _ = db.CreateRotaAssignment(ctx, testDate, member2, true, &originalID)

	// Act
	assignments, err := db.GetAssignmentsByDate(ctx, testDate)

	// Assert
	require.NoError(t, err)
	require.Len(t, assignments, 2)
}

func TestGetAssignmentsByDate_Empty(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Use dynamic date
	testDate := time.Now().AddDate(0, 0, 7).Format("2006-01-02")

	// Act
	assignments, err := db.GetAssignmentsByDate(ctx, testDate)

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

	// Use dynamic dates
	baseDate := time.Now().AddDate(0, 0, 7)
	date1 := baseDate.Format("2006-01-02")
	date2 := baseDate.AddDate(0, 0, 1).Format("2006-01-02")

	_, _ = db.CreateRotaAssignment(ctx, date1, memberID, false, nil)

	// Act
	assignments1, _ := db.GetAssignmentsByDate(ctx, date1)
	assignments2, _ := db.GetAssignmentsByDate(ctx, date2)

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

	// Get current date and create assignments relative to it
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	fourDaysAway := time.Now().AddDate(0, 0, 4).Format("2006-01-02")
	fourteenDaysAway := time.Now().AddDate(0, 0, 14).Format("2006-01-02")

	_, _ = db.CreateRotaAssignment(ctx, tomorrow, memberID, false, nil)         // Tomorrow (1 day away)
	_, _ = db.CreateRotaAssignment(ctx, fourDaysAway, memberID, false, nil)     // 4 days away
	_, _ = db.CreateRotaAssignment(ctx, fourteenDaysAway, memberID, false, nil) // 14 days away (beyond 10 days)

	// Act - Get assignments for next 10 days
	assignments, err := db.GetUpcomingAssignments(ctx, memberID, 10)

	// Assert
	require.NoError(t, err)
	require.Len(t, assignments, 2) // Only tomorrow and 4 days away, not 14 days away
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
