package database

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateCalendarSubscription_Success(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	memberID, err := db.AddTeamMember("Alice", "alice@example.com")
	require.NoError(t, err)

	// Act
	token, err := db.CreateCalendarSubscription(memberID)

	// Assert
	require.NoError(t, err)
	require.NotEmpty(t, token)

	// Verify in database
	member, err := db.GetMemberByToken(token)
	require.NoError(t, err)
	require.Equal(t, memberID, member.ID)
	require.Equal(t, "Alice", member.Name)
}

func TestCreateCalendarSubscription_InvalidMember(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Act
	_, err := db.CreateCalendarSubscription("nonexistent")

	// Assert
	require.Error(t, err)
	require.Contains(t, err.Error(), "member not found")
}

func TestGetMemberByToken_Found(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	memberID, _ := db.AddTeamMember("Alice", "alice@example.com")
	token, _ := db.CreateCalendarSubscription(memberID)

	// Act
	member, err := db.GetMemberByToken(token)

	// Assert
	require.NoError(t, err)
	require.Equal(t, memberID, member.ID)
	require.Equal(t, "Alice", member.Name)
	require.Equal(t, "alice@example.com", member.Email)
	require.True(t, member.IsActive)
}

func TestGetMemberByToken_NotFound(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Act
	_, err := db.GetMemberByToken("nonexistent-token")

	// Assert
	require.Error(t, err)
}

func TestGetCalendarSubscriptionOperations(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	memberID, _ := db.AddTeamMember("Alice", "alice@example.com")

	// Act - Create subscription
	token, err := db.CreateCalendarSubscription(memberID)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	// Verify subscription exists
	member, err := db.GetMemberByToken(token)
	require.NoError(t, err)
	require.Equal(t, memberID, member.ID)

	// Test duplicate subscription (should work - multiple tokens per member)
	token2, err := db.CreateCalendarSubscription(memberID)
	require.NoError(t, err)
	require.NotEmpty(t, token2)
	require.NotEqual(t, token, token2)
}

func TestCalendarSubscriptionLifecycle(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	memberID, _ := db.AddTeamMember("Alice", "alice@example.com")
	token, _ := db.CreateCalendarSubscription(memberID)

	// Act - Verify we can get member by token
	member, err := db.GetMemberByToken(token)
	require.NoError(t, err)
	require.Equal(t, "Alice", member.Name)

	// Verify token is unique
	member2, err := db.GetMemberByToken("wrong-token")
	require.Error(t, err)
	require.Nil(t, member2)
}

func TestGetUpcomingAssignments_WithCovers(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	memberID, _ := db.AddTeamMember("Alice", "alice@example.com")
	coverMemberID, _ := db.AddTeamMember("Bob", "bob@example.com")

	// Create original assignment
	originalID, _ := db.CreateRotaAssignment("2026-01-07", memberID, false, nil)

	// Create cover assignment
	_, _ = db.CreateRotaAssignment("2026-01-07", coverMemberID, true, &originalID)

	// Act
	assignments, err := db.GetUpcomingAssignments(memberID, 10)

	// Assert - Should only get original assignment, not cover
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	require.Equal(t, originalID, assignments[0].ID)
	require.False(t, assignments[0].IsCover)
}

func TestGetUpcomingAssignments_BeyondRange(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	memberID, _ := db.AddTeamMember("Alice", "alice@example.com")

	// Create assignment 15 days in future
	_, _ = db.CreateRotaAssignment("2026-01-22", memberID, false, nil)

	// Act - Get only 10 days ahead
	assignments, err := db.GetUpcomingAssignments(memberID, 10)

	// Assert
	require.NoError(t, err)
	require.Empty(t, assignments) // Should not include 15-day future assignment
}
