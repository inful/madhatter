package calendar

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
	"html/template"
	"math"
	"math/rand"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	texttemplate "text/template"
	"time"

	ics "github.com/arran4/golang-ical"
	"github.com/inful/madhatter/internal/database"
)

const (
	meetingDateLayout       = "2006-01-02"
	meetingStartHour        = 9
	meetingStartMinute      = 30
	morningMeetingMinutes   = 15
	projectMeetingMinutes   = 30
	defaultMeetingsTimezone = "Europe/Oslo"

	morningMeetingSummary = "Morning Shuffle"
	projectMeetingSummary = "Project shuffle"

	// Keep the shuffle seed stable even if display names change.
	morningMeetingSeedKey = "Morning meeting"
	projectMeetingSeedKey = "Project meeting"
)

type MeetingsOptions struct {
	Timezone string
	TeamsURL string

	// Links are additional resources rendered into meeting descriptions.
	// If meeting-specific links are set, they take precedence.
	Links []MeetingLink

	// MorningLinks are used for Tue–Fri "Morning Shuffle" events.
	MorningLinks []MeetingLink
	// ProjectLinks are used for Mon "Project shuffle" events.
	ProjectLinks []MeetingLink

	// TemplateTextPath and TemplateHTMLPath allow overriding meeting description templates at deployment.
	// If empty, built-in defaults are used.
	TemplateTextPath string
	TemplateHTMLPath string

	// SeedSalt lets you stabilize shuffles across deployments.
	// If empty, a default salt is used.
	SeedSalt string
}

// MeetingLink represents an extra resource link to attach to meeting events.
// If HTML is set, it will be rendered as-is in HTML descriptions.
// If Label+URL are set, the HTML renderer will generate an <a> tag.
type MeetingLink struct {
	Label string
	URL   string
	HTML  template.HTML
}

// ParseMeetingLinks parses a comma-separated list of links.
// Each entry may be either:
// - a raw HTML anchor (e.g. <a href="https://example.com">Runbook</a>)
// - a label+URL pair separated by '|' (e.g. Runbook|https://example.com)
// Whitespace is trimmed. Invalid entries are ignored.
func ParseMeetingLinks(raw string) []MeetingLink {
	parts := strings.Split(raw, ",")
	out := make([]MeetingLink, 0, len(parts))
	for _, p := range parts {
		item := strings.TrimSpace(p)
		if item == "" {
			continue
		}

		// Raw HTML anchor (trusted deployment input).
		if strings.Contains(item, "<") {
			if strings.Contains(strings.ToLower(item), "<a") {
				out = append(out, MeetingLink{HTML: template.HTML(item)}) //nolint:gosec // Trusted deployment input.
			}
			continue
		}

		label, rawURL, ok := strings.Cut(item, "|")
		if !ok {
			continue
		}
		label = strings.TrimSpace(label)
		rawURL = strings.TrimSpace(rawURL)
		if label == "" {
			continue
		}
		if safe := safeHTTPURL(rawURL); safe != "" {
			out = append(out, MeetingLink{Label: label, URL: safe})
		}
	}
	return out
}

type meetingDescriptionData struct {
	MeetingName string
	TeamsURL    string
	Links       []meetingLinkTemplateData
	Present     []string
	Away        []string
	Support     string
	Shuffle     []string
	Agenda      []string
}

type meetingLinkTemplateData struct {
	Label string
	URL   string
	HTML  template.HTML
	Text  string
}

var (
	defaultMeetingDescriptionTextTemplate = texttemplate.Must(texttemplate.New("meetingText").Parse(
		`{{.MeetingName}}

{{- if .Links }}
Links:
{{- range .Links }}- {{.Text}}
{{- end }}
{{ end }}

Present:
{{- if .Shuffle }}
{{- range .Shuffle }}{{.}}
{{- end }}
{{- else }}- (no attendees)
{{- end }}
{{- if .Away }}

Away:
{{- range .Away }}- {{.}}
{{- end }}
{{- end }}

Agenda:
{{- range .Agenda }}- {{.}}
{{- end }}`,
	))

	defaultMeetingDescriptionHTMLTemplate = template.Must(template.New("meetingHTML").Parse(
		`<h3>{{.MeetingName}}</h3>{{if .TeamsURL}}<p><a href="{{.TeamsURL}}">Join Teams meeting</a></p>{{end}}` +
			`{{if .Links}}<h4>Links</h4>{{range .Links}}{{if .HTML}}<p>{{.HTML}}</p>{{else}}<p><a href="{{.URL}}">{{.Label}}</a></p>{{end}}{{end}}{{end}}` +
			`<h4>Present</h4>{{if .Shuffle}}{{range .Shuffle}}<p>&#8226; {{.}}</p>{{end}}{{else}}<p>(no attendees)</p>{{end}}` +
			`{{if .Away}}<h4>Away</h4>{{range .Away}}<p>&#8226; {{.}}</p>{{end}}{{end}}` +
			`<h4>Agenda</h4>{{range .Agenda}}<p>&#8226; {{.}}</p>{{end}}`,
	))

	meetingTextTemplateCache sync.Map // map[string]*texttemplate.Template
	meetingHTMLTemplateCache sync.Map // map[string]*template.Template
)

func loadMeetingTextTemplate(path string) (*texttemplate.Template, error) {
	if strings.TrimSpace(path) == "" {
		return defaultMeetingDescriptionTextTemplate, nil
	}
	if cached, ok := meetingTextTemplateCache.Load(path); ok {
		return cached.(*texttemplate.Template), nil
	}
	contents, err := os.ReadFile(path) //nolint:gosec // Template path is deployment-configured; reading it is intended.
	if err != nil {
		return nil, fmt.Errorf("read meeting text template: %w", err)
	}
	tmpl, err := texttemplate.New("meetingTextOverride").Parse(string(contents))
	if err != nil {
		return nil, fmt.Errorf("parse meeting text template: %w", err)
	}
	meetingTextTemplateCache.Store(path, tmpl)
	return tmpl, nil
}

func loadMeetingHTMLTemplate(path string) (*template.Template, error) {
	if strings.TrimSpace(path) == "" {
		return defaultMeetingDescriptionHTMLTemplate, nil
	}
	if cached, ok := meetingHTMLTemplateCache.Load(path); ok {
		return cached.(*template.Template), nil
	}
	contents, err := os.ReadFile(path) //nolint:gosec // Template path is deployment-configured; reading it is intended.
	if err != nil {
		return nil, fmt.Errorf("read meeting html template: %w", err)
	}
	tmpl, err := template.New("meetingHTMLOverride").Parse(string(contents))
	if err != nil {
		return nil, fmt.Errorf("parse meeting html template: %w", err)
	}
	meetingHTMLTemplateCache.Store(path, tmpl)
	return tmpl, nil
}

func (o MeetingsOptions) normalized() MeetingsOptions {
	out := o
	if out.Timezone == "" {
		out.Timezone = defaultMeetingsTimezone
	}
	if out.SeedSalt == "" {
		out.SeedSalt = "support-rota-meetings"
	}
	return out
}

// GenerateMeetingsICalForToken generates an iCalendar containing the team's meetings.
// The token is used purely as authorization (must exist) and does not currently personalize content.
func GenerateMeetingsICalForToken(
	ctx context.Context,
	db *database.DB,
	token string,
	lookaheadDays int,
	opts MeetingsOptions,
	isBusinessDay func(time.Time) bool,
) (string, error) {
	return GenerateMeetingsICalForTokenFrom(ctx, db, token, time.Now(), lookaheadDays, opts, isBusinessDay)
}

func GenerateMeetingsICalForTokenFrom(
	ctx context.Context,
	db *database.DB,
	token string,
	from time.Time,
	lookaheadDays int,
	opts MeetingsOptions,
	isBusinessDay func(time.Time) bool,
) (string, error) {
	// Validate token exists (re-use calendar subscription tokens).
	if _, err := db.GetMemberByToken(ctx, token); err != nil {
		return "", fmt.Errorf("invalid token: %w", err)
	}

	opts = opts.normalized()
	loc, err := time.LoadLocation(opts.Timezone)
	if err != nil {
		loc = time.UTC
	}

	generator := NewICalGeneratorWithMetadata(
		"Support Meetings Calendar",
		"Daily morning meeting (Tue-Fri) and project meeting (Mon)",
	)
	generator.AddTimezoneSupport(opts.Timezone)

	start := from.In(loc)
	end := start.AddDate(0, 0, lookaheadDays)

	for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
		if isBusinessDay != nil && !isBusinessDay(d) {
			continue
		}

		if err := addMeetingForDay(ctx, db, generator, d, loc, opts); err != nil {
			return "", err
		}
	}

	return generator.Serialize()
}

func addMeetingForDay(ctx context.Context, db *database.DB, g *ICalGenerator, day time.Time, loc *time.Location, opts MeetingsOptions) error {
	switch day.Weekday() {
	case time.Monday:
		return addProjectMeetingEvent(ctx, db, g, day, loc, opts)
	case time.Tuesday, time.Wednesday, time.Thursday, time.Friday:
		return addMorningMeetingEvent(ctx, db, g, day, loc, opts)
	case time.Saturday, time.Sunday:
		return nil
	}

	return nil
}

func addMorningMeetingEvent(ctx context.Context, db *database.DB, g *ICalGenerator, day time.Time, loc *time.Location, opts MeetingsOptions) error {
	dateStr := day.In(loc).Format(meetingDateLayout)
	uid := fmt.Sprintf("meeting-morning-%s@supportrota", strings.ReplaceAll(dateStr, "-", ""))

	event := g.calendar.AddEvent(uid)
	startAt := time.Date(day.Year(), day.Month(), day.Day(), meetingStartHour, meetingStartMinute, 0, 0, loc)
	endAt := startAt.Add(morningMeetingMinutes * time.Minute)

	setTimedEventWithTZID(event, startAt, endAt, opts.Timezone)
	event.SetSummary(morningMeetingSummary)
	event.SetStatus(ics.ObjectStatusConfirmed)
	event.SetSequence(0)
	event.SetModifiedAt(time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC))

	if opts.TeamsURL != "" {
		event.SetLocation(opts.TeamsURL)
		event.SetURL(opts.TeamsURL)
	}

	description, err := buildMeetingDescription(ctx, db, dateStr, morningMeetingSummary, morningMeetingSeedKey, true, opts)
	if err != nil {
		return err
	}
	event.SetDescription(description)

	htmlDesc, err := buildMeetingDescriptionHTML(ctx, db, dateStr, morningMeetingSummary, morningMeetingSeedKey, true, opts)
	if err != nil {
		return err
	}
	setAltDescHTML(event, htmlDesc)
	return nil
}

func addProjectMeetingEvent(ctx context.Context, db *database.DB, g *ICalGenerator, day time.Time, loc *time.Location, opts MeetingsOptions) error {
	dateStr := day.In(loc).Format(meetingDateLayout)
	uid := fmt.Sprintf("meeting-project-%s@supportrota", strings.ReplaceAll(dateStr, "-", ""))

	event := g.calendar.AddEvent(uid)
	startAt := time.Date(day.Year(), day.Month(), day.Day(), meetingStartHour, meetingStartMinute, 0, 0, loc)
	endAt := startAt.Add(projectMeetingMinutes * time.Minute)

	setTimedEventWithTZID(event, startAt, endAt, opts.Timezone)
	event.SetSummary(projectMeetingSummary)
	event.SetStatus(ics.ObjectStatusConfirmed)
	event.SetSequence(0)
	event.SetModifiedAt(time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC))

	if opts.TeamsURL != "" {
		event.SetLocation(opts.TeamsURL)
		event.SetURL(opts.TeamsURL)
	}

	description, err := buildMeetingDescription(ctx, db, dateStr, projectMeetingSummary, projectMeetingSeedKey, false, opts)
	if err != nil {
		return err
	}
	event.SetDescription(description)

	htmlDesc, err := buildMeetingDescriptionHTML(ctx, db, dateStr, projectMeetingSummary, projectMeetingSeedKey, false, opts)
	if err != nil {
		return err
	}
	setAltDescHTML(event, htmlDesc)
	return nil
}

func buildMeetingDescriptionHTML(
	ctx context.Context,
	db *database.DB,
	dateStr string,
	meetingName string,
	meetingSeedKey string,
	includeJazzHands bool,
	opts MeetingsOptions,
) (string, error) {
	data, err := buildMeetingDescriptionData(ctx, db, dateStr, meetingName, meetingSeedKey, includeJazzHands, opts)
	if err != nil {
		return "", err
	}

	var b bytes.Buffer
	tmpl, err := loadMeetingHTMLTemplate(opts.TemplateHTMLPath)
	if err != nil {
		return "", err
	}
	if err := tmpl.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

func memberNames(members []database.TeamMember) []string {
	out := make([]string, 0, len(members))
	for _, m := range members {
		out = append(out, m.Name)
	}
	return out
}

func safeHTTPURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	if u.Host == "" {
		return ""
	}
	return raw
}

func supportLine(supportName string, supportIsCover bool) string {
	if supportName == "" {
		return "Unassigned"
	}
	line := supportName
	if supportIsCover {
		line += " (COVER)"
	}
	return line
}

func shuffleOrderLines(order []database.TeamMember, supportName string) []string {
	out := make([]string, 0, len(order))
	for i, m := range order {
		name := m.Name
		if supportName != "" && strings.EqualFold(m.Name, supportName) {
			name += " (Support)"
		}
		out = append(out, fmt.Sprintf("%d. %s", i+1, name))
	}
	return out
}

func agendaLines(includeJazzHands bool) []string {
	agenda := []string{"Shuffle: what you're doing today."}
	if includeJazzHands {
		agenda = append(agenda, "JazzHands: say anything.")
	}
	return agenda
}

func buildMeetingDescription(
	ctx context.Context,
	db *database.DB,
	dateStr string,
	meetingName string,
	meetingSeedKey string,
	includeJazzHands bool,
	opts MeetingsOptions,
) (string, error) {
	data, err := buildMeetingDescriptionData(ctx, db, dateStr, meetingName, meetingSeedKey, includeJazzHands, opts)
	if err != nil {
		return "", err
	}

	var b bytes.Buffer
	tmpl, err := loadMeetingTextTemplate(opts.TemplateTextPath)
	if err != nil {
		return "", err
	}
	if err := tmpl.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

func buildMeetingDescriptionData(
	ctx context.Context,
	db *database.DB,
	dateStr string,
	meetingName string,
	meetingSeedKey string,
	includeJazzHands bool,
	opts MeetingsOptions,
) (meetingDescriptionData, error) {
	present, away, err := getPresenceForDate(ctx, db, dateStr)
	if err != nil {
		return meetingDescriptionData{}, err
	}

	supportName, supportIsCover, err := getSupportForDate(ctx, db, dateStr)
	if err != nil {
		return meetingDescriptionData{}, err
	}

	seedKey := normalizeSeedKey(meetingSeedKey, meetingName)
	order := shuffledOrder(present, opts.SeedSalt+"|"+dateStr+"|"+seedKey)
	linkData := buildMeetingLinkTemplateData(selectMeetingLinks(meetingSeedKey, opts))

	return meetingDescriptionData{
		MeetingName: meetingName,
		TeamsURL:    safeHTTPURL(opts.TeamsURL),
		Links:       linkData,
		Present:     memberNames(present),
		Away:        memberNames(away),
		Support:     supportLine(supportName, supportIsCover),
		Shuffle:     shuffleOrderLines(order, supportName),
		Agenda:      agendaLines(includeJazzHands),
	}, nil
}

func normalizeSeedKey(seedKey string, fallback string) string {
	if strings.TrimSpace(seedKey) == "" {
		return fallback
	}
	return seedKey
}

func selectMeetingLinks(meetingSeedKey string, opts MeetingsOptions) []MeetingLink {
	// Default.
	links := opts.Links

	switch meetingSeedKey {
	case morningMeetingSeedKey:
		if len(opts.MorningLinks) > 0 {
			return opts.MorningLinks
		}
	case projectMeetingSeedKey:
		if len(opts.ProjectLinks) > 0 {
			return opts.ProjectLinks
		}
	}

	return links
}

func buildMeetingLinkTemplateData(links []MeetingLink) []meetingLinkTemplateData {
	out := make([]meetingLinkTemplateData, 0, len(links))
	for _, l := range links {
		if strings.TrimSpace(string(l.HTML)) != "" {
			out = append(out, meetingLinkTemplateData{HTML: l.HTML, Text: "(HTML link)"})
			continue
		}
		label := strings.TrimSpace(l.Label)
		linkURL := safeHTTPURL(strings.TrimSpace(l.URL))
		if label == "" || linkURL == "" {
			continue
		}
		out = append(out, meetingLinkTemplateData{Label: label, URL: linkURL, Text: label + ": " + linkURL})
	}
	return out
}

func setTimedEventWithTZID(event *ics.VEvent, startAt, endAt time.Time, timezone string) {
	if timezone == "" {
		// Fall back to the library's default behavior.
		event.SetStartAt(startAt)
		event.SetEndAt(endAt)
		return
	}

	// Use TZID so clients render the time in the given zone (including DST).
	// We intentionally do not append 'Z' (UTC) here.
	event.AddProperty(ics.ComponentPropertyDtStart, startAt.Format("20060102T150405"), ics.WithTZID(timezone))
	event.AddProperty(ics.ComponentPropertyDtEnd, endAt.Format("20060102T150405"), ics.WithTZID(timezone))
}

func getPresenceForDate(ctx context.Context, db *database.DB, dateStr string) ([]database.TeamMember, []database.TeamMember, error) {
	members, err := db.GetActiveTeamMembers(ctx)
	if err != nil {
		return nil, nil, err
	}

	leaveRecords, err := db.GetLeaveByDate(ctx, dateStr)
	if err != nil {
		return nil, nil, err
	}

	onLeave := make(map[string]struct{}, len(leaveRecords))
	for i := range leaveRecords {
		onLeave[leaveRecords[i].MemberID] = struct{}{}
	}

	present := make([]database.TeamMember, 0, len(members))
	away := make([]database.TeamMember, 0, len(leaveRecords))

	for _, m := range members {
		if _, ok := onLeave[m.ID]; ok {
			away = append(away, m)
			continue
		}
		present = append(present, m)
	}

	sort.Slice(present, func(i, j int) bool { return present[i].Name < present[j].Name })
	sort.Slice(away, func(i, j int) bool { return away[i].Name < away[j].Name })

	return present, away, nil
}

func getSupportForDate(ctx context.Context, db *database.DB, dateStr string) (string, bool, error) {
	assignments, err := db.GetAssignmentsByDate(ctx, dateStr)
	if err != nil {
		return "", false, err
	}

	for i := range assignments {
		if assignments[i].IsCover {
			return assignments[i].MemberName, true, nil
		}
	}
	for i := range assignments {
		if !assignments[i].IsCover {
			return assignments[i].MemberName, false, nil
		}
	}

	return "", false, nil
}

func shuffledOrder(present []database.TeamMember, seedKey string) []database.TeamMember {
	if len(present) <= 1 {
		return append([]database.TeamMember(nil), present...)
	}

	seed := stableSeed(seedKey)
	//nolint:gosec // Deterministic shuffle for meeting order; not used for security.
	rng := rand.New(rand.NewSource(int64(seed & math.MaxInt64)))

	out := append([]database.TeamMember(nil), present...)
	for i := len(out) - 1; i > 0; i-- {
		j := rng.Intn(i + 1)
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func stableSeed(key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return h.Sum64()
}
