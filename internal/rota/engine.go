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
		// Skip weekends
		if currentDate.Weekday() == time.Saturday || currentDate.Weekday() == time.Sunday {
			currentDate = currentDate.AddDate(0, 0, 1)
			continue
		}

		// Check for leave
		dateStr := currentDate.Format("2006-01-02")
		leaves, err := e.db.GetLeaveByDate(dateStr)
		if err != nil {
			return err
		}

		// Find available member
		originalMember := members[memberIndex]
		coveringMember := originalMember

		// Check if original member is on leave
		for i := range leaves {
			if leaves[i].MemberID == originalMember.ID {
				// Find cover
				cover, coverErr := e.findCover(members, leaves, memberIndex)
				if coverErr != nil {
					// No cover available, skip this day
					memberIndex = (memberIndex + 1) % len(members)
					currentDate = currentDate.AddDate(0, 0, 1)
					continue
				}
				coveringMember = cover
				break
			}
		}

		// Create assignment
		isCover := coveringMember.ID != originalMember.ID
		var originalAssignmentID *string
		if isCover {
			// Find the leave record for this date and member
			for i := range leaves {
				if leaves[i].MemberID == originalMember.ID {
					// Store leave ID temporarily - will be updated later
					leaveID := leaves[i].ID
					originalAssignmentID = &leaveID
					break
				}
			}
		}

		_, err = e.db.CreateRotaAssignment(dateStr, coveringMember.ID, isCover, originalAssignmentID)
		if err != nil {
			return err
		}

		// Move to next member
		memberIndex = (memberIndex + 1) % len(members)
		currentDate = currentDate.AddDate(0, 0, 1)
	}

	return nil
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
	// Get leave record
	leave, err := e.db.GetLeaveByID(leaveID)
	if err != nil {
		return err
	}

	// Get active members
	members, err := e.db.GetActiveTeamMembers()
	if err != nil {
		return err
	}

	// Find original member index
	originalIndex := -1
	for i := range members {
		if members[i].ID == leave.MemberID {
			originalIndex = i
			break
		}
	}

	if originalIndex == -1 {
		return errors.New("member not found")
	}

	// Dates are now time.Time, no parsing needed
	startDate := leave.StartDate
	endDate := leave.EndDate

	// Process each day of leave
	for d := startDate; d.Before(endDate.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
		// Skip weekends
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}

		dateStr := d.Format("2006-01-02")

		// Get existing assignments for this date
		existingAssignments, err := e.db.GetAssignmentsByDate(dateStr)
		if err != nil {
			return err
		}

		// Find the original assignment for the person on leave
		var originalAssignmentID string
		hasOriginalAssignment := false
		for _, a := range existingAssignments {
			if a.MemberID == leave.MemberID && !a.IsCover {
				hasOriginalAssignment = true
				originalAssignmentID = a.ID
				break
			}
		}

		// If no original assignment exists, create one first
		if !hasOriginalAssignment {
			newAssignmentID, createErr := e.db.CreateRotaAssignment(dateStr, leave.MemberID, false, nil)
			if createErr != nil {
				return createErr
			}
			originalAssignmentID = newAssignmentID
		}

		// Find cover
		cover, err := e.findCover(members, []database.LeaveRecord{*leave}, originalIndex)
		if err != nil {
			continue // Skip if no cover available
		}

		// Create cover assignment referencing the original assignment
		_, err = e.db.CreateRotaAssignment(dateStr, cover.ID, true, &originalAssignmentID)
		if err != nil {
			return err
		}

		// Update leave record status
		if err := e.db.UpdateLeaveStatus(leaveID, "assigned"); err != nil {
			return err
		}
	}

	return nil
}
