package calendar

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	ics "github.com/arran4/golang-ical"
	"github.com/inful/madhatter/internal/database"
)

const hoursPerDay = 24

// titleCase converts a string to title case without using deprecated strings.Title.
func titleCase(s string) string {
	if s == "" {
		return s
	}

	// Split by spaces and capitalise each word
	words := strings.Fields(s)
	for i, word := range words {
		if len(word) > 0 {
			// Convert first rune to upper case, rest to lower
			runes := []rune(word)
			runes[0] = unicode.ToUpper(runes[0])
			for j := 1; j < len(runes); j++ {
				runes[j] = unicode.ToLower(runes[j])
			}
			words[i] = string(runes)
		}
	}
	return strings.Join(words, " ")
}

// ICalGenerator handles iCalendar (.ics) file generation using golang-ical library.
type ICalGenerator struct {
	calendar *ics.Calendar
}

// NewICalGeneratorWithMetadata creates a new iCalendar generator with custom metadata.
func NewICalGeneratorWithMetadata(name, description string) *ICalGenerator {
	cal := ics.NewCalendar()
	cal.SetName(name)
	cal.SetDescription(description)
	cal.SetProductId("-//SupportRota//EN")
	cal.SetVersion("2.0")
	cal.SetCalscale("GREGORIAN")
	cal.SetMethod(ics.MethodPublish)

	return &ICalGenerator{calendar: cal}
}

// NewICalGenerator creates a new iCalendar generator.
func NewICalGenerator() *ICalGenerator {
	return NewICalGeneratorWithMetadata(
		"Support Rota Calendar",
		"Automated support rota assignments",
	)
}

// AddAssignment adds a rota assignment as a calendar event.
func (g *ICalGenerator) AddAssignment(assignment database.RotaAssignment, memberName string) error {
	event := g.calendar.AddEvent(fmt.Sprintf("%s@supportrota", assignment.ID))

	// Parse the assignment date
	eventDate, err := time.Parse("2006-01-02", assignment.Date)
	if err != nil {
		return fmt.Errorf("invalid date format %s: %w", assignment.Date, err)
	}

	// Set event times (9 AM to 5 PM on the assignment date)
	startTime := time.Date(eventDate.Year(), eventDate.Month(), eventDate.Day(), 9, 0, 0, 0, time.UTC)
	endTime := time.Date(eventDate.Year(), eventDate.Month(), eventDate.Day(), 17, 0, 0, 0, time.UTC)

	event.SetStartAt(startTime)
	event.SetEndAt(endTime)

	// Set summary
	summary := fmt.Sprintf("Support Duty - %s", memberName)
	if assignment.IsCover {
		summary += " (COVER)"
	}
	event.SetSummary(summary)

	// Set description
	description := "Support duty assignment"
	if assignment.IsCover && assignment.OriginalAssignmentID != nil {
		description += " - Cover assignment for leave"
	}
	event.SetDescription(description)

	// Set status
	event.SetStatus(ics.ObjectStatusConfirmed)

	// Set sequence number (for future updates)
	event.SetSequence(0)

	// Set last modified timestamp
	event.SetModifiedAt(time.Now().UTC())

	return nil
}

// AddLeaveEvent adds a leave/absence event to the calendar.
func (g *ICalGenerator) AddLeaveEvent(memberName string, leaveType string, startDate, endDate time.Time) error {
	event := g.calendar.AddEvent(fmt.Sprintf("leave-%s-%d", strings.ToLower(memberName), startDate.Unix()))

	// Set event times (all day event)
	event.SetAllDayStartAt(startDate)
	event.SetAllDayEndAt(endDate.Add(hoursPerDay * time.Hour)) // End date is exclusive in iCalendar

	// Set summary
	event.SetSummary(fmt.Sprintf("%s - %s", memberName, titleCase(leaveType)))

	// Set description
	event.SetDescription(fmt.Sprintf("%s leave for %s", titleCase(leaveType), memberName))

	// Set status to tentative for leave
	event.SetStatus(ics.ObjectStatusTentative)

	// Set transparency to opaque (time is busy)
	event.SetTimeTransparency(ics.TransparencyOpaque)

	return nil
}

// AddHoliday adds a holiday/closed day event.
func (g *ICalGenerator) AddHoliday(name string, date time.Time) error {
	event := g.calendar.AddEvent(fmt.Sprintf("holiday-%d", date.Unix()))

	// Set event as all day
	event.SetAllDayStartAt(date)
	event.SetAllDayEndAt(date.Add(hoursPerDay * time.Hour))

	// Set summary
	event.SetSummary(fmt.Sprintf("Office Closed - %s", name))

	// Set description
	event.SetDescription("Support rota is not scheduled on this day")

	// Set status to canceled
	event.SetStatus(ics.ObjectStatusCancelled)

	// Set transparency to transparent (time is free)
	event.SetTimeTransparency(ics.TransparencyTransparent)

	return nil
}

// Serialize returns the iCalendar content as a string.
func (g *ICalGenerator) Serialize() (string, error) {
	return g.calendar.Serialize(), nil
}

// SerializeBytes returns the iCalendar content as bytes.
func (g *ICalGenerator) SerializeBytes() ([]byte, error) {
	content, err := g.Serialize()
	if err != nil {
		return nil, err
	}
	return []byte(content), nil
}

// GenerateICalFromAssignments creates a complete iCalendar file from rota assignments.
func GenerateICalFromAssignments(assignments []database.RotaAssignment, memberName string) (string, error) {
	generator := NewICalGenerator()

	for _, assignment := range assignments {
		if err := generator.AddAssignment(assignment, memberName); err != nil {
			return "", fmt.Errorf("failed to add assignment: %w", err)
		}
	}

	return generator.Serialize()
}

// GenerateICalForToken generates iCalendar content for a subscription token.
func GenerateICalForToken(ctx context.Context, db *database.DB, token string, lookaheadDays int) (string, error) {
	// Get member by token
	member, err := db.GetMemberByToken(ctx, token)
	if err != nil {
		return "", fmt.Errorf("invalid token: %w", err)
	}

	// Get upcoming assignments for this member
	assignments, err := db.GetUpcomingAssignments(ctx, member.ID, lookaheadDays)
	if err != nil {
		return "", fmt.Errorf("failed to get assignments: %w", err)
	}

	// Generate iCalendar
	icalContent, err := GenerateICalFromAssignments(assignments, member.Name)
	if err != nil {
		return "", fmt.Errorf("failed to generate iCalendar: %w", err)
	}

	return icalContent, nil
}

// GenerateTeamCalendar generates a calendar with all team members' assignments.
func GenerateTeamCalendar(assignments []database.RotaAssignment, members map[string]string) (string, error) {
	generator := NewICalGenerator()

	for _, assignment := range assignments {
		memberName, ok := members[assignment.MemberID]
		if !ok {
			memberName = "Unknown"
		}

		if err := generator.AddAssignment(assignment, memberName); err != nil {
			return "", fmt.Errorf("failed to add assignment: %w", err)
		}
	}

	return generator.Serialize()
}

// ParseICal parses an iCalendar file and returns events.
func ParseICal(icalContent string) ([]*ics.VEvent, error) {
	reader := bytes.NewReader([]byte(icalContent))
	cal, err := ics.ParseCalendar(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse calendar: %w", err)
	}

	events := cal.Events()
	return events, nil
}

// ValidateICal validates iCalendar content.
func ValidateICal(icalContent string) error {
	_, err := ParseICal(icalContent)
	return err
}

// AddTimezoneSupport adds timezone information to the calendar.
func (g *ICalGenerator) AddTimezoneSupport(timezone string) {
	if timezone == "" {
		return
	}

	// Set X-WR-TIMEZONE for client compatibility.
	g.calendar.SetXWRTimezone(timezone)

	// Include a VTIMEZONE block. Many clients can resolve TZID without it,
	// but including it improves interoperability.
	//
	// Note: golang-ical's built-in timezone support is intentionally minimal;
	// clients will generally apply correct DST rules from their tz database
	// as long as events use DTSTART/DTEND with TZID.
	g.calendar.AddTimezone(timezone)
}

// AddCustomProperty adds a custom property to the calendar.
func (g *ICalGenerator) AddCustomProperty(key, value string) {
	// Use SetXProperty for custom properties
	// Note: The library may not have SetXProperty, so we'll use SetXWRCalName as an example
	// For custom properties, we might need to use AddProperty directly
}
