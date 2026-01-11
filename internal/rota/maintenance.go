package rota

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/inful/madhatter/internal/database"
)

const scheduleDaysAhead = 14

// ScheduleMaintenance handles automatic schedule maintenance and updates.
type ScheduleMaintenance struct {
	db     *database.DB
	engine *Engine
}

// NewScheduleMaintenance creates a new schedule maintenance service.
func NewScheduleMaintenance(db *database.DB) *ScheduleMaintenance {
	return &ScheduleMaintenance{
		db:     db,
		engine: NewEngine(db),
	}
}

// EnsureSchedule guarantees that a schedule exists for the next 14 days from today.
// It only generates assignments for dates beyond the latest existing assignment.
// Returns true if new assignments were created, false if schedule was already complete.
func (sm *ScheduleMaintenance) EnsureSchedule(ctx context.Context) (bool, error) {
	today := time.Now().Format("2006-01-02")
	todayTime, err := time.Parse("2006-01-02", today)
	if err != nil {
		return false, fmt.Errorf("failed to parse today date: %w", err)
	}
	endDate := todayTime.AddDate(0, 0, scheduleDaysAhead)

	// First reconcile stale covers in the 14-day window
	if err = sm.reconcileCoversForDateRange(ctx, todayTime, endDate); err != nil {
		return false, fmt.Errorf("failed to reconcile covers: %w", err)
	}

	// Get the latest date that has any assignments
	latestAssignmentDate, err := sm.db.GetLatestAssignmentDate(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get latest assignment date: %w", err)
	}

	// Determine the start date for new assignments
	var startDate time.Time
	if latestAssignmentDate == "" {
		// No assignments exist, start from today
		startDate = todayTime
	} else {
		// Start from the day after the latest existing assignment
		latestDate, _ := time.Parse("2006-01-02", latestAssignmentDate)
		startDate = latestDate.AddDate(0, 0, 1)
	}

	// If start date is already beyond the 14-day window, nothing to do
	if startDate.After(endDate) {
		return false, nil
	}

	// Generate assignments for the gap
	return sm.GenerateMissingDays(ctx, startDate, endDate)
}

// GetScheduleGap calculates the date range that needs assignments.
// Returns start and end dates for the gap, or empty strings if no gap exists.
func (sm *ScheduleMaintenance) GetScheduleGap(ctx context.Context) (string, string, error) {
	today := time.Now().Format("2006-01-02")
	todayTime, err := time.Parse("2006-01-02", today)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse today date: %w", err)
	}

	latestAssignmentDate, err := sm.db.GetLatestAssignmentDate(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to get latest assignment date: %w", err)
	}

	startDate := todayTime
	if latestAssignmentDate != "" {
		startDate, err = time.Parse("2006-01-02", latestAssignmentDate)
		if err != nil {
			return "", "", fmt.Errorf("failed to parse latest assignment date %s: %w", latestAssignmentDate, err)
		}
		startDate = startDate.AddDate(0, 0, 1)
	}

	endDate := todayTime.AddDate(0, 0, scheduleDaysAhead)

	if startDate.After(endDate) {
		return "", "", nil
	}

	hasBusinessDays, err := sm.hasBusinessGap(ctx, startDate, endDate)
	if err != nil {
		return "", "", err
	}
	if !hasBusinessDays {
		return "", "", nil
	}

	return startDate.Format("2006-01-02"), endDate.Format("2006-01-02"), nil
}

func (sm *ScheduleMaintenance) hasBusinessGap(ctx context.Context, startDate, endDate time.Time) (bool, error) {
	for currentDate := startDate; !currentDate.After(endDate); currentDate = currentDate.AddDate(0, 0, 1) {
		if currentDate.Weekday() == time.Saturday || currentDate.Weekday() == time.Sunday {
			continue
		}

		dateStr := currentDate.Format("2006-01-02")
		assignments, err := sm.db.GetAssignmentsByDate(ctx, dateStr)
		if err != nil {
			return false, fmt.Errorf("failed to check assignments for %s: %w", dateStr, err)
		}

		if len(assignments) == 0 {
			return true, nil
		}
	}

	return false, nil
}

// GenerateMissingDays generates assignments for a date range.
// This is the core method that implements "static as possible" scheduling.
// It only creates assignments for dates that don't have any assignments yet.
// Returns true if assignments were created, false otherwise.
func (sm *ScheduleMaintenance) GenerateMissingDays(ctx context.Context, startDate, endDate time.Time) (bool, error) {
	// Get existing assignments and validate
	datesWithAssignments, err := sm.getDatesWithAssignments(ctx, startDate, endDate)
	if err != nil {
		return false, err
	}

	// Get active team members
	members, err := sm.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get active team members: %w", err)
	}

	if len(members) == 0 {
		return false, errors.New("no active team members")
	}

	// Determine starting position in rotation
	memberIndex, err := sm.getStartingMemberIndex(ctx, startDate, members)
	if err != nil {
		return false, err
	}

	// Generate assignments for missing dates
	return sm.generateAssignmentsForMissingDates(ctx, startDate, endDate, datesWithAssignments, members, memberIndex)
}

// getDatesWithAssignments retrieves all dates that already have assignments in the given range.
func (sm *ScheduleMaintenance) getDatesWithAssignments(ctx context.Context, startDate, endDate time.Time) (map[string]bool, error) {
	existingAssignments, err := sm.db.GetAssignmentsByDateRange(
		ctx,
		startDate.Format("2006-01-02"),
		endDate.Format("2006-01-02"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing assignments: %w", err)
	}

	datesWithAssignments := make(map[string]bool)
	for _, assignment := range existingAssignments {
		datesWithAssignments[assignment.Date] = true
	}

	return datesWithAssignments, nil
}

// getStartingMemberIndex determines where to start in the rotation.
func (sm *ScheduleMaintenance) getStartingMemberIndex(ctx context.Context, startDate time.Time, members []database.TeamMember) (int, error) {
	// Find the last assigned member BEFORE the start date to continue the rotation
	dayBeforeStart := startDate.AddDate(0, 0, -1)
	assignmentsBefore, err := sm.db.GetAssignmentsByDateRange(
		ctx,
		dayBeforeStart.Format("2006-01-02"),
		dayBeforeStart.Format("2006-01-02"),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to get assignments before range: %w", err)
	}

	lastAssignedMemberID := sm.findLastAssignedMember(assignmentsBefore, members)
	memberIndex := sm.findMemberIndex(members, lastAssignedMemberID)

	// If we found a previous assignment, start from the next member
	// Otherwise start from the beginning
	if memberIndex != -1 {
		return (memberIndex + 1) % len(members), nil
	}
	return 0, nil
}

// generateAssignmentsForMissingDates creates assignments only for dates without them.
func (sm *ScheduleMaintenance) generateAssignmentsForMissingDates(
	ctx context.Context,
	startDate, endDate time.Time,
	datesWithAssignments map[string]bool,
	members []database.TeamMember,
	memberIndex int,
) (bool, error) {
	createdAny := false
	currentDate := startDate

	for currentDate.Before(endDate.AddDate(0, 0, 1)) {
		if sm.shouldSkipDate(currentDate, datesWithAssignments) {
			currentDate = currentDate.AddDate(0, 0, 1)
			continue
		}

		// The engine's processDate will handle holiday checking internally
		if err := sm.engine.processDate(ctx, currentDate, members, &memberIndex); err != nil {
			return createdAny, fmt.Errorf("failed to create assignment for %s: %w",
				currentDate.Format("2006-01-02"), err)
		}

		createdAny = true
		currentDate = currentDate.AddDate(0, 0, 1)
	}

	return createdAny, nil
}

// shouldSkipDate determines if a date should be skipped for assignment.
func (sm *ScheduleMaintenance) shouldSkipDate(date time.Time, datesWithAssignments map[string]bool) bool {
	dateStr := date.Format("2006-01-02")

	// Skip if this date already has assignments
	if datesWithAssignments[dateStr] {
		return true
	}

	// Skip weekends
	if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
		return true
	}

	// Skip holidays - this will be checked by the engine's holiday checker
	// The engine will handle holiday checking when processing dates
	return false
}

// HandleTeamChange processes team member changes and updates the schedule accordingly.
// When a member is added or removed, the rotation may need adjustment.
func (sm *ScheduleMaintenance) HandleTeamChange(ctx context.Context) error {
	// Simply ensure the schedule is complete
	// The rotation will naturally adjust based on current team members
	_, err := sm.EnsureSchedule(ctx)
	return err
}

// RegenerateSchedule creates a fresh schedule from scratch, replacing all existing assignments
// in the specified date range. This is useful for initial team setup or schedule resets.
func (sm *ScheduleMaintenance) RegenerateSchedule(ctx context.Context, start, end time.Time) (int, error) {
	// Delete existing assignments in the date range
	err := sm.db.DeleteAssignmentsInRange(ctx, start.Format("2006-01-02"), end.Format("2006-01-02"))
	if err != nil {
		return 0, fmt.Errorf("failed to delete existing assignments: %w", err)
	}

	// Use the engine's GenerateSchedule which handles all the logic correctly
	err = sm.engine.GenerateSchedule(ctx, start, end)
	if err != nil {
		return 0, err
	}

	// Count how many assignments were created
	assignments, err := sm.db.GetAssignmentsByDateRange(
		ctx,
		start.Format("2006-01-02"),
		end.Format("2006-01-02"),
	)
	if err != nil {
		return 0, err
	}

	return len(assignments), nil
}

// HandleLeaveChange processes leave changes and updates cover assignments.
// This reconciles covers for the specific leave and assigns new ones as needed.
func (sm *ScheduleMaintenance) HandleLeaveChange(ctx context.Context, leaveID string) error {
	// Get the leave to know which dates to reconcile
	leave, err := sm.db.GetLeaveByID(ctx, leaveID)
	if err != nil {
		return err
	}

	// Reconcile covers for this specific leave's date range
	if err := sm.reconcileCoversForDateRange(ctx, leave.StartDate, leave.EndDate); err != nil {
		return err
	}

	// Then assign covers for the updated leave if still active
	return sm.engine.AssignCoversForLeave(ctx, leaveID)
}

// findOriginalMemberID finds the member ID of the original assignment.
func (sm *ScheduleMaintenance) findOriginalMemberID(assignments []database.RotaAssignment, originalAssignmentID *string) string {
	if originalAssignmentID == nil {
		return ""
	}
	for i := range assignments {
		if assignments[i].ID == *originalAssignmentID {
			return assignments[i].MemberID
		}
	}
	return ""
}

// isMemberOnLeave checks if a member is on leave for a given date.
func (sm *ScheduleMaintenance) isMemberOnLeave(ctx context.Context, date string, memberID string) (bool, error) {
	leaves, err := sm.db.GetLeaveByDate(ctx, date)
	if err != nil {
		return false, err
	}
	for i := range leaves {
		if leaves[i].MemberID == memberID {
			return true, nil
		}
	}
	return false, nil
}

// reconcileCoversForDateRange removes stale covers for a specific date range.
func (sm *ScheduleMaintenance) reconcileCoversForDateRange(ctx context.Context, startDate, endDate time.Time) error {
	// Get all assignments in the specified range
	assignments, err := sm.db.GetAssignmentsByDateRange(
		ctx,
		startDate.Format("2006-01-02"),
		endDate.Format("2006-01-02"),
	)
	if err != nil {
		return fmt.Errorf("failed to get assignments for reconciliation: %w", err)
	}

	// Check each cover assignment
	for i := range assignments {
		assignment := &assignments[i]
		if !assignment.IsCover || assignment.OriginalAssignmentID == nil {
			continue
		}

		// Find the original assignment
		originalMemberID := sm.findOriginalMemberID(assignments, assignment.OriginalAssignmentID)
		if originalMemberID == "" {
			continue
		}

		// Check if original person is still on leave for this date
		onLeave, err := sm.isMemberOnLeave(ctx, assignment.Date, originalMemberID)
		if err != nil {
			return fmt.Errorf("failed to check leave for date %s: %w", assignment.Date, err)
		}

		// If not on leave (or leave is completed/deleted), remove the cover
		if !onLeave {
			if err := sm.db.DeleteRotaAssignment(ctx, assignment.ID); err != nil {
				return fmt.Errorf("failed to remove stale cover %s: %w", assignment.ID, err)
			}
		}
	}

	return nil
}

// findLastAssignedMember finds the last member who was assigned in the existing schedule.
// Returns the ID of the last assigned member, or empty string if no assignments exist.
func (sm *ScheduleMaintenance) findLastAssignedMember(assignments []database.RotaAssignment, _ []database.TeamMember) string {
	if len(assignments) == 0 {
		return ""
	}

	// Find the latest assignment date
	latestDate := ""
	latestAssignment := database.RotaAssignment{}
	for _, a := range assignments {
		if a.Date > latestDate {
			latestDate = a.Date
			latestAssignment = a
		}
	}

	// Return the member ID from the latest assignment
	return latestAssignment.MemberID
}

// findMemberIndex finds the index of a member in the members slice.
// Returns -1 if not found.
func (sm *ScheduleMaintenance) findMemberIndex(members []database.TeamMember, memberID string) int {
	if memberID == "" {
		return -1
	}
	for i, m := range members {
		if m.ID == memberID {
			return i
		}
	}
	return -1
}
