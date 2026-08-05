package calendar

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func unfoldICalLines(s string) string {
	// RFC 5545 line unfolding: remove CRLF + (SP / HTAB).
	return strings.NewReplacer(
		"\r\n ", "",
		"\n ", "",
		"\r\n\t", "",
		"\n\t", "",
	).Replace(s)
}

func TestGenerateMeetingsICalForToken_IncludesDeterministicShuffle(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	// internal/calendar -> internal -> repo root.
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	memberID, err := db.AddTeamMember(ctx, "Token Owner", "token@example.com")
	require.NoError(t, err)
	token, err := db.CreateCalendarSubscription(ctx, memberID)
	require.NoError(t, err)

	from := time.Date(2026, 1, 12, 12, 0, 0, 0, time.UTC) // Monday

	ics1, err := GenerateMeetingsICalForTokenFrom(
		ctx,
		db,
		token,
		from,
		7,
		MeetingsOptions{Timezone: "UTC", SeedSalt: "test-salt", TeamsURL: "https://teams.example.com/meet"},
		func(t time.Time) bool { return t.Weekday() != time.Saturday && t.Weekday() != time.Sunday },
	)
	require.NoError(t, err)

	ics2, err := GenerateMeetingsICalForTokenFrom(
		ctx,
		db,
		token,
		from,
		7,
		MeetingsOptions{Timezone: "UTC", SeedSalt: "test-salt", TeamsURL: "https://teams.example.com/meet"},
		func(t time.Time) bool { return t.Weekday() != time.Saturday && t.Weekday() != time.Sunday },
	)
	require.NoError(t, err)

	require.Equal(t, ics1, ics2, "meeting calendar should be deterministic for a given day+team")
	require.NoError(t, ValidateICal(ics1), "generated meetings calendar must be valid iCalendar")
	require.Contains(t, ics1, "SUMMARY:Morning Shuffle")
	require.Contains(t, ics1, "Present")
	require.Contains(t, ics1, "JazzHands")
	require.Contains(t, ics1, "X-ALT-DESC;FMTTYPE=text/html")
	require.Contains(t, unfoldICalLines(ics1), "<a href=\"https://teams.example.com/meet\"")
}

func TestGenerateMeetingsICalForToken_IsIdenticalAcrossDifferentSubscriptions(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	// internal/calendar -> internal -> repo root.
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	memberAID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	memberBID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	tokenA, err := db.CreateCalendarSubscription(ctx, memberAID)
	require.NoError(t, err)
	tokenB, err := db.CreateCalendarSubscription(ctx, memberBID)
	require.NoError(t, err)

	from := time.Date(2026, 1, 12, 12, 0, 0, 0, time.UTC) // Monday

	icsA, err := GenerateMeetingsICalForTokenFrom(
		ctx,
		db,
		tokenA,
		from,
		7,
		MeetingsOptions{Timezone: "UTC", SeedSalt: "test-salt", TeamsURL: "https://teams.example.com/meet"},
		func(t time.Time) bool { return t.Weekday() != time.Saturday && t.Weekday() != time.Sunday },
	)
	require.NoError(t, err)

	icsB, err := GenerateMeetingsICalForTokenFrom(
		ctx,
		db,
		tokenB,
		from,
		7,
		MeetingsOptions{Timezone: "UTC", SeedSalt: "test-salt", TeamsURL: "https://teams.example.com/meet"},
		func(t time.Time) bool { return t.Weekday() != time.Saturday && t.Weekday() != time.Sunday },
	)
	require.NoError(t, err)

	require.Contains(t, icsA, "LAST-MODIFIED:20260112T000000Z")
	require.Equal(t, icsA, icsB, "meeting calendar content must be identical across subscriptions")
}

func TestGenerateMeetingsICalForToken_UsesTZIDForEventTimes(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	// internal/calendar -> internal -> repo root.
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	memberID, err := db.AddTeamMember(ctx, "Token Owner", "token@example.com")
	require.NoError(t, err)
	token, err := db.CreateCalendarSubscription(ctx, memberID)
	require.NoError(t, err)

	from := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC) // Monday (summer time in Norway).

	ical, err := GenerateMeetingsICalForTokenFrom(
		ctx,
		db,
		token,
		from,
		2,
		MeetingsOptions{Timezone: "Europe/Oslo", SeedSalt: "test-salt"},
		func(t time.Time) bool { return t.Weekday() != time.Saturday && t.Weekday() != time.Sunday },
	)
	require.NoError(t, err)

	assert.Contains(t, ical, "X-WR-TIMEZONE:Europe/Oslo")
	assert.Contains(t, ical, "DTSTART;TZID=Europe/Oslo:")
	assert.Contains(t, ical, "DTEND;TZID=Europe/Oslo:")
	assert.NotContains(t, ical, "DTSTART:20260601T093000Z")
}

func TestGenerateMeetingsICalForToken_AllowsTemplateOverrides(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	// internal/calendar -> internal -> repo root.
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	memberID, err := db.AddTeamMember(ctx, "Token Owner", "token@example.com")
	require.NoError(t, err)
	token, err := db.CreateCalendarSubscription(ctx, memberID)
	require.NoError(t, err)

	tmplDir := t.TempDir()
	textPath := filepath.Join(tmplDir, "meeting.txt.tmpl")
	htmlPath := filepath.Join(tmplDir, "meeting.html.tmpl")

	require.NoError(t, os.WriteFile(textPath, []byte("CUSTOM TEXT: {{.MeetingName}}"), 0o600))
	require.NoError(t, os.WriteFile(htmlPath, []byte("<p>CUSTOM HTML: {{.MeetingName}}</p>"), 0o600))

	from := time.Date(2026, 1, 12, 12, 0, 0, 0, time.UTC) // Monday
	ical, err := GenerateMeetingsICalForTokenFrom(
		ctx,
		db,
		token,
		from,
		1,
		MeetingsOptions{
			Timezone:         "UTC",
			SeedSalt:         "test-salt",
			Links:            ParseMeetingLinks(`Runbook|https://example.com/runbook, <a href="https://example.com/raw">Raw</a>`),
			TemplateTextPath: textPath,
			TemplateHTMLPath: htmlPath,
		},
		func(t time.Time) bool { return t.Weekday() != time.Saturday && t.Weekday() != time.Sunday },
	)
	require.NoError(t, err)

	unfolded := unfoldICalLines(ical)
	require.Contains(t, unfolded, "CUSTOM TEXT: Project shuffle")
	require.Contains(t, unfolded, "X-ALT-DESC;FMTTYPE=text/html")
	require.Contains(t, unfolded, "CUSTOM HTML: Project shuffle")
}

func TestGenerateMeetingsICalForToken_DefaultTemplatesIncludeLinks(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	// internal/calendar -> internal -> repo root.
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	memberID, err := db.AddTeamMember(ctx, "Token Owner", "token@example.com")
	require.NoError(t, err)
	token, err := db.CreateCalendarSubscription(ctx, memberID)
	require.NoError(t, err)

	from := time.Date(2026, 1, 12, 12, 0, 0, 0, time.UTC) // Monday
	ical, err := GenerateMeetingsICalForTokenFrom(
		ctx,
		db,
		token,
		from,
		1,
		MeetingsOptions{
			Timezone: "UTC",
			SeedSalt: "test-salt",
			Links:    ParseMeetingLinks(`Runbook|https://example.com/runbook, <a href="https://example.com/raw">Raw</a>`),
		},
		func(t time.Time) bool { return t.Weekday() != time.Saturday && t.Weekday() != time.Sunday },
	)
	require.NoError(t, err)

	unfolded := unfoldICalLines(ical)
	require.Contains(t, unfolded, "https://example.com/runbook")
	require.Contains(t, unfolded, "https://example.com/raw")
}

func TestGenerateMeetingsICalForToken_UsesDifferentLinksPerMeetingType(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	// internal/calendar -> internal -> repo root.
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	memberID, err := db.AddTeamMember(ctx, "Token Owner", "token@example.com")
	require.NoError(t, err)
	token, err := db.CreateCalendarSubscription(ctx, memberID)
	require.NoError(t, err)

	from := time.Date(2026, 1, 12, 12, 0, 0, 0, time.UTC) // Monday
	ical, err := GenerateMeetingsICalForTokenFrom(
		ctx,
		db,
		token,
		from,
		2, // Monday + Tuesday
		MeetingsOptions{
			Timezone:         "UTC",
			SeedSalt:         "test-salt",
			ProjectLinks:     ParseMeetingLinks("Project|https://example.com/project"),
			MorningLinks:     ParseMeetingLinks("Morning|https://example.com/morning"),
			Links:            ParseMeetingLinks("Fallback|https://example.com/fallback"),
			TemplateTextPath: "",
			TemplateHTMLPath: "",
		},
		func(t time.Time) bool { return t.Weekday() != time.Saturday && t.Weekday() != time.Sunday },
	)
	require.NoError(t, err)

	unfolded := unfoldICalLines(ical)

	// Extract per-event blocks and ensure the right links are in the right meeting.
	projectIdx := strings.Index(unfolded, "UID:meeting-project-20260112@supportrota")
	require.Positive(t, projectIdx)
	morningIdx := strings.Index(unfolded, "UID:meeting-morning-20260113@supportrota")
	require.Positive(t, morningIdx)

	projectBlock := unfolded[projectIdx:]
	if next := strings.Index(projectBlock, "END:VEVENT"); next != -1 {
		projectBlock = projectBlock[:next]
	}

	morningBlock := unfolded[morningIdx:]
	if next := strings.Index(morningBlock, "END:VEVENT"); next != -1 {
		morningBlock = morningBlock[:next]
	}

	require.Contains(t, projectBlock, "https://example.com/project")
	require.NotContains(t, projectBlock, "https://example.com/morning")

	require.Contains(t, morningBlock, "https://example.com/morning")
	require.NotContains(t, morningBlock, "https://example.com/project")
}

func TestGenerateMeetingsForDate_WeekdayReturnsOneMeeting(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	memberID, err := db.AddTeamMember(ctx, "Token Owner", "token@example.com")
	require.NoError(t, err)
	token, err := db.CreateCalendarSubscription(ctx, memberID)
	require.NoError(t, err)
	_ = token

	// Tuesday 2026-01-13 is a business day with the morning meeting.
	day, err := GenerateMeetingsForDate(
		ctx, db, "2026-01-13",
		MeetingsOptions{Timezone: "UTC", SeedSalt: "test"},
		func(t time.Time) bool { return t.Weekday() != time.Saturday && t.Weekday() != time.Sunday },
	)
	require.NoError(t, err)
	assert.Equal(t, "2026-01-13", day.Date)
	require.Len(t, day.Meetings, 1)
	assert.Equal(t, "Morning Shuffle", day.Meetings[0].Name)
	assert.Equal(t, "09:30", day.Meetings[0].StartTime)
	assert.Equal(t, "09:45", day.Meetings[0].EndTime)
	assert.NotEmpty(t, day.Meetings[0].Text)
	assert.NotEmpty(t, string(day.Meetings[0].HTML))
}

func TestGenerateMeetingsForDate_MondayReturnsProjectMeeting(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	memberID, err := db.AddTeamMember(ctx, "Token Owner", "token@example.com")
	require.NoError(t, err)
	_, err = db.CreateCalendarSubscription(ctx, memberID)
	require.NoError(t, err)

	// Monday 2026-01-12 has the project meeting.
	day, err := GenerateMeetingsForDate(
		ctx, db, "2026-01-12",
		MeetingsOptions{Timezone: "UTC", SeedSalt: "test"},
		func(t time.Time) bool { return t.Weekday() != time.Saturday && t.Weekday() != time.Sunday },
	)
	require.NoError(t, err)
	require.Len(t, day.Meetings, 1)
	assert.Equal(t, "Project shuffle", day.Meetings[0].Name)
}

func TestGenerateMeetingsForDate_WeekendReturnsNoMeetings(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Saturday 2026-01-17.
	day, err := GenerateMeetingsForDate(
		ctx, db, "2026-01-17",
		MeetingsOptions{Timezone: "UTC", SeedSalt: "test"},
		func(t time.Time) bool { return t.Weekday() != time.Saturday && t.Weekday() != time.Sunday },
	)
	require.NoError(t, err)
	assert.Empty(t, day.Meetings)
}

func TestGenerateMeetingsForDate_InvalidDate(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = GenerateMeetingsForDate(
		ctx, db, "not-a-date",
		MeetingsOptions{Timezone: "UTC"},
		func(t time.Time) bool { return true },
	)
	assert.Error(t, err)
}

func TestGenerateMeetingsForDate_InvalidToken(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Note: GenerateMeetingsForDate doesn't validate the token. The
	// caller's handler (handleMeetingsDayHTML) is responsible for
	// that. The test is here to confirm the calendar-package
	// function focuses on rendering, not authorization.
	day, err := GenerateMeetingsForDate(
		ctx, db, "2026-01-13",
		MeetingsOptions{Timezone: "UTC"},
		func(t time.Time) bool { return t.Weekday() != time.Saturday && t.Weekday() != time.Sunday },
	)
	require.NoError(t, err)
	assert.NotNil(t, day)
}

// TestGetSupportForDate_PrefersCover verifies the documented precedence:
// when both an original and a cover assignment exist for the date,
// the cover row wins and isCover=true is returned.
func TestGetSupportForDate_PrefersCover(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	originalID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	coverID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	date := "2026-07-15"
	origAssignmentID, err := db.CreateRotaAssignment(ctx, date, originalID, false, nil)
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, date, coverID, true, &origAssignmentID)
	require.NoError(t, err)

	name, isCover, err := getSupportForDate(ctx, db, date)
	require.NoError(t, err)
	assert.Equal(t, "Bob", name)
	assert.True(t, isCover)
}

// TestGetSupportForDate_OnlyOriginal verifies the no-cover branch.
func TestGetSupportForDate_OnlyOriginal(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	date := "2026-07-16"
	_, err = db.CreateRotaAssignment(ctx, date, memberID, false, nil)
	require.NoError(t, err)

	name, isCover, err := getSupportForDate(ctx, db, date)
	require.NoError(t, err)
	assert.Equal(t, "Alice", name)
	assert.False(t, isCover)
}

// TestGetSupportForDate_NoAssignments verifies the empty-result branch.
func TestGetSupportForDate_NoAssignments(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	name, isCover, err := getSupportForDate(ctx, db, "2026-07-17")
	require.NoError(t, err)
	assert.Empty(t, name)
	assert.False(t, isCover)
}

// TestGetSupportForDate_InvalidDate verifies the date-parsing error path.
func TestGetSupportForDate_InvalidDate(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	name, isCover, err := getSupportForDate(ctx, db, "not-a-date")
	require.Error(t, err)
	assert.Empty(t, name)
	assert.False(t, isCover)
}
