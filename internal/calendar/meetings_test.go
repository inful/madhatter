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
	require.Contains(t, ics1, "Shuffle order")
	require.Contains(t, ics1, "JazzHands")
	require.Contains(t, ics1, "X-ALT-DESC;FMTTYPE=text/html")
	require.Contains(t, unfoldICalLines(ics1), "<a href=\"https://teams.example.com/meet\"")
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
