package rota

import (
	"errors"
	"time"

	"github.com/inful/madhatter/internal/database"
)

type Engine struct {
	db *database.DB
}

func NewEngine(db *database.DB) *Engine {
	return &Engine{db: db}
}

// GenerateSchedule creates round-robin assignments for a date range.
func (e *Engine) GenerateSchedule(startDate, endDate time.Time) error {
	members, err := e.db.GetActiveTeamMembers()
	if err != nil {
		return err
	}

	if len(members) == 0 {
		return errors.New("no active team members")
	}

	currentDate := startDate
	memberIndex := 0

	for currentDate.Before(endDate.AddDate(0, 0, 1)) {
		if err := e.processDate(currentDate, members, &memberIndex); err != nil {
			return err
		}
		currentDate = currentDate.AddDate(0, 0, 1)
	}

	return nil
}

// processDate handles assignment for a single date.
func (e *Engine) processDate(currentDate time.Time, members []database.TeamMember, memberIndex *int) error {
	// Skip weekends
	if currentDate.Weekday() == time.Saturday || currentDate.Weekday() == time.Sunday {
		return nil
	}

	dateStr := currentDate.Format("2006-01-02")
	leaves, err := e.db.GetLeaveByDate(dateStr)
	if err != nil {
		return err
	}

	originalMember := members[*memberIndex]
	coveringMember := e.determineCoveringMember(originalMember, leaves, members, *memberIndex)

	if err := e.createAssignment(dateStr, originalMember, coveringMember, leaves); err != nil {
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
func (e *Engine) createAssignment(dateStr string, originalMember, coveringMember database.TeamMember, _ []database.LeaveRecord) error {
	isCover := coveringMember.ID != originalMember.ID

	if isCover {
		// For cover assignments, we need to:
		// 1. Create the original assignment for the person on leave
		// 2. Create the cover assignment that references the original
		originalAssignmentID, err := e.db.CreateRotaAssignment(dateStr, originalMember.ID, false, nil)
		if err != nil {
			return err
		}

		// Create the cover assignment
		_, err = e.db.CreateRotaAssignment(dateStr, coveringMember.ID, true, &originalAssignmentID)
		return err
	}

	// For non-cover assignments, just create normally
	_, err := e.db.CreateRotaAssignment(dateStr, coveringMember.ID, false, nil)
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
func (e *Engine) AssignCoversForLeave(leaveID string) error {
	leave, err := e.db.GetLeaveByID(leaveID)
	if err != nil {
		return err
	}

	members, err := e.db.GetActiveTeamMembers()
	if err != nil {
		return err
	}

	originalIndex := e.findMemberIndex(members, leave.MemberID)
	if originalIndex == -1 {
		return errors.New("member not found")
	}

	return e.processLeaveDates(leave, members, originalIndex, leaveID)
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

// processLeaveDates processes each day of leave and creates cover assignments.
func (e *Engine) processLeaveDates(leave *database.LeaveRecord, members []database.TeamMember, originalIndex int, leaveID string) error {
	for d := leave.StartDate; d.Before(leave.EndDate.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
		if err := e.processLeaveDate(d, members, originalIndex, leave, leaveID); err != nil {
			return err
		}
	}
	return nil
}

// processLeaveDate handles a single day of leave.
func (e *Engine) processLeaveDate(d time.Time, members []database.TeamMember, originalIndex int, leave *database.LeaveRecord, leaveID string) error {
	// Skip weekends
	if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		return nil
	}

	dateStr := d.Format("2006-01-02")
	originalAssignmentID, err := e.ensureOriginalAssignment(dateStr, leave)
	if err != nil {
		return err
	}

	cover, err := e.findCover(members, []database.LeaveRecord{*leave}, originalIndex)
	if err != nil {
		// Skip if no cover available - this is intentional
		return nil //nolint:nilerr
	}

	if err := e.createCoverAssignment(dateStr, cover.ID, originalAssignmentID); err != nil {
		return err
	}

	return e.db.UpdateLeaveStatus(leaveID, "assigned")
}

// ensureOriginalAssignment finds or creates the original assignment for the person on leave.
func (e *Engine) ensureOriginalAssignment(dateStr string, leave *database.LeaveRecord) (string, error) {
	existingAssignments, err := e.db.GetAssignmentsByDate(dateStr)
	if err != nil {
		return "", err
	}

	for _, a := range existingAssignments {
		if a.MemberID == leave.MemberID && !a.IsCover {
			return a.ID, nil
		}
	}

	return e.db.CreateRotaAssignment(dateStr, leave.MemberID, false, nil)
}

// createCoverAssignment creates a cover assignment.
func (e *Engine) createCoverAssignment(dateStr, coverMemberID, originalAssignmentID string) error {
	_, err := e.db.CreateRotaAssignment(dateStr, coverMemberID, true, &originalAssignmentID)
	return err
}
