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
	assert.Error(t, err)
}
