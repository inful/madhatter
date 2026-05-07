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

// SetHolidayChecker configures holiday-aware skipping for maintenance operations.
func (sm *ScheduleMaintenance) SetHolidayChecker(checker HolidayChecker) {
	sm.engine.SetHolidayChecker(checker)
}

// EnsureSchedule guarantees that a schedule exists for the next 14 days from today.
// It scans the entire 14-day window and fills any gaps, not just at the end.
// This handles cases where assignments were deleted (e.g., team member removed).
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

	// Generate assignments for the entire 14-day window, filling any gaps
	// This ensures that deleted assignments (e.g., from removed team members) are replaced
	return sm.GenerateMissingDays(ctx, todayTime, endDate)
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
// It walks backwards from startDate (up to lookbackDays calendar days) looking
// for the most recent *non-cover* assignment on a business day so that the
// rotation anchor is always deterministic, unaffected by weekend boundaries or
// the presence of cover assignments on the preceding day.
func (sm *ScheduleMaintenance) getStartingMemberIndex(ctx context.Context, startDate time.Time, members []database.TeamMember) (int, error) {
	const lookbackDays = 30

	for i := 1; i <= lookbackDays; i++ {
		day := startDate.AddDate(0, 0, -i)

		// Skip weekends - they never have original assignments.
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}

		// Skip holidays if a checker is configured.
		if sm.engine.holidayChecker != nil && sm.engine.holidayChecker(day) {
			continue
		}

		assignments, err := sm.db.GetAssignmentsByDateRange(
			ctx,
			day.Format("2006-01-02"),
			day.Format("2006-01-02"),
		)
		if err != nil {
			return 0, fmt.Errorf("failed to get assignments before range: %w", err)
		}

		// Find a non-cover original assignment - this is the R1 rotation anchor.
		for _, a := range assignments {
			if !a.IsCover {
				memberIndex := sm.findMemberIndex(members, a.MemberID)
				if memberIndex != -1 {
					return (memberIndex + 1) % len(members), nil
				}
			}
		}
	}

	// No prior original assignment found; start the rotation from the beginning.
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

	// Skip if this date already has assignments.
	if datesWithAssignments[dateStr] {
		return true
	}

	// Skip weekends.
	if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
		return true
	}

	// Skip holidays so that processDate is never called on them and createdAny
	// is only set when an assignment is actually written.
	if sm.engine.holidayChecker != nil && sm.engine.holidayChecker(date) {
		return true
	}

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
	// Get active team members before rebuilding the range.
	members, err := sm.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get active team members: %w", err)
	}

	if len(members) == 0 {
		return 0, errors.New("no active team members")
	}

	memberIndex, err := sm.getStartingMemberIndex(ctx, start, members)
	if err != nil {
		return 0, err
	}

	// Delete existing assignments in the date range
	err = sm.db.DeleteAssignmentsInRange(ctx, start.Format("2006-01-02"), end.Format("2006-01-02"))
	if err != nil {
		return 0, fmt.Errorf("failed to delete existing assignments: %w", err)
	}

	for currentDate := start; !currentDate.After(end); currentDate = currentDate.AddDate(0, 0, 1) {
		if err = sm.engine.processDate(ctx, currentDate, members, &memberIndex); err != nil {
			return 0, fmt.Errorf("failed to regenerate assignment for %s: %w",
				currentDate.Format("2006-01-02"), err)
		}
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

// HandleLeaveDelete deletes a leave record and restores the original schedule for
// the affected date range. It must be called instead of deleting the record directly
// so that orphaned cover/placeholder rows are cleaned up before GenerateMissingDays
// refills those slots with the correct round-robin assignments.
func (sm *ScheduleMaintenance) HandleLeaveDelete(ctx context.Context, leaveID string) error {
	// Read dates before deletion so we know which range to reconcile afterwards.
	leave, err := sm.db.GetLeaveByID(ctx, leaveID)
	if err != nil {
		return fmt.Errorf("failed to get leave record: %w", err)
	}

	startDate := leave.StartDate
	endDate := leave.EndDate

	if err := sm.db.DeleteLeaveRecord(ctx, leaveID); err != nil {
		return fmt.Errorf("failed to delete leave record: %w", err)
	}

	// Remove stale covers (and their paired placeholder rows) for the leave window.
	if err := sm.reconcileCoversForDateRange(ctx, startDate, endDate); err != nil {
		return fmt.Errorf("failed to reconcile covers after leave deletion: %w", err)
	}

	// Refill any now-empty slots in the leave window with the correct assignments.
	if _, err := sm.GenerateMissingDays(ctx, startDate, endDate); err != nil {
		return fmt.Errorf("failed to regenerate assignments after leave deletion: %w", err)
	}

	return nil
}

// reconcileCoversForDateRange removes stale covers for a specific date range.
func (sm *ScheduleMaintenance) reconcileCoversForDateRange(ctx context.Context, startDate, endDate time.Time) error {
	leaveIndex, err := sm.buildLeaveIndex(ctx, startDate, endDate)
	if err != nil {
		return err
	}

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

		// Check if original person is still on leave for this date using preloaded index
		membersOnDate := leaveIndex[assignment.Date]
		onLeave := membersOnDate != nil && membersOnDate[originalMemberID]

		// If not on leave (or leave is completed/deleted), remove the cover
		if !onLeave {
			if err := sm.db.DeleteRotaAssignment(ctx, assignment.ID); err != nil {
				return fmt.Errorf("failed to remove stale cover %s: %w", assignment.ID, err)
			}
		}
	}

	return nil
}

func (sm *ScheduleMaintenance) buildLeaveIndex(ctx context.Context, startDate, endDate time.Time) (map[string]map[string]bool, error) {
	leaveIndex := make(map[string]map[string]bool)

	for current := startDate; !current.After(endDate); current = current.AddDate(0, 0, 1) {
		dateStr := current.Format("2006-01-02")
		leaves, err := sm.db.GetLeaveByDate(ctx, dateStr)
		if err != nil {
			return nil, fmt.Errorf("failed to load leaves for %s: %w", dateStr, err)
		}

		if len(leaves) == 0 {
			continue
		}

		membersOnDate := make(map[string]bool, len(leaves))
		for i := range leaves {
			membersOnDate[leaves[i].MemberID] = true
		}

		leaveIndex[dateStr] = membersOnDate
	}

	return leaveIndex, nil
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
