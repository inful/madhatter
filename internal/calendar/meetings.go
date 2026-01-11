package calendar

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"sort"
	"strings"
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
	defaultMeetingsTimezone = "UTC"
)

type MeetingsOptions struct {
	Timezone string
	TeamsURL string

	// SeedSalt lets you stabilize shuffles across deployments.
	// If empty, a default salt is used.
	SeedSalt string
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

	event.SetStartAt(startAt)
	event.SetEndAt(endAt)
	event.SetSummary("Morning meeting")
	event.SetStatus(ics.ObjectStatusConfirmed)
	event.SetSequence(0)
	event.SetModifiedAt(time.Now().UTC())

	if opts.TeamsURL != "" {
		event.SetLocation(opts.TeamsURL)
		event.SetURL(opts.TeamsURL)
	}

	description, err := buildMeetingDescription(ctx, db, dateStr, "Morning meeting", true, opts)
	if err != nil {
		return err
	}
	event.SetDescription(description)
	return nil
}

func addProjectMeetingEvent(ctx context.Context, db *database.DB, g *ICalGenerator, day time.Time, loc *time.Location, opts MeetingsOptions) error {
	dateStr := day.In(loc).Format(meetingDateLayout)
	uid := fmt.Sprintf("meeting-project-%s@supportrota", strings.ReplaceAll(dateStr, "-", ""))

	event := g.calendar.AddEvent(uid)
	startAt := time.Date(day.Year(), day.Month(), day.Day(), meetingStartHour, meetingStartMinute, 0, 0, loc)
	endAt := startAt.Add(projectMeetingMinutes * time.Minute)

	event.SetStartAt(startAt)
	event.SetEndAt(endAt)
	event.SetSummary("Project meeting")
	event.SetStatus(ics.ObjectStatusConfirmed)
	event.SetSequence(0)
	event.SetModifiedAt(time.Now().UTC())

	if opts.TeamsURL != "" {
		event.SetLocation(opts.TeamsURL)
		event.SetURL(opts.TeamsURL)
	}

	description, err := buildMeetingDescription(ctx, db, dateStr, "Project meeting", false, opts)
	if err != nil {
		return err
	}
	event.SetDescription(description)
	return nil
}

func buildMeetingDescription(
	ctx context.Context,
	db *database.DB,
	dateStr string,
	meetingName string,
	includeJazzHands bool,
	opts MeetingsOptions,
) (string, error) {
	present, away, err := getPresenceForDate(ctx, db, dateStr)
	if err != nil {
		return "", err
	}

	supportName, supportIsCover, err := getSupportForDate(ctx, db, dateStr)
	if err != nil {
		return "", err
	}

	order := shuffledOrder(present, opts.SeedSalt+"|"+dateStr+"|"+meetingName)

	var b strings.Builder
	b.WriteString(meetingName)
	b.WriteString("\n\n")

	writeMemberList(&b, "Present", present)
	b.WriteString("\n")
	writeMemberList(&b, "Away", away)
	b.WriteString("\n")
	writeSupport(&b, supportName, supportIsCover)
	b.WriteString("\n")
	writeShuffleOrder(&b, order, supportName)
	b.WriteString("\n")
	writeAgenda(&b, includeJazzHands)

	return b.String(), nil
}

func writeMemberList(b *strings.Builder, title string, members []database.TeamMember) {
	b.WriteString(title)
	b.WriteString(":\n")
	if len(members) == 0 {
		b.WriteString("- (none)\n")
		return
	}
	for _, m := range members {
		b.WriteString("- ")
		b.WriteString(m.Name)
		b.WriteString("\n")
	}
}

func writeSupport(b *strings.Builder, supportName string, supportIsCover bool) {
	b.WriteString("Support:\n")
	if supportName == "" {
		b.WriteString("- Unassigned\n")
		return
	}
	b.WriteString("- ")
	b.WriteString(supportName)
	if supportIsCover {
		b.WriteString(" (COVER)")
	}
	b.WriteString("\n")
}

func writeShuffleOrder(b *strings.Builder, order []database.TeamMember, supportName string) {
	b.WriteString("Shuffle order:\n")
	if len(order) == 0 {
		b.WriteString("- (no attendees)\n")
		return
	}
	for i, m := range order {
		name := m.Name
		if supportName != "" && strings.EqualFold(m.Name, supportName) {
			name += " (Support)"
		}
		_, _ = fmt.Fprintf(b, "%d. %s\n", i+1, name)
	}
}

func writeAgenda(b *strings.Builder, includeJazzHands bool) {
	b.WriteString("Agenda:\n")
	b.WriteString("- Shuffle: what you're doing today.\n")
	if includeJazzHands {
		b.WriteString("- JazzHands: say anything.\n")
	}
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
