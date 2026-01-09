package rota

import (
	"context"
	"errors"
	"time"

	"github.com/inful/madhatter/internal/database"
)

// HolidayChecker is a function that checks if a date should be skipped due to holidays.
type HolidayChecker func(date time.Time) bool

type Engine struct {
	db             *database.DB
	holidayChecker HolidayChecker
}

func NewEngine(db *database.DB) *Engine {
	return &Engine{
		db:             db,
		holidayChecker: nil,
	}
}

// SetHolidayChecker sets a function that checks if dates are holidays.
func (e *Engine) SetHolidayChecker(checker HolidayChecker) {
	e.holidayChecker = checker
}

// GenerateSchedule creates round-robin assignments for a date range.
func (e *Engine) GenerateSchedule(ctx context.Context, startDate, endDate time.Time) error {
	members, err := e.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return err
	}

	if len(members) == 0 {
		return errors.New("no active team members")
	}

	currentDate := startDate
	memberIndex := 0

	for currentDate.Before(endDate.AddDate(0, 0, 1)) {
		if err := e.processDate(ctx, currentDate, members, &memberIndex); err != nil {
			return err
		}
		currentDate = currentDate.AddDate(0, 0, 1)
	}

	return nil
}

// processDate handles assignment for a single date.
func (e *Engine) processDate(ctx context.Context, currentDate time.Time, members []database.TeamMember, memberIndex *int) error {
	// Skip weekends
	if currentDate.Weekday() == time.Saturday || currentDate.Weekday() == time.Sunday {
		return nil
	}

	// Skip holidays if holiday checker is configured
	if e.holidayChecker != nil && e.holidayChecker(currentDate) {
		return nil
	}

	dateStr := currentDate.Format("2006-01-02")
	leaves, err := e.db.GetLeaveByDate(ctx, dateStr)
	if err != nil {
		return err
	}

	originalMember := members[*memberIndex]
	coveringMember := e.determineCoveringMember(originalMember, leaves, members, *memberIndex)

	if err := e.createAssignment(ctx, dateStr, originalMember, coveringMember, leaves); err != nil {
		return err
	}

	*memberIndex = (*memberIndex + 1) % len(members)
	return nil
}

// determineCoveringMember finds who should cover the assignment.
func (e *Engine) determineCoveringMember(originalMember database.TeamMember, leaves []database.LeaveRecord, members []database.TeamMember, memberIndex int) database.TeamMember {
	for i := range leaves {
		if leaves[i].MemberID == originalMember.ID {
			cover, coverErr := e.findCover(members, leaves, memberIndex)
			if coverErr == nil {
				return cover
			}
			break
		}
	}
	return originalMember
}

// createAssignment creates the rota assignment.
func (e *Engine) createAssignment(ctx context.Context, dateStr string, originalMember, coveringMember database.TeamMember, _ []database.LeaveRecord) error {
	isCover := coveringMember.ID != originalMember.ID

	if isCover {
		// For cover assignments, we need to:
		// 1. Create the original assignment for the person on leave
		// 2. Create the cover assignment that references the original
		originalAssignmentID, err := e.db.CreateRotaAssignment(ctx, dateStr, originalMember.ID, false, nil)
		if err != nil {
			return err
		}

		// Create the cover assignment
		_, err = e.db.CreateRotaAssignment(ctx, dateStr, coveringMember.ID, true, &originalAssignmentID)
		return err
	}

	// For non-cover assignments, just create normally
	_, err := e.db.CreateRotaAssignment(ctx, dateStr, coveringMember.ID, false, nil)
	return err
}

// findCover finds the next available member for cover.
func (e *Engine) findCover(members []database.TeamMember, leaves []database.LeaveRecord, startIndex int) (database.TeamMember, error) {
	for i := 1; i <= len(members); i++ {
		candidateIndex := (startIndex + i) % len(members)
		candidate := members[candidateIndex]

		// Check if candidate is on leave
		onLeave := false
		for j := range leaves {
			if leaves[j].MemberID == candidate.ID {
				onLeave = true
				break
			}
		}

		if !onLeave {
			return candidate, nil
		}
	}

	return database.TeamMember{}, errors.New("no available cover found")
}

// AssignCoversForLeave creates cover assignments for a leave record.
func (e *Engine) AssignCoversForLeave(ctx context.Context, leaveID string) error {
	leave, err := e.db.GetLeaveByID(ctx, leaveID)
	if err != nil {
		return err
	}

	members, err := e.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return err
	}

	originalIndex := e.findMemberIndex(members, leave.MemberID)
	if originalIndex == -1 {
		return errors.New("member not found")
	}

	// Find the last cover assignment before this leave to continue fair rotation
	startIndex := e.getNextCoverIndex(ctx, members, originalIndex, leave.StartDate)

	return e.processLeaveDates(ctx, leave, members, startIndex, leaveID)
}

// findMemberIndex finds the index of a member in the members slice.
func (e *Engine) findMemberIndex(members []database.TeamMember, memberID string) int {
	for i := range members {
		if members[i].ID == memberID {
			return i
		}
	}
	return -1
}

// getNextCoverIndex finds the next fair index to start looking for cover.
// It looks at cover assignments before the given date and returns the index to start the search from.
// Note: findCover will start checking from (startIndex + 1), so we return (lastCoverIndex)
// to make it check (lastCoverIndex + 1) which is the next person after the last cover.
func (e *Engine) getNextCoverIndex(ctx context.Context, members []database.TeamMember, fallbackIndex int, beforeDate time.Time) int {
	// Get assignments before this date to find the last cover
	endDate := beforeDate.Format("2006-01-02")
	startDate := beforeDate.AddDate(-1, 0, 0).Format("2006-01-02") // Look back 1 year

	assignments, err := e.db.GetAssignmentsByDateRange(ctx, startDate, endDate)
	if err != nil || len(assignments) == 0 {
		return fallbackIndex
	}

	// Find the most recent cover assignment before this date
	var lastCoverMemberID string
	var lastCoverDate string

	for i := range assignments {
		if assignments[i].IsCover && assignments[i].Date < endDate {
			// Find the most recent cover by date
			if lastCoverDate == "" || assignments[i].Date > lastCoverDate {
				lastCoverDate = assignments[i].Date
				lastCoverMemberID = assignments[i].MemberID
			}
		}
	}

	// If we found a recent cover assignment, start from that person's index
	// findCover will add 1 to this, so it will check the next person
	if lastCoverMemberID != "" {
		lastCoverIndex := e.findMemberIndex(members, lastCoverMemberID)
		if lastCoverIndex != -1 {
			return lastCoverIndex
		}
	}

	return fallbackIndex
}

// processLeaveDates processes each day of leave and creates cover assignments.
func (e *Engine) processLeaveDates(ctx context.Context, leave *database.LeaveRecord, members []database.TeamMember, startIndex int, leaveID string) error {
	currentIndex := startIndex
	for d := leave.StartDate; d.Before(leave.EndDate.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
		newIndex, err := e.processLeaveDate(ctx, d, members, currentIndex, leave, leaveID)
		if err != nil {
			return err
		}
		// Update index for next day to continue fair rotation
		if newIndex != -1 {
			currentIndex = newIndex
		}
	}
	return nil
}

// processLeaveDate handles a single day of leave and returns the index of the cover member.
func (e *Engine) processLeaveDate(ctx context.Context, d time.Time, members []database.TeamMember, startIndex int, leave *database.LeaveRecord, leaveID string) (int, error) {
	// Skip weekends
	if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		return -1, nil
	}

	// Skip holidays if holiday checker is configured
	if e.holidayChecker != nil && e.holidayChecker(d) {
		return -1, nil
	}

	dateStr := d.Format("2006-01-02")
	originalAssignmentID, err := e.ensureOriginalAssignment(ctx, dateStr, leave)
	if err != nil {
		return -1, err
	}

	cover, err := e.findCover(members, []database.LeaveRecord{*leave}, startIndex)
	if err != nil {
		// Skip if no cover available - this is intentional
		return -1, nil //nolint:nilerr
	}

	if err := e.createCoverAssignment(ctx, dateStr, cover.ID, originalAssignmentID); err != nil {
		return -1, err
	}

	if err := e.db.UpdateLeaveStatus(ctx, leaveID, "assigned"); err != nil {
		return -1, err
	}

	// Return the index of the cover member for next iteration
	return e.findMemberIndex(members, cover.ID), nil
}

// ensureOriginalAssignment finds or creates the original assignment for the person on leave.
func (e *Engine) ensureOriginalAssignment(ctx context.Context, dateStr string, leave *database.LeaveRecord) (string, error) {
	existingAssignments, err := e.db.GetAssignmentsByDate(ctx, dateStr)
	if err != nil {
		return "", err
	}

	for _, a := range existingAssignments {
		if a.MemberID == leave.MemberID && !a.IsCover {
			return a.ID, nil
		}
	}

	return e.db.CreateRotaAssignment(ctx, dateStr, leave.MemberID, false, nil)
}

// createCoverAssignment creates a cover assignment.
func (e *Engine) createCoverAssignment(ctx context.Context, dateStr, coverMemberID, originalAssignmentID string) error {
	_, err := e.db.CreateRotaAssignment(ctx, dateStr, coverMemberID, true, &originalAssignmentID)
	return err
}
