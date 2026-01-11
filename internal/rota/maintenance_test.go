package rota

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduleMaintenance_EnsureSchedule(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members first
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	maintenance := NewScheduleMaintenance(db)

	// Test 1: No existing assignments - should create schedule up to 14 days from today
	t.Run("NoExistingAssignments", func(t *testing.T) {
		created, err := maintenance.EnsureSchedule(ctx)
		require.NoError(t, err)
		assert.True(t, created, "Should create new assignments")

		// Verify assignments exist for the next 14 days
		latestDate, err := db.GetLatestAssignmentDate(ctx)
		require.NoError(t, err)
		assert.NotEmpty(t, latestDate)

		latestTime, _ := time.Parse("2006-01-02", latestDate)
		todayTime := time.Now()
		expectedEnd := todayTime.AddDate(0, 0, 14)

		// Should have assignments up to 14 days from today
		// Since weekends are skipped, the latest assignment might be before the exact 14-day mark
		// But it should be within the 14-day window
		latestDateOnly := latestTime.Format("2006-01-02")
		expectedEndOnly := expectedEnd.Format("2006-01-02")

		// Check that latest assignment is not beyond the 14-day window
		assert.LessOrEqual(t, latestDateOnly, expectedEndOnly,
			"Latest assignment should not be beyond 14 days from today")

		// GetScheduleGap should return empty when schedule is complete for business days
		start, end, err := maintenance.GetScheduleGap(ctx)
		require.NoError(t, err)
		assert.Empty(t, start, "Schedule should be complete (no gaps)")
		assert.Empty(t, end, "Schedule should be complete (no gaps)")
	})

	// Test 2: Existing complete schedule - should not create new assignments
	t.Run("CompleteSchedule", func(t *testing.T) {
		created, err := maintenance.EnsureSchedule(ctx)
		require.NoError(t, err)
		assert.False(t, created, "Should not create new assignments when schedule is complete")
	})
}

func TestScheduleMaintenance_GetScheduleGap(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members first
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	maintenance := NewScheduleMaintenance(db)

	t.Run("NoAssignments", func(t *testing.T) {
		start, end, err := maintenance.GetScheduleGap(ctx)
		require.NoError(t, err)

		today := time.Now().Format("2006-01-02")
		expectedEnd := time.Now().AddDate(0, 0, 14).Format("2006-01-02")

		assert.Equal(t, today, start)
		assert.Equal(t, expectedEnd, end)
	})

	t.Run("CompleteSchedule", func(t *testing.T) {
		// Create full schedule
		_, err := maintenance.EnsureSchedule(ctx)
		require.NoError(t, err)

		start, end, err := maintenance.GetScheduleGap(ctx)
		require.NoError(t, err)

		// Schedule should be complete for business days
		assert.Empty(t, start, "Start should be empty when schedule is complete")
		assert.Empty(t, end, "End should be empty when schedule is complete")
	})
}

func TestScheduleMaintenance_GenerateMissingDays(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members first
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	maintenance := NewScheduleMaintenance(db)

	t.Run("GenerateForDateRange", func(t *testing.T) {
		startDate := time.Now().AddDate(0, 0, 1) // Tomorrow
		endDate := time.Now().AddDate(0, 0, 7)   // 7 days from now

		created, err := maintenance.GenerateMissingDays(ctx, startDate, endDate)
		require.NoError(t, err)
		assert.True(t, created, "Should create assignments")

		// Verify assignments exist for the range
		for i := 1; i <= 7; i++ {
			date := time.Now().AddDate(0, 0, i)
			if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
				continue // Skip weekends
			}

			dateStr := date.Format("2006-01-02")
			assignments, err := db.GetAssignmentsByDate(ctx, dateStr)
			require.NoError(t, err)
			assert.NotEmpty(t, assignments, "Should have assignments for %s", dateStr)
		}
	})

	t.Run("PreserveExistingAssignments", func(t *testing.T) {
		// Get the latest assignment date from the first test
		latestDate, err := db.GetLatestAssignmentDate(ctx)
		require.NoError(t, err)

		// Generate assignments for the next 7 days after the latest
		latestTime, _ := time.Parse("2006-01-02", latestDate)
		startDate := latestTime.AddDate(0, 0, 1)
		endDate := latestTime.AddDate(0, 0, 8)

		// This should create new assignments (7 days after the current schedule)
		created, err := maintenance.GenerateMissingDays(ctx, startDate, endDate)
		require.NoError(t, err)
		assert.True(t, created, "Should create new assignments for dates after current schedule")

		// Verify new assignments were created
		newAssignments, err := db.GetAssignmentsByDateRange(
			ctx,
			startDate.Format("2006-01-02"),
			endDate.Format("2006-01-02"),
		)
		require.NoError(t, err)
		assert.NotEmpty(t, newAssignments, "Should have new assignments")

		// Now generate again for the same range - should not create duplicates
		created2, err := maintenance.GenerateMissingDays(ctx, startDate, endDate)
		require.NoError(t, err)
		assert.False(t, created2, "Should not create new assignments when range is complete")

		// Verify no duplicates
		assignmentsAfter, err := db.GetAssignmentsByDateRange(
			ctx,
			startDate.Format("2006-01-02"),
			endDate.Format("2006-01-02"),
		)
		require.NoError(t, err)
		assert.Len(t, assignmentsAfter, len(newAssignments), "Should not create duplicates")
	})
}

func TestScheduleMaintenance_HandleTeamChange(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members first
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	maintenance := NewScheduleMaintenance(db)

	// Create initial schedule
	_, err = maintenance.EnsureSchedule(ctx)
	require.NoError(t, err)

	t.Run("TeamChangeTriggersScheduleUpdate", func(t *testing.T) {
		// Add a new team member
		_, err := db.AddTeamMember(ctx, "New Member", "new@example.com")
		require.NoError(t, err)

		// Handle team change
		err = maintenance.HandleTeamChange(ctx)
		require.NoError(t, err)

		// Verify schedule is still complete
		start, end, err := maintenance.GetScheduleGap(ctx)
		require.NoError(t, err)

		// Schedule should be complete for business days
		assert.Empty(t, start, "Schedule should be complete after team change")
		assert.Empty(t, end, "Schedule should be complete after team change")
	})
}

func TestScheduleMaintenance_HandleLeaveChange_LeaveCreatesCover(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members first
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	maintenance := NewScheduleMaintenance(db)

	// Create initial schedule
	_, err = maintenance.EnsureSchedule(ctx)
	require.NoError(t, err)

	// Get a team member
	members, err := db.GetActiveTeamMembers(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, members)

	// Find a weekday in the schedule (skip weekends)
	targetDate := time.Now()
	for targetDate.Weekday() == time.Saturday || targetDate.Weekday() == time.Sunday {
		targetDate = targetDate.AddDate(0, 0, 1)
	}
	targetDateStr := targetDate.Format("2006-01-02")

	// Create leave for the target date
	leaveID, err := db.CreateLeaveRecord(ctx, members[0].ID, targetDateStr, targetDateStr)
	require.NoError(t, err)

	// Handle leave change
	err = maintenance.HandleLeaveChange(ctx, leaveID)
	require.NoError(t, err)

	// Verify cover assignment exists
	assignments, err := db.GetAssignmentsByDate(ctx, targetDateStr)
	require.NoError(t, err)

	// Should have at least one assignment
	assert.NotEmpty(t, assignments, "Should have assignments for leave day")

	// Check if there's a cover assignment
	hasCover := false
	for _, a := range assignments {
		if a.IsCover {
			hasCover = true
			break
		}
	}
	assert.True(t, hasCover, "Should have a cover assignment")
}

func TestScheduleMaintenance_HandleLeaveChange_DeleteRemovesCover(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members first
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	maintenance := NewScheduleMaintenance(db)

	// Create initial schedule
	_, err = maintenance.EnsureSchedule(ctx)
	require.NoError(t, err)

	// Get a team member
	members, err := db.GetActiveTeamMembers(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, members)

	// Find a weekday in the schedule
	targetDate := time.Now()
	for targetDate.Weekday() == time.Saturday || targetDate.Weekday() == time.Sunday {
		targetDate = targetDate.AddDate(0, 0, 1)
	}
	targetDateStr := targetDate.Format("2006-01-02")

	// Create leave and get cover assignment
	leaveID, err := db.CreateLeaveRecord(ctx, members[0].ID, targetDateStr, targetDateStr)
	require.NoError(t, err)

	err = maintenance.HandleLeaveChange(ctx, leaveID)
	require.NoError(t, err)

	// Verify cover exists
	assignmentsBefore, err := db.GetAssignmentsByDate(ctx, targetDateStr)
	require.NoError(t, err)
	hasCoverBefore := false
	for _, a := range assignmentsBefore {
		if a.IsCover {
			hasCoverBefore = true
			break
		}
	}
	require.True(t, hasCoverBefore, "Should have cover before deletion")

	// Delete the leave (simulating the web handler flow)
	err = db.DeleteLeaveRecord(ctx, leaveID)
	require.NoError(t, err)

	// Call HandleTeamChange like the handlers do
	err = maintenance.HandleTeamChange(ctx)
	require.NoError(t, err)

	// Verify cover is removed
	assignmentsAfter, err := db.GetAssignmentsByDate(ctx, targetDateStr)
	require.NoError(t, err)
	hasCoverAfter := false
	for _, a := range assignmentsAfter {
		if a.IsCover {
			hasCoverAfter = true
			break
		}
	}
	assert.False(t, hasCoverAfter, "Should not have cover after deletion + HandleTeamChange")
}

func TestScheduleMaintenance_RotationPreservation(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members first
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	maintenance := NewScheduleMaintenance(db)

	// Create initial schedule
	_, err = maintenance.EnsureSchedule(ctx)
	require.NoError(t, err)

	t.Run("RotationContinuesFromLastAssignment", func(t *testing.T) {
		// Get the last assignment
		latestDate, err := db.GetLatestAssignmentDate(ctx)
		require.NoError(t, err)

		lastAssignments, err := db.GetAssignmentsByDate(ctx, latestDate)
		require.NoError(t, err)
		require.NotEmpty(t, lastAssignments)

		lastMemberID := lastAssignments[0].MemberID

		// Generate additional days
		startDate, _ := time.Parse("2006-01-02", latestDate)
		startDate = startDate.AddDate(0, 0, 1)
		endDate := startDate.AddDate(0, 0, 7)

		created, err := maintenance.GenerateMissingDays(ctx, startDate, endDate)
		require.NoError(t, err)
		assert.True(t, created)

		// Get the first new assignment (skip weekends)
		newDateStr := startDate.Format("2006-01-02")
		if startDate.Weekday() == time.Saturday || startDate.Weekday() == time.Sunday {
			// Skip to Monday
			startDate = startDate.AddDate(0, 0, 2)
			newDateStr = startDate.Format("2006-01-02")
		}

		newAssignments, err := db.GetAssignmentsByDate(ctx, newDateStr)
		require.NoError(t, err)
		require.NotEmpty(t, newAssignments)

		// The rotation should continue from the last member
		newMemberID := newAssignments[0].MemberID

		// Get all team members to understand the rotation
		members, _ := db.GetActiveTeamMembers(ctx)

		// Find indices
		lastIndex := -1
		newIndex := -1
		for i, m := range members {
			if m.ID == lastMemberID {
				lastIndex = i
			}
			if m.ID == newMemberID {
				newIndex = i
			}
		}

		// With 2 members (Alice, Bob), rotation should be:
		// If last was Alice (index 0), next should be Bob (index 1)
		// If last was Bob (index 1), next should be Alice (index 0)
		if len(members) > 1 {
			expectedNewIndex := (lastIndex + 1) % len(members)
			assert.Equal(t, expectedNewIndex, newIndex,
				"Rotation should continue correctly. Last: %s (index %d), Expected next: %s (index %d), Got: %s (index %d)",
				members[lastIndex].Name, lastIndex,
				members[expectedNewIndex].Name, expectedNewIndex,
				members[newIndex].Name, newIndex)
		}
	})
}
