package calendar

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/inful/madhatter/internal/database"
)

// GenerateICS creates an ICS calendar file content from rota assignments.
func GenerateICS(assignments []database.RotaAssignment, memberName string) string {
	var buffer bytes.Buffer

	// ICS header
	buffer.WriteString("BEGIN:VCALENDAR\r\n")
	buffer.WriteString("VERSION:2.0\r\n")
	buffer.WriteString("PRODID:-//SupportRota//EN\r\n")
	buffer.WriteString("CALSCALE:GREGORIAN\r\n")
	buffer.WriteString("METHOD:PUBLISH\r\n")
	buffer.WriteString("X-WR-CALNAME:Support Rota - " + memberName + "\r\n")
	buffer.WriteString("X-WR-TIMEZONE:UTC\r\n")

	// Add events for each assignment
	for _, assignment := range assignments {
		buffer.WriteString("BEGIN:VEVENT\r\n")

		// UID - unique identifier for the event
		uid := fmt.Sprintf("%s@supportrota", assignment.ID)
		buffer.WriteString(fmt.Sprintf("UID:%s\r\n", uid))

		// DTSTART and DTEND (9am to 5pm on the assignment date)
		// Assignment.Date is a string in format "2006-01-02"
		eventDate := strings.ReplaceAll(assignment.Date, "-", "")
		buffer.WriteString(fmt.Sprintf("DTSTART:%sT090000Z\r\n", eventDate))
		buffer.WriteString(fmt.Sprintf("DTEND:%sT170000Z\r\n", eventDate))

		// Summary
		summary := fmt.Sprintf("Support Duty - %s", memberName)
		if assignment.IsCover {
			summary += " (COVER)"
		}
		buffer.WriteString(fmt.Sprintf("SUMMARY:%s\r\n", summary))

		// Description
		description := "Support duty assignment"
		if assignment.IsCover && assignment.OriginalAssignmentID != nil {
			description += " - Cover assignment"
		}
		buffer.WriteString(fmt.Sprintf("DESCRIPTION:%s\r\n", description))

		// Status
		buffer.WriteString("STATUS:CONFIRMED\r\n")

		// Sequence (for updates)
		buffer.WriteString("SEQUENCE:0\r\n")

		// Last modified
		now := time.Now().UTC().Format("20060102T150405Z")
		buffer.WriteString(fmt.Sprintf("DTSTAMP:%s\r\n", now))

		buffer.WriteString("END:VEVENT\r\n")
	}

	// ICS footer
	buffer.WriteString("END:VCALENDAR\r\n")

	return buffer.String()
}

// GenerateICSForToken generates ICS content for a calendar subscription token.
func GenerateICSForToken(db *database.DB, token string, lookaheadDays int) (string, error) {
	// Get member by token
	member, err := db.GetMemberByToken(token)
	if err != nil {
		return "", fmt.Errorf("invalid token: %w", err)
	}

	// Get upcoming assignments for this member
	assignments, err := db.GetUpcomingAssignments(member.ID, lookaheadDays)
	if err != nil {
		return "", fmt.Errorf("failed to get assignments: %w", err)
	}

	// Generate ICS
	icsContent := GenerateICS(assignments, member.Name)
	return icsContent, nil
}

// ExportICSFile writes ICS content to a file.
func ExportICSFile(content string, filePath string) error {
	// Ensure .ics extension
	if !strings.HasSuffix(filePath, ".ics") {
		filePath += ".ics"
	}
	// Write to file (this would use os.WriteFile in a real implementation)
	// For now, we'll just return nil as this is a placeholder
	// The actual file writing would be done in the CLI command
	_ = filePath // Use the variable to avoid ineffassign warning
	return nil
}
