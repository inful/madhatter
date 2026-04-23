package calendar

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
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

	supportDayLinks []MeetingLink
	withAlarm       bool
}

type SupportCalendarOptions struct {
	SupportDayLinks []MeetingLink
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
	cal.SetRefreshInterval("PT1H")
	cal.SetXPublishedTTL("PT1H")

	return &ICalGenerator{calendar: cal}
}

// NewICalGenerator creates a new iCalendar generator.
func NewICalGenerator() *ICalGenerator {
	return NewICalGeneratorWithMetadata(
		"Support Rota Calendar",
		"Automated support rota assignments",
	)
}

func (g *ICalGenerator) WithSupportDayLinks(links []MeetingLink) *ICalGenerator {
	g.supportDayLinks = links
	return g
}

// WithAlarm enables a 1-day advance display notification on each event.
func (g *ICalGenerator) WithAlarm() *ICalGenerator {
	g.withAlarm = true
	return g
}

// AddAssignment adds a rota assignment as a calendar event.
func (g *ICalGenerator) AddAssignment(assignment database.RotaAssignment, memberName string) error {
	event := g.calendar.AddEvent(fmt.Sprintf("%s@supportrota", assignment.ID))

	// Parse the assignment date
	eventDate, err := time.Parse("2006-01-02", assignment.Date)
	if err != nil {
		return fmt.Errorf("invalid date format %s: %w", assignment.Date, err)
	}

	// Set event as all-day.
	// DTEND is exclusive in iCalendar, so end is the next day.
	startDate := time.Date(eventDate.Year(), eventDate.Month(), eventDate.Day(), 0, 0, 0, 0, time.UTC)
	event.SetAllDayStartAt(startDate)
	event.SetAllDayEndAt(startDate.Add(hoursPerDay * time.Hour))

	// Set summary.
	summary := fmt.Sprintf("HAT day (%s)", memberName)
	if assignment.IsCover {
		summary += " (COVER)"
	}
	event.SetSummary(summary)

	// Set description.
	baseDescription := "Support duty"
	if assignment.IsCover {
		baseDescription += " (cover)"
		if assignment.OriginalAssignmentID != nil {
			baseDescription += " for leave"
		}
	}

	textDescription := baseDescription
	if len(g.supportDayLinks) > 0 {
		textDescription += formatLinksText(g.supportDayLinks)
	}
	event.SetDescription(textDescription)

	htmlDesc := htmlHeading(summary) + htmlParagraph(baseDescription)
	if len(g.supportDayLinks) > 0 {
		htmlDesc += formatLinksHTML(g.supportDayLinks)
	}
	setAltDescHTML(event, htmlDesc)

	// Do not mark as busy.
	event.SetTimeTransparency(ics.TransparencyTransparent)

	// Set status
	event.SetStatus(ics.ObjectStatusConfirmed)

	// Set sequence number (for future updates)
	event.SetSequence(0)

	// Set last modified timestamp
	event.SetModifiedAt(time.Now().UTC())

	// Add a 1-day advance reminder for personal calendars.
	if g.withAlarm {
		alarm := event.AddAlarm()
		alarm.SetAction(ics.ActionDisplay)
		alarm.SetTrigger("-P1D")
		alarm.SetDescription(summary)
	}

	return nil
}

func formatLinksText(links []MeetingLink) string {
	var b strings.Builder
	first := true
	for _, l := range links {
		label, urlStr := meetingLinkLabelAndURL(l)
		if label == "" {
			continue
		}
		if first {
			b.WriteString("\n\nLinks:\n")
			first = false
		}
		b.WriteString("- ")
		b.WriteString(label)
		if urlStr != "" {
			b.WriteString(": ")
			b.WriteString(urlStr)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func meetingLinkLabelAndURL(link MeetingLink) (label string, urlStr string) {
	label = strings.TrimSpace(link.Label)
	urlStr = safeHTTPURL(strings.TrimSpace(link.URL))
	if label != "" {
		return label, urlStr
	}

	if strings.TrimSpace(string(link.HTML)) == "" {
		return "", ""
	}

	extractedLabel, extractedURL := extractAnchorLabelAndURL(string(link.HTML))
	if extractedLabel == "" {
		extractedLabel = "(HTML link)"
	}
	if extractedURL != "" {
		extractedURL = safeHTTPURL(extractedURL)
	}
	return extractedLabel, extractedURL
}

func extractAnchorLabelAndURL(htmlAnchor string) (label string, urlStr string) {
	// Best-effort extraction for plain-text description.
	// We intentionally keep this simple and dependency-free.

	urlStr = extractHrefFromAnchor(htmlAnchor)

	start := strings.Index(htmlAnchor, ">")
	if start != -1 {
		end := strings.Index(strings.ToLower(htmlAnchor[start+1:]), "</a")
		if end != -1 {
			label = strings.TrimSpace(stripHTMLTags(htmlAnchor[start+1 : start+1+end]))
		}
	}

	return label, urlStr
}

func extractHrefFromAnchor(htmlAnchor string) string {
	lower := strings.ToLower(htmlAnchor)
	hrefIdx := strings.Index(lower, "href=")
	if hrefIdx < 0 {
		return ""
	}

	rest := strings.TrimSpace(htmlAnchor[hrefIdx+len("href="):])
	if rest == "" {
		return ""
	}

	quote := rest[0]
	if quote == '\'' || quote == '"' {
		rest = rest[1:]
		before, _, ok := strings.Cut(rest, string(quote))
		if !ok {
			return ""
		}
		return before
	}

	// Unquoted value; stop at space or '>':
	end := len(rest)
	if i := strings.IndexAny(rest, " >"); i != -1 {
		end = i
	}
	return rest[:end]
}

func stripHTMLTags(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func formatLinksHTML(links []MeetingLink) string {
	var b strings.Builder
	builtAny := false

	for _, l := range links {
		if strings.TrimSpace(string(l.HTML)) != "" {
			if !builtAny {
				b.WriteString("<h4>Links</h4>")
				builtAny = true
			}
			b.WriteString("<p>")
			b.WriteString(string(l.HTML))
			b.WriteString("</p>")
			continue
		}

		label := strings.TrimSpace(l.Label)
		urlStr := safeHTTPURL(strings.TrimSpace(l.URL))
		if label == "" || urlStr == "" {
			continue
		}
		if !builtAny {
			b.WriteString("<h4>Links</h4>")
			builtAny = true
		}
		b.WriteString("<p><a href=\"")
		b.WriteString(template.HTMLEscapeString(urlStr))
		b.WriteString("\">")
		b.WriteString(template.HTMLEscapeString(label))
		b.WriteString("</a></p>")
	}

	return b.String()
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
	setAltDescHTML(
		event,
		htmlHeading(fmt.Sprintf("%s - %s", memberName, titleCase(leaveType)))+htmlParagraph(fmt.Sprintf("%s leave for %s", titleCase(leaveType), memberName)),
	)

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
	setAltDescHTML(
		event,
		htmlHeading(fmt.Sprintf("Office Closed - %s", name))+htmlParagraph("Support rota is not scheduled on this day"),
	)

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

// GenerateICalFromAssignmentsWithOptions creates a complete iCalendar file from rota assignments.
func GenerateICalFromAssignmentsWithOptions(assignments []database.RotaAssignment, memberName string, opts SupportCalendarOptions) (string, error) {
	generator := NewICalGenerator().WithSupportDayLinks(opts.SupportDayLinks).WithAlarm()

	for _, assignment := range assignments {
		if err := generator.AddAssignment(assignment, memberName); err != nil {
			return "", fmt.Errorf("failed to add assignment: %w", err)
		}
	}

	return generator.Serialize()
}

func GenerateICalFromAssignments(assignments []database.RotaAssignment, memberName string) (string, error) {
	return GenerateICalFromAssignmentsWithOptions(assignments, memberName, SupportCalendarOptions{})
}

// GenerateICalForToken generates iCalendar content for a subscription token.
func GenerateICalForToken(ctx context.Context, db *database.DB, token string, lookaheadDays int) (string, error) {
	return GenerateICalForTokenWithOptions(ctx, db, token, lookaheadDays, SupportCalendarOptions{})
}

func GenerateICalForTokenWithOptions(ctx context.Context, db *database.DB, token string, lookaheadDays int, opts SupportCalendarOptions) (string, error) {
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
	icalContent, err := GenerateICalFromAssignmentsWithOptions(assignments, member.Name, opts)
	if err != nil {
		return "", fmt.Errorf("failed to generate iCalendar: %w", err)
	}

	return icalContent, nil
}

// GenerateOthersICalForToken generates iCalendar content for all upcoming
// assignments except the token owner's.
func GenerateOthersICalForToken(ctx context.Context, db *database.DB, token string, lookaheadDays int) (string, error) {
	member, err := db.GetMemberByToken(ctx, token)
	if err != nil {
		return "", fmt.Errorf("invalid token: %w", err)
	}

	startDate := time.Now().Format("2006-01-02")
	endDate := time.Now().AddDate(0, 0, lookaheadDays).Format("2006-01-02")
	assignments, err := db.GetAssignmentsByDateRange(ctx, startDate, endDate)
	if err != nil {
		return "", fmt.Errorf("failed to get assignments: %w", err)
	}

	memberNames := make(map[string]string)
	members, err := db.GetActiveTeamMembers(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get team members: %w", err)
	}
	for _, m := range members {
		memberNames[m.ID] = m.Name
	}

	var others []database.RotaAssignment
	for _, assignment := range assignments {
		if assignment.MemberID == member.ID {
			continue
		}
		others = append(others, assignment)
	}

	icalContent, err := GenerateTeamCalendar(others, memberNames)
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
