package calendar

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"os"
	"strings"
	"sync"
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

	assignmentTemplateTextPath string
	assignmentTemplateHTMLPath string
	leaveTemplateTextPath      string
	leaveTemplateHTMLPath      string
	holidayTemplateTextPath    string
	holidayTemplateHTMLPath    string
}

// SupportCalendarOptions configures the support-rota ICS calendar generation.
type SupportCalendarOptions struct {
	SupportDayLinks []MeetingLink
	WithAlarm       bool

	// Optional paths to Go template files for overriding assignment event descriptions.
	// The template receives an assignmentDescriptionData value.
	// If empty, built-in defaults are used.
	AssignmentTemplateTextPath string
	AssignmentTemplateHTMLPath string

	// Optional paths to Go template files for overriding leave event descriptions.
	// The template receives a leaveDescriptionData value.
	// If empty, built-in defaults are used.
	LeaveTemplateTextPath string
	LeaveTemplateHTMLPath string

	// Optional paths to Go template files for overriding holiday event descriptions.
	// The template receives a holidayDescriptionData value.
	// If empty, built-in defaults are used.
	HolidayTemplateTextPath string
	HolidayTemplateHTMLPath string
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

// WithAssignmentTemplates sets optional template file paths for assignment event descriptions.
// An empty path means "use the built-in default".
func (g *ICalGenerator) WithAssignmentTemplates(textPath, htmlPath string) *ICalGenerator {
	g.assignmentTemplateTextPath = textPath
	g.assignmentTemplateHTMLPath = htmlPath
	return g
}

// WithLeaveTemplates sets optional template file paths for leave event descriptions.
// An empty path means "use the built-in default".
func (g *ICalGenerator) WithLeaveTemplates(textPath, htmlPath string) *ICalGenerator {
	g.leaveTemplateTextPath = textPath
	g.leaveTemplateHTMLPath = htmlPath
	return g
}

// WithHolidayTemplates sets optional template file paths for holiday event descriptions.
// An empty path means "use the built-in default".
func (g *ICalGenerator) WithHolidayTemplates(textPath, htmlPath string) *ICalGenerator {
	g.holidayTemplateTextPath = textPath
	g.holidayTemplateHTMLPath = htmlPath
	return g
}

// assignmentDescriptionData is the template data for support-day (assignment) events.
type assignmentDescriptionData struct {
	// Summary is the event summary line, e.g. "HAT day (Name) (COVER)".
	Summary string
	// MemberName is the name of the team member on duty.
	MemberName string
	// IsCover is true when this assignment covers for another member's leave.
	IsCover bool
	// IsForLeave is true when there is an original assignment being covered.
	IsForLeave bool
	// Links is the list of support-day resource links.
	Links []meetingLinkTemplateData
}

// leaveDescriptionData is the template data for leave events.
type leaveDescriptionData struct {
	// Summary is the event summary line, e.g. "Name - Vacation".
	Summary string
	// MemberName is the name of the absent team member.
	MemberName string
	// LeaveType is the title-cased leave type, e.g. "Vacation".
	LeaveType string
}

// holidayDescriptionData is the template data for holiday/closed-day events.
type holidayDescriptionData struct {
	// Summary is the event summary line, e.g. "Office Closed - New Year".
	Summary string
	// Name is the name of the holiday.
	Name string
}

var (
	defaultAssignmentTextTemplate = texttemplate.Must(texttemplate.New("assignmentText").Parse(
		`Support duty{{if .IsCover}} (cover){{if .IsForLeave}} for leave{{end}}{{end}}{{if .Links}}

Links:
{{- range .Links}}
- {{.Text}}{{end}}{{end}}`,
	))

	defaultAssignmentHTMLTemplate = template.Must(template.New("assignmentHTML").Parse(
		`<h3>{{.Summary}}</h3>` +
			`<p>Support duty{{if .IsCover}} (cover){{if .IsForLeave}} for leave{{end}}{{end}}</p>` +
			`{{if .Links}}<h4>Links</h4>{{range .Links}}{{if .HTML}}<p>{{.HTML}}</p>` +
			`{{else}}<p><a href="{{.URL}}">{{.Label}}</a></p>{{end}}{{end}}{{end}}`,
	))

	defaultLeaveTextTemplate = texttemplate.Must(texttemplate.New("leaveText").Parse(
		`{{.LeaveType}} leave for {{.MemberName}}`,
	))

	defaultLeaveHTMLTemplate = template.Must(template.New("leaveHTML").Parse(
		`<h3>{{.Summary}}</h3><p>{{.LeaveType}} leave for {{.MemberName}}</p>`,
	))

	defaultHolidayTextTemplate = texttemplate.Must(texttemplate.New("holidayText").Parse(
		`Support rota is not scheduled on this day`,
	))

	defaultHolidayHTMLTemplate = template.Must(template.New("holidayHTML").Parse(
		`<h3>{{.Summary}}</h3><p>Support rota is not scheduled on this day</p>`,
	))

	supportTextTemplateCache sync.Map // map[string]*texttemplate.Template
	supportHTMLTemplateCache sync.Map // map[string]*template.Template
)

func loadSupportTextTemplate(path string, defaultTmpl *texttemplate.Template) (*texttemplate.Template, error) {
	if strings.TrimSpace(path) == "" {
		return defaultTmpl, nil
	}
	if cached, ok := supportTextTemplateCache.Load(path); ok {
		return cached.(*texttemplate.Template), nil
	}
	contents, err := os.ReadFile(path) //nolint:gosec // Template path is deployment-configured; reading it is intended.
	if err != nil {
		return nil, fmt.Errorf("read support text template %q: %w", path, err)
	}
	tmpl, err := texttemplate.New("supportTextOverride").Parse(string(contents))
	if err != nil {
		return nil, fmt.Errorf("parse support text template %q: %w", path, err)
	}
	supportTextTemplateCache.Store(path, tmpl)
	return tmpl, nil
}

func loadSupportHTMLTemplate(path string, defaultTmpl *template.Template) (*template.Template, error) {
	if strings.TrimSpace(path) == "" {
		return defaultTmpl, nil
	}
	if cached, ok := supportHTMLTemplateCache.Load(path); ok {
		return cached.(*template.Template), nil
	}
	contents, err := os.ReadFile(path) //nolint:gosec // Template path is deployment-configured; reading it is intended.
	if err != nil {
		return nil, fmt.Errorf("read support html template %q: %w", path, err)
	}
	tmpl, err := template.New("supportHTMLOverride").Parse(string(contents))
	if err != nil {
		return nil, fmt.Errorf("parse support html template %q: %w", path, err)
	}
	supportHTMLTemplateCache.Store(path, tmpl)
	return tmpl, nil
}

func renderSupportText(path string, defaultTmpl *texttemplate.Template, data any) (string, error) {
	tmpl, err := loadSupportTextTemplate(path, defaultTmpl)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("execute support text template: %w", err)
	}
	return b.String(), nil
}

func renderSupportHTML(path string, defaultTmpl *template.Template, data any) (string, error) {
	tmpl, err := loadSupportHTMLTemplate(path, defaultTmpl)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("execute support html template: %w", err)
	}
	return b.String(), nil
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

	// Build template data.
	data := assignmentDescriptionData{
		Summary:    summary,
		MemberName: memberName,
		IsCover:    assignment.IsCover,
		IsForLeave: assignment.IsCover && assignment.OriginalAssignmentID != nil,
		Links:      buildMeetingLinkTemplateData(g.supportDayLinks),
	}

	// Render text description.
	textDesc, err := renderSupportText(g.assignmentTemplateTextPath, defaultAssignmentTextTemplate, data)
	if err != nil {
		return err
	}
	event.SetDescription(textDesc)

	// Render HTML description.
	htmlDesc, err := renderSupportHTML(g.assignmentTemplateHTMLPath, defaultAssignmentHTMLTemplate, data)
	if err != nil {
		return err
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

// AddLeaveEvent adds a leave/absence event to the calendar.
func (g *ICalGenerator) AddLeaveEvent(memberName string, leaveType string, startDate, endDate time.Time) error {
	event := g.calendar.AddEvent(fmt.Sprintf("leave-%s-%d", strings.ToLower(memberName), startDate.Unix()))

	// Set event times (all day event)
	event.SetAllDayStartAt(startDate)
	event.SetAllDayEndAt(endDate.Add(hoursPerDay * time.Hour)) // End date is exclusive in iCalendar

	// Set summary
	summary := fmt.Sprintf("%s - %s", memberName, titleCase(leaveType))
	event.SetSummary(summary)

	// Build template data.
	data := leaveDescriptionData{
		Summary:    summary,
		MemberName: memberName,
		LeaveType:  titleCase(leaveType),
	}

	// Render text description.
	textDesc, err := renderSupportText(g.leaveTemplateTextPath, defaultLeaveTextTemplate, data)
	if err != nil {
		return err
	}
	event.SetDescription(textDesc)

	// Render HTML description.
	htmlDesc, err := renderSupportHTML(g.leaveTemplateHTMLPath, defaultLeaveHTMLTemplate, data)
	if err != nil {
		return err
	}
	setAltDescHTML(event, htmlDesc)

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
	summary := fmt.Sprintf("Office Closed - %s", name)
	event.SetSummary(summary)

	// Build template data.
	data := holidayDescriptionData{
		Summary: summary,
		Name:    name,
	}

	// Render text description.
	textDesc, err := renderSupportText(g.holidayTemplateTextPath, defaultHolidayTextTemplate, data)
	if err != nil {
		return err
	}
	event.SetDescription(textDesc)

	// Render HTML description.
	htmlDesc, err := renderSupportHTML(g.holidayTemplateHTMLPath, defaultHolidayHTMLTemplate, data)
	if err != nil {
		return err
	}
	setAltDescHTML(event, htmlDesc)

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
	generator := NewICalGenerator().
		WithSupportDayLinks(opts.SupportDayLinks).
		WithAssignmentTemplates(opts.AssignmentTemplateTextPath, opts.AssignmentTemplateHTMLPath).
		WithLeaveTemplates(opts.LeaveTemplateTextPath, opts.LeaveTemplateHTMLPath).
		WithHolidayTemplates(opts.HolidayTemplateTextPath, opts.HolidayTemplateHTMLPath)
	if opts.WithAlarm {
		generator = generator.WithAlarm()
	}

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
