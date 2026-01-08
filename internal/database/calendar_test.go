package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateCalendarSubscription_Success(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Act
	token, err := db.CreateCalendarSubscription(ctx, memberID)

	// Assert
	require.NoError(t, err)
	require.NotEmpty(t, token)

	// Verify in database
	member, err := db.GetMemberByToken(ctx, token)
	require.NoError(t, err)
	require.Equal(t, memberID, member.ID)
	require.Equal(t, "Alice", member.Name)
}

func TestCreateCalendarSubscription_InvalidMember(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Act
	_, err := db.CreateCalendarSubscription(ctx, "nonexistent")

	// Assert
	require.Error(t, err)
	require.Contains(t, err.Error(), "member not found")
}

func TestGetMemberByToken_Found(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	token, _ := db.CreateCalendarSubscription(ctx, memberID)

	// Act
	member, err := db.GetMemberByToken(ctx, token)

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

	ctx := context.Background()

	// Act
	_, err := db.GetMemberByToken(ctx, "nonexistent-token")

	// Assert
	require.Error(t, err)
}

func TestGetCalendarSubscriptionOperations(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")

	// Act - Create subscription
	token, err := db.CreateCalendarSubscription(ctx, memberID)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	// Verify subscription exists
	member, err := db.GetMemberByToken(ctx, token)
	require.NoError(t, err)
	require.Equal(t, memberID, member.ID)

	// Test duplicate subscription (should work - multiple tokens per member)
	token2, err := db.CreateCalendarSubscription(ctx, memberID)
	require.NoError(t, err)
	require.NotEmpty(t, token2)
	require.NotEqual(t, token, token2)
}

func TestCalendarSubscriptionLifecycle(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	token, _ := db.CreateCalendarSubscription(ctx, memberID)

	// Act - Verify we can get member by token
	member, err := db.GetMemberByToken(ctx, token)
	require.NoError(t, err)
	require.Equal(t, "Alice", member.Name)

	// Verify token is unique
	member2, err := db.GetMemberByToken(ctx, "wrong-token")
	require.Error(t, err)
	require.Nil(t, member2)
}

func TestGetUpcomingAssignments_WithCovers(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	coverMemberID, _ := db.AddTeamMember(ctx, "Bob", "bob@example.com")

	// Create original assignment (tomorrow)
	originalID, _ := db.CreateRotaAssignment(ctx, "2026-01-09", memberID, false, nil)

	// Create cover assignment (same day)
	_, _ = db.CreateRotaAssignment(ctx, "2026-01-09", coverMemberID, true, &originalID)

	// Act
	assignments, err := db.GetUpcomingAssignments(ctx, memberID, 10)

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

	ctx := context.Background()
	memberID, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")

	// Create assignment 15 days in future
	_, _ = db.CreateRotaAssignment(ctx, "2026-01-22", memberID, false, nil)

	// Act - Get only 10 days ahead
	assignments, err := db.GetUpcomingAssignments(ctx, memberID, 10)

	// Assert
	require.NoError(t, err)
	require.Empty(t, assignments) // Should not include 15-day future assignment
}
