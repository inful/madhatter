package calendar

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"strings"
	texttemplate "text/template"
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

	supportAssignmentTextTemplate *texttemplate.Template
	supportAssignmentHTMLTemplate *template.Template
	leaveTextTemplate             *texttemplate.Template
	leaveHTMLTemplate             *template.Template
	holidayTextTemplate           *texttemplate.Template
	holidayHTMLTemplate           *template.Template
}

type SupportCalendarOptions struct {
	SupportDayLinks []MeetingLink
	WithAlarm       bool

	// ShuffleSeed drives the per-day stable randomisation in the
	// snapshot. Empty = built-in default.
	ShuffleSeed string

	// WFHMaterialiser, when non-nil, is invoked once per day before
	// the snapshot is built. Used to make sure recurring-WFH rows are
	// present in the WFH query.
	WFHMaterialiser WFHMaterialiser

	// HolidayLookup, when non-nil, supplies the holiday name for a
	// given date in the snapshot. nil is allowed (every date is
	// non-holiday).
	HolidayLookup HolidayLookup

	// Template overrides. Empty values fall back to built-in defaults
	// that reproduce today's hard-coded output exactly.
	SupportAssignmentTemplateTextPath string
	SupportAssignmentTemplateHTMLPath string
	LeaveTemplateTextPath             string
	LeaveTemplateHTMLPath             string
	HolidayTemplateTextPath           string
	HolidayTemplateHTMLPath           string
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

// WithAlarm enables a 1-day advance display notification on each assignment event.
func (g *ICalGenerator) WithAlarm() *ICalGenerator {
	g.withAlarm = true
	return g
}

// WithShuffleSeed is a no-op kept for API stability. The shuffle seed
// is read from SupportCalendarOptions by the public generator
// functions.
func (g *ICalGenerator) WithShuffleSeed(string) *ICalGenerator { return g }

// WithWFHMaterialiser is a no-op kept for API stability. The materialiser
// is read from SupportCalendarOptions by the public generator
// functions.
func (g *ICalGenerator) WithWFHMaterialiser(WFHMaterialiser) *ICalGenerator { return g }

// WithHolidayLookup is a no-op kept for API stability. The lookup is
// read from SupportCalendarOptions by the public generator functions.
func (g *ICalGenerator) WithHolidayLookup(HolidayLookup) *ICalGenerator { return g }

// WithSupportAssignmentTemplate loads the operator's text and HTML
// templates for the support-assignment event kind. Both arguments may
// be empty, in which case the built-in defaults are used.
func (g *ICalGenerator) WithSupportAssignmentTemplate(textPath, htmlPath string) (*ICalGenerator, error) {
	tmpl, err := loadSupportAssignmentText(textPath)
	if err != nil {
		return nil, err
	}
	g.supportAssignmentTextTemplate = tmpl

	htmlTmpl, err := loadSupportAssignmentHTML(htmlPath)
	if err != nil {
		return nil, err
	}
	g.supportAssignmentHTMLTemplate = htmlTmpl
	return g, nil
}

// WithLeaveTemplate loads the operator's text and HTML templates for
// the leave-event kind.
func (g *ICalGenerator) WithLeaveTemplate(textPath, htmlPath string) (*ICalGenerator, error) {
	tmpl, err := loadLeaveText(textPath)
	if err != nil {
		return nil, err
	}
	g.leaveTextTemplate = tmpl

	htmlTmpl, err := loadLeaveHTML(htmlPath)
	if err != nil {
		return nil, err
	}
	g.leaveHTMLTemplate = htmlTmpl
	return g, nil
}

// WithHolidayTemplate loads the operator's text and HTML templates for
// the holiday-event kind.
func (g *ICalGenerator) WithHolidayTemplate(textPath, htmlPath string) (*ICalGenerator, error) {
	tmpl, err := loadHolidayText(textPath)
	if err != nil {
		return nil, err
	}
	g.holidayTextTemplate = tmpl

	htmlTmpl, err := loadHolidayHTML(htmlPath)
	if err != nil {
		return nil, err
	}
	g.holidayHTMLTemplate = htmlTmpl
	return g, nil
}

// resolvedSupportAssignmentTextTemplate returns the configured template
// or the built-in default.
func (g *ICalGenerator) resolvedSupportAssignmentTextTemplate() *texttemplate.Template {
	if g.supportAssignmentTextTemplate == nil {
		return defaultSupportAssignmentTextTemplate
	}
	return g.supportAssignmentTextTemplate
}

// resolvedSupportAssignmentHTMLTemplate returns the configured HTML
// template or the built-in default.
func (g *ICalGenerator) resolvedSupportAssignmentHTMLTemplate() *template.Template {
	if g.supportAssignmentHTMLTemplate == nil {
		return defaultSupportAssignmentHTMLTemplate
	}
	return g.supportAssignmentHTMLTemplate
}

// resolvedLeaveTextTemplate returns the configured text template or
// the built-in default.
func (g *ICalGenerator) resolvedLeaveTextTemplate() *texttemplate.Template {
	if g.leaveTextTemplate == nil {
		return defaultLeaveTextTemplate
	}
	return g.leaveTextTemplate
}

// resolvedLeaveHTMLTemplate returns the configured HTML template or
// the built-in default.
func (g *ICalGenerator) resolvedLeaveHTMLTemplate() *template.Template {
	if g.leaveHTMLTemplate == nil {
		return defaultLeaveHTMLTemplate
	}
	return g.leaveHTMLTemplate
}

// resolvedHolidayTextTemplate returns the configured text template or
// the built-in default.
func (g *ICalGenerator) resolvedHolidayTextTemplate() *texttemplate.Template {
	if g.holidayTextTemplate == nil {
		return defaultHolidayTextTemplate
	}
	return g.holidayTextTemplate
}

// resolvedHolidayHTMLTemplate returns the configured HTML template or
// the built-in default.
func (g *ICalGenerator) resolvedHolidayHTMLTemplate() *template.Template {
	if g.holidayHTMLTemplate == nil {
		return defaultHolidayHTMLTemplate
	}
	return g.holidayHTMLTemplate
}

// AddAssignment adds a rota assignment as a calendar event using the
// built-in templates. Callers that have a per-day snapshot should
// prefer AddAssignmentWithSnapshot, which exposes the snapshot fields
// to the templates.
//
// This method remains for callers (and tests) that don't have a
// snapshot. It uses a zero-value snapshot so template authors who
// reference snapshot fields see empty lists / empty strings.
func (g *ICalGenerator) AddAssignment(assignment database.RotaAssignment, memberName string) error {
	return g.AddAssignmentWithSnapshot(assignment, memberName, &presenceSnapshot{Date: assignment.Date})
}

// AddAssignmentWithSnapshot adds a rota assignment as a calendar event
// and renders the description via the configured support-assignment
// templates. The snapshot is embedded in the template data so
// operators can reference per-day fields (on-site count, HAT name,
// stable order, etc.) from their templates.
func (g *ICalGenerator) AddAssignmentWithSnapshot(assignment database.RotaAssignment, memberName string, snap *presenceSnapshot) error {
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

	// Build the template data. The base text and link list are
	// computed the same way as before; the snapshot's embedded fields
	// give templates access to per-day presence.
	baseText := "Support duty"
	if assignment.IsCover {
		baseText += " (cover)"
		if assignment.OriginalAssignmentID != nil {
			baseText += " for leave"
		}
	}
	if snap == nil {
		snap = &presenceSnapshot{Date: assignment.Date}
	}
	data := supportAssignmentData{
		presenceSnapshot: *snap,
		Summary:          summary,
		BaseText:         baseText,
		IsCover:          assignment.IsCover,
		IsCoverForLeave:  assignment.IsCover && assignment.OriginalAssignmentID != nil,
		Date:             assignment.Date,
		Links:            buildMeetingLinkTemplateData(g.supportDayLinks),
	}

	textDescription, err := renderTemplate(g.resolvedSupportAssignmentTextTemplate(), "supportText", data)
	if err != nil {
		return fmt.Errorf("render support text: %w", err)
	}
	event.SetDescription(textDescription)

	htmlDescription, err := renderHTMLTemplate(g.resolvedSupportAssignmentHTMLTemplate(), "supportHTML", data)
	if err != nil {
		return fmt.Errorf("render support HTML: %w", err)
	}
	setAltDescHTML(event, htmlDescription)

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

// AddLeaveEvent adds a leave/absence event to the calendar using the
// built-in templates. Callers that have a per-day snapshot should
// prefer AddLeaveEventWithSnapshot.
func (g *ICalGenerator) AddLeaveEvent(memberName string, leaveType string, startDate, endDate time.Time) error {
	return g.AddLeaveEventWithSnapshot(memberName, leaveType, startDate, endDate, &presenceSnapshot{Date: startDate.Format("2006-01-02")})
}

// AddLeaveEventWithSnapshot renders a leave event with the configured
// leave templates. The snapshot is embedded in the template data so
// operators can reference per-day fields.
func (g *ICalGenerator) AddLeaveEventWithSnapshot(memberName string, leaveType string, startDate, endDate time.Time, snap *presenceSnapshot) error {
	event := g.calendar.AddEvent(fmt.Sprintf("leave-%s-%d", strings.ToLower(memberName), startDate.Unix()))

	// Set event times (all day event)
	event.SetAllDayStartAt(startDate)
	event.SetAllDayEndAt(endDate.Add(hoursPerDay * time.Hour)) // End date is exclusive in iCalendar

	summary := fmt.Sprintf("%s - %s", memberName, titleCase(leaveType))
	baseText := fmt.Sprintf("%s leave for %s", titleCase(leaveType), memberName)
	event.SetSummary(summary)

	if snap == nil {
		snap = &presenceSnapshot{Date: startDate.Format("2006-01-02")}
	}
	data := leaveData{
		presenceSnapshot: *snap,
		Summary:          summary,
		BaseText:         baseText,
		MemberName:       memberName,
		LeaveType:        titleCase(leaveType),
		StartDate:        startDate.Format("2006-01-02"),
		EndDate:          endDate.Format("2006-01-02"),
	}

	textDescription, err := renderTemplate(g.resolvedLeaveTextTemplate(), "leaveText", data)
	if err != nil {
		return fmt.Errorf("render leave text: %w", err)
	}
	event.SetDescription(textDescription)

	htmlDescription, err := renderHTMLTemplate(g.resolvedLeaveHTMLTemplate(), "leaveHTML", data)
	if err != nil {
		return fmt.Errorf("render leave HTML: %w", err)
	}
	setAltDescHTML(event, htmlDescription)

	// Set status to tentative for leave
	event.SetStatus(ics.ObjectStatusTentative)

	// Set transparency to opaque (time is busy)
	event.SetTimeTransparency(ics.TransparencyOpaque)

	return nil
}

// AddHoliday adds a holiday/closed day event to the calendar using the
// built-in templates. Callers with a snapshot should prefer
// AddHolidayWithSnapshot.
func (g *ICalGenerator) AddHoliday(name string, date time.Time) error {
	return g.AddHolidayWithSnapshot(name, date, &presenceSnapshot{Date: date.Format("2006-01-02")})
}

// AddHolidayWithSnapshot renders a holiday event with the configured
// holiday templates. The snapshot is embedded in the template data so
// operators can reference per-day fields.
func (g *ICalGenerator) AddHolidayWithSnapshot(name string, date time.Time, snap *presenceSnapshot) error {
	event := g.calendar.AddEvent(fmt.Sprintf("holiday-%d", date.Unix()))

	// Set event as all day
	event.SetAllDayStartAt(date)
	event.SetAllDayEndAt(date.Add(hoursPerDay * time.Hour))

	summary := fmt.Sprintf("Office Closed - %s", name)
	baseText := "Support rota is not scheduled on this day"
	event.SetSummary(summary)

	if snap == nil {
		snap = &presenceSnapshot{Date: date.Format("2006-01-02")}
	}
	data := holidayData{
		presenceSnapshot: *snap,
		Summary:          summary,
		BaseText:         baseText,
		Name:             name,
		Date:             date.Format("2006-01-02"),
	}

	textDescription, err := renderTemplate(g.resolvedHolidayTextTemplate(), "holidayText", data)
	if err != nil {
		return fmt.Errorf("render holiday text: %w", err)
	}
	event.SetDescription(textDescription)

	htmlDescription, err := renderHTMLTemplate(g.resolvedHolidayHTMLTemplate(), "holidayHTML", data)
	if err != nil {
		return fmt.Errorf("render holiday HTML: %w", err)
	}
	setAltDescHTML(event, htmlDescription)

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
	generator, err := newSupportGenerator(opts)
	if err != nil {
		return "", err
	}

	for _, assignment := range assignments {
		if err := generator.AddAssignment(assignment, memberName); err != nil {
			return "", fmt.Errorf("failed to add assignment: %w", err)
		}
	}

	return generator.Serialize()
}

// newSupportGenerator builds an ICalGenerator preconfigured with the
// support-assignment template paths and any other options. Used by
// every public generator function in this file.
func newSupportGenerator(opts SupportCalendarOptions) (*ICalGenerator, error) {
	generator := NewICalGenerator().
		WithSupportDayLinks(opts.SupportDayLinks).
		WithShuffleSeed(opts.ShuffleSeed).
		WithWFHMaterialiser(opts.WFHMaterialiser).
		WithHolidayLookup(opts.HolidayLookup)
	if opts.WithAlarm {
		generator = generator.WithAlarm()
	}
	generator, err := generator.WithSupportAssignmentTemplate(opts.SupportAssignmentTemplateTextPath, opts.SupportAssignmentTemplateHTMLPath)
	if err != nil {
		return nil, err
	}
	generator, err = generator.WithLeaveTemplate(opts.LeaveTemplateTextPath, opts.LeaveTemplateHTMLPath)
	if err != nil {
		return nil, err
	}
	generator, err = generator.WithHolidayTemplate(opts.HolidayTemplateTextPath, opts.HolidayTemplateHTMLPath)
	if err != nil {
		return nil, err
	}
	return generator, nil
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

	generator, err := newSupportGenerator(opts)
	if err != nil {
		return "", err
	}

	// Build a per-day snapshot once per date and reuse it across all
	// events added for that date.
	builder := newPresenceBuilder(db, opts.WFHMaterialiser, opts.HolidayLookup, opts.ShuffleSeed)
	snapshotByDate := make(map[string]*presenceSnapshot, lookaheadDays)
	snapshotFor := func(dateStr string) (*presenceSnapshot, error) {
		if s, ok := snapshotByDate[dateStr]; ok {
			return s, nil
		}
		s, sErr := builder.Build(ctx, dateStr)
		if sErr != nil {
			return nil, sErr
		}
		snapshotByDate[dateStr] = s
		return s, nil
	}

	for _, assignment := range assignments {
		snap, sErr := snapshotFor(assignment.Date)
		if sErr != nil {
			return "", fmt.Errorf("build snapshot for %s: %w", assignment.Date, sErr)
		}
		if addErr := generator.AddAssignmentWithSnapshot(assignment, member.Name, snap); addErr != nil {
			return "", fmt.Errorf("failed to add assignment: %w", addErr)
		}
	}

	icalContent, err := generator.Serialize()
	if err != nil {
		return "", fmt.Errorf("failed to generate iCalendar: %w", err)
	}

	return icalContent, nil
}

// GenerateOthersICalForToken generates iCalendar content for all upcoming
// assignments except the token owner's.
func GenerateOthersICalForToken(ctx context.Context, db *database.DB, token string, lookaheadDays int) (string, error) {
	return GenerateOthersICalForTokenWithOptions(ctx, db, token, lookaheadDays, SupportCalendarOptions{})
}

// GenerateOthersICalForTokenWithOptions is the operator-configurable
// version that threads support templates and per-day presence into
// every team member's assignment event.
func GenerateOthersICalForTokenWithOptions(ctx context.Context, db *database.DB, token string, lookaheadDays int, opts SupportCalendarOptions) (string, error) {
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

	icalContent, err := GenerateTeamCalendarWithOptions(ctx, db, others, memberNames, opts)
	if err != nil {
		return "", fmt.Errorf("failed to generate iCalendar: %w", err)
	}

	return icalContent, nil
}

// GenerateTeamCalendar generates a calendar with all team members' assignments.
func GenerateTeamCalendar(assignments []database.RotaAssignment, members map[string]string) (string, error) {
	return GenerateTeamCalendarWithOptions(context.Background(), nil, assignments, members, SupportCalendarOptions{})
}

// GenerateTeamCalendarWithOptions generates a calendar with all team
// members' assignments, applying the operator's template overrides
// and the per-day presence snapshot. db may be nil; in that case the
// snapshot is empty (event-adders pass it through to templates
// regardless).
func GenerateTeamCalendarWithOptions(ctx context.Context, db *database.DB, assignments []database.RotaAssignment, members map[string]string, opts SupportCalendarOptions) (string, error) {
	generator, err := newSupportGenerator(opts)
	if err != nil {
		return "", err
	}

	var builder *presenceBuilder
	if db != nil {
		builder = newPresenceBuilder(db, opts.WFHMaterialiser, opts.HolidayLookup, opts.ShuffleSeed)
	}
	snapshotByDate := make(map[string]*presenceSnapshot)
	snapshotFor := func(dateStr string) *presenceSnapshot {
		if builder == nil {
			return &presenceSnapshot{Date: dateStr}
		}
		if s, ok := snapshotByDate[dateStr]; ok {
			return s
		}
		s, err := builder.Build(ctx, dateStr)
		if err != nil {
			return &presenceSnapshot{Date: dateStr}
		}
		snapshotByDate[dateStr] = s
		return s
	}

	for _, assignment := range assignments {
		memberName, ok := members[assignment.MemberID]
		if !ok {
			memberName = "Unknown"
		}

		snap := snapshotFor(assignment.Date)
		if err := generator.AddAssignmentWithSnapshot(assignment, memberName, snap); err != nil {
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
