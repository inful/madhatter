package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateLeaveRecord(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add a team member
	memberID, err := db.AddTeamMember(ctx, "John Doe", "john@example.com")
	require.NoError(t, err)

	// Create a leave record
	leaveID, err := db.CreateLeaveRecord(ctx, memberID, "2026-02-01", "2026-02-05")
	require.NoError(t, err)

	// Update the leave record
	err = db.UpdateLeaveRecord(ctx, leaveID, memberID, "2026-02-10", "2026-02-15", "assigned")
	require.NoError(t, err)

	// Verify the update
	leave, err := db.GetLeaveByID(ctx, leaveID)
	require.NoError(t, err)

	expectedStart, _ := time.Parse("2006-01-02", "2026-02-10")
	expectedEnd, _ := time.Parse("2006-01-02", "2026-02-15")

	assert.Equal(t, expectedStart, leave.StartDate)
	assert.Equal(t, expectedEnd, leave.EndDate)
	assert.Equal(t, "assigned", leave.Status)
}

func TestDeleteLeaveRecord(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add a team member
	memberID, err := db.AddTeamMember(ctx, "John Doe", "john@example.com")
	require.NoError(t, err)

	// Create a leave record
	leaveID, err := db.CreateLeaveRecord(ctx, memberID, "2026-02-01", "2026-02-05")
	require.NoError(t, err)

	// Delete the leave record
	err = db.DeleteLeaveRecord(ctx, leaveID)
	require.NoError(t, err)

	// Verify the leave is deleted
	_, err = db.GetLeaveByID(ctx, leaveID)
	assert.Error(t, err)
}
