package rota

import (
	"context"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
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

func TestScheduleMaintenance_GenerateMissingDays_SkipsHolidays(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	maintenance := NewScheduleMaintenance(db)
	holiday := time.Date(2026, time.January, 6, 0, 0, 0, 0, time.UTC)
	maintenance.SetHolidayChecker(func(date time.Time) bool {
		return date.Equal(holiday)
	})

	startDate := time.Date(2026, time.January, 5, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2026, time.January, 7, 0, 0, 0, 0, time.UTC)

	created, err := maintenance.GenerateMissingDays(ctx, startDate, endDate)
	require.NoError(t, err)
	assert.True(t, created)

	assignments, err := db.GetAssignmentsByDate(ctx, holiday.Format("2006-01-02"))
	require.NoError(t, err)
	assert.Empty(t, assignments, "holiday should not receive assignments")

	previousDayAssignments, err := db.GetAssignmentsByDate(ctx, startDate.Format("2006-01-02"))
	require.NoError(t, err)
	assert.NotEmpty(t, previousDayAssignments, "non-holiday business day should receive assignments")

	nextDayAssignments, err := db.GetAssignmentsByDate(ctx, endDate.Format("2006-01-02"))
	require.NoError(t, err)
	assert.NotEmpty(t, nextDayAssignments, "business day after holiday should receive assignments")
	assert.NotEqual(t, previousDayAssignments[0].MemberID, nextDayAssignments[0].MemberID,
		"holiday should not consume a rotation turn")
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

func TestScheduleMaintenance_HandleTeamChange_DeleteMemberReschedulesRota(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add three team members
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	charlieID, err := db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	maintenance := NewScheduleMaintenance(db)

	// Create initial schedule for 14 days
	_, err = maintenance.EnsureSchedule(ctx)
	require.NoError(t, err)

	// Get all assignments to verify initial state
	today := time.Now()
	endDate := today.AddDate(0, 0, 14)
	assignmentsBefore, err := db.GetAssignmentsByDateRange(
		ctx,
		today.Format("2006-01-02"),
		endDate.Format("2006-01-02"),
	)
	require.NoError(t, err)
	require.NotEmpty(t, assignmentsBefore, "Should have assignments before deletion")

	// Find assignments for Bob (middle member) - he should have some assignments scattered throughout
	bobAssignmentCount := 0
	bobAssignmentDates := make([]string, 0)
	for _, a := range assignmentsBefore {
		if a.MemberID == bobID {
			bobAssignmentCount++
			bobAssignmentDates = append(bobAssignmentDates, a.Date)
		}
	}
	require.Positive(t, bobAssignmentCount, "Bob should have assignments before deletion")

	// Delete Bob - this will CASCADE delete all his assignments
	err = db.DeleteTeamMember(ctx, bobID)
	require.NoError(t, err)

	// Get assignments after deletion but before HandleTeamChange
	assignmentsAfterDelete, err := db.GetAssignmentsByDateRange(
		ctx,
		today.Format("2006-01-02"),
		endDate.Format("2006-01-02"),
	)
	require.NoError(t, err)

	// Verify Bob's assignments were deleted (CASCADE)
	bobStillHasAssignments := false
	for _, a := range assignmentsAfterDelete {
		if a.MemberID == bobID {
			bobStillHasAssignments = true
			break
		}
	}
	assert.False(t, bobStillHasAssignments, "Bob's assignments should be cascade deleted")

	// The number of assignments should be less than before (Bob's assignments deleted)
	assert.Less(t, len(assignmentsAfterDelete), len(assignmentsBefore),
		"Should have fewer assignments after Bob is deleted")

	// Now call HandleTeamChange to fix the schedule
	err = maintenance.HandleTeamChange(ctx)
	require.NoError(t, err)

	// Get assignments after HandleTeamChange
	assignmentsAfterFix, err := db.GetAssignmentsByDateRange(
		ctx,
		today.Format("2006-01-02"),
		endDate.Format("2006-01-02"),
	)
	require.NoError(t, err)

	// Count business days in the 14-day window (excluding weekends)
	expectedBusinessDays := 0
	for current := today; !current.After(endDate); current = current.AddDate(0, 0, 1) {
		if current.Weekday() != time.Saturday && current.Weekday() != time.Sunday {
			expectedBusinessDays++
		}
	}

	// After HandleTeamChange, all business days should have assignments again
	// (Bob's old days should be reassigned to Alice and Charlie)
	assert.GreaterOrEqual(t, len(assignmentsAfterFix), expectedBusinessDays-1,
		"Should have assignments for all business days after HandleTeamChange. Expected ~%d, got %d",
		expectedBusinessDays, len(assignmentsAfterFix))

	// Verify that all of Bob's old assignment dates now have assignments from remaining members
	remainingMembers := map[string]bool{aliceID: true, charlieID: true}
	for _, date := range bobAssignmentDates {
		var dateAssignments []database.RotaAssignment
		dateAssignments, err = db.GetAssignmentsByDate(ctx, date)
		require.NoError(t, err)
		assert.NotEmpty(t, dateAssignments,
			"Date %s (previously Bob's) should have an assignment after HandleTeamChange", date)

		if len(dateAssignments) > 0 {
			// Verify the assignment is for a remaining member
			assert.True(t, remainingMembers[dateAssignments[0].MemberID],
				"Date %s should be assigned to Alice or Charlie, got member %s",
				date, dateAssignments[0].MemberID)
		}
	}

	// Verify schedule is complete (no gaps)
	start, end, err := maintenance.GetScheduleGap(ctx)
	require.NoError(t, err)
	assert.Empty(t, start, "Schedule should be complete after team member deletion")
	assert.Empty(t, end, "Schedule should be complete after team member deletion")
}

// TestGenerateMissingDays_HolidayOnlyRange verifies that GenerateMissingDays returns
// created=false when every date in the range is either a weekend or a holiday,
// i.e. no assignment is actually written.
func TestGenerateMissingDays_HolidayOnlyRange(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	// 2026-01-06 is a Tuesday; make it a holiday.
	holiday := time.Date(2026, time.January, 6, 0, 0, 0, 0, time.UTC)
	maintenance := NewScheduleMaintenance(db)
	maintenance.SetHolidayChecker(func(date time.Time) bool {
		return date.Equal(holiday)
	})

	// Range contains only the holiday - no real business day.
	created, err := maintenance.GenerateMissingDays(ctx, holiday, holiday)
	require.NoError(t, err)
	assert.False(t, created, "no assignment should be created when the range is all holidays")
}

// TestGetStartingMemberIndex_SkipsWeekendBoundary verifies that when the schedule
// starts on a Monday the rotation anchor is seeded from the most recent Friday
// assignment (not from Sunday which has no assignment).
func TestGetStartingMemberIndex_SkipsWeekendBoundary(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	maintenance := NewScheduleMaintenance(db)

	// Create an assignment on Friday 2026-01-09.
	friday := time.Date(2026, time.January, 9, 0, 0, 0, 0, time.UTC)
	_, err = db.CreateRotaAssignment(ctx, friday.Format("2006-01-02"), func() string {
		members, _ := db.GetActiveTeamMembers(ctx)
		return members[0].ID // Alice
	}(), false, nil)
	require.NoError(t, err)

	// Generate assignments starting from Monday 2026-01-12.
	// The rotation should continue from Alice, so Monday gets Bob.
	monday := time.Date(2026, time.January, 12, 0, 0, 0, 0, time.UTC)
	created, err := maintenance.GenerateMissingDays(ctx, monday, monday)
	require.NoError(t, err)
	assert.True(t, created, "assignment should be created for Monday")

	mondayAssignments, err := db.GetAssignmentsByDate(ctx, monday.Format("2006-01-02"))
	require.NoError(t, err)
	require.NotEmpty(t, mondayAssignments)

	members, err := db.GetActiveTeamMembers(ctx)
	require.NoError(t, err)
	require.Len(t, members, 2)

	// Friday was assigned to Alice (members[0]); Monday should be Bob (members[1]).
	assert.Equal(t, members[1].ID, mondayAssignments[0].MemberID,
		"Monday rotation should continue from Friday's Alice assignment, so Bob is next")
}
