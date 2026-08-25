package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateTeamMember(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add a team member
	memberID, err := db.AddTeamMember(ctx, "John Doe", "john@example.com")
	require.NoError(t, err)

	// Update the team member
	err = db.UpdateTeamMember(ctx, memberID, "Jane Doe", "jane@example.com")
	require.NoError(t, err)

	// Verify the update
	member, err := db.GetMemberByID(ctx, memberID)
	require.NoError(t, err)
	assert.Equal(t, "Jane Doe", member.Name)
	assert.Equal(t, "jane@example.com", member.Email)
}

func TestDeleteTeamMember(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add a team member
	memberID, err := db.AddTeamMember(ctx, "John Doe", "john@example.com")
	require.NoError(t, err)

	// Delete the team member
	err = db.DeleteTeamMember(ctx, memberID)
	require.NoError(t, err)

	// Verify the member is deleted
	_, err = db.GetMemberByID(ctx, memberID)
	require.Error(t, err)
}

func TestDeleteTeamMemberWithRelatedRecords(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members
	memberID, err := db.AddTeamMember(ctx, "John Doe", "john@example.com")
	require.NoError(t, err)

	// Create related records

	// 1. Create a calendar subscription
	_, err = db.CreateCalendarSubscription(ctx, memberID)
	require.NoError(t, err)

	// 2. Create a rota assignment
	_, err = db.CreateRotaAssignment(ctx, "2025-01-15", memberID, false, nil)
	require.NoError(t, err)

	// 3. Create a leave record
	_, err = db.CreateLeaveRecord(ctx, memberID, "2025-01-16", "2025-01-17", LeaveTypeLeave)
	require.NoError(t, err)

	// Attempt to delete the team member - this should succeed with CASCADE
	err = db.DeleteTeamMember(ctx, memberID)
	require.NoError(t, err, "Should delete team member with related records via CASCADE")

	// Verify the member is deleted
	_, err = db.GetMemberByID(ctx, memberID)
	require.Error(t, err)

	// Verify related records are also deleted
	assignments, err := db.GetAssignmentsByDateRange(ctx, "2025-01-15", "2025-01-15")
	require.NoError(t, err)
	assert.Empty(t, assignments, "Rota assignments should be cascade deleted")
}
