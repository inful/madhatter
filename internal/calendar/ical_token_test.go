package calendar

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerateICalForTokenWithOptions_InvalidToken covers the most common
// failure: a calendar subscription URL that no longer resolves to a member,
// either because the subscription was deleted or the token was corrupted.
// The function must surface the database error rather than producing an
// empty calendar silently — a stale token is indistinguishable from a
// working one to a calendar client otherwise.
func TestGenerateICalForTokenWithOptions_InvalidToken(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = GenerateICalForTokenWithOptions(ctx, db, "not-a-real-token", 7, SupportCalendarOptions{})
	require.Error(t, err, "an invalid token must surface a database error")
	assert.Contains(t, err.Error(), "invalid token", "the wrapping error names the failure for callers and operators")
}

// TestGenerateICalForTokenWithOptions_NoUpcomingAssignments covers the
// case where a member has a valid subscription but no assignments in the
// configured lookahead window. The function must still produce a valid
// ICS document — calendar clients expect an empty calendar to render as
// "no events" rather than failing the subscription.
func TestGenerateICalForTokenWithOptions_NoUpcomingAssignments(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	token, err := db.CreateCalendarSubscription(ctx, memberID)
	require.NoError(t, err)

	icsStr, err := GenerateICalForTokenWithOptions(ctx, db, token, 7, SupportCalendarOptions{})
	require.NoError(t, err)
	// A valid empty iCalendar still has the VCALENDAR wrapper and the
	// closing tag; verify the document is structurally well-formed.
	assert.Contains(t, icsStr, "BEGIN:VCALENDAR")
	assert.Contains(t, icsStr, "END:VCALENDAR")
	assert.NotContains(t, icsStr, "HAT day (Alice)")
}

// TestGenerateICalForTokenWithOptions_IncludesUpcomingAssignments covers
// the happy path: a member has a HAT day assignment within the lookahead
// window, and the function must emit an event naming them.
func TestGenerateICalForTokenWithOptions_IncludesUpcomingAssignments(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Pick a future weekday so the assignment is unambiguously inside
	// the lookahead window and weekend-skipping logic cannot collapse
	// it onto the test run date.
	date := time.Now().UTC().AddDate(0, 0, 5)
	for date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
		date = date.AddDate(0, 0, 1)
	}
	dateStr := date.Format("2006-01-02")
	_, err = db.CreateRotaAssignment(ctx, dateStr, memberID, false, nil)
	require.NoError(t, err)

	token, err := db.CreateCalendarSubscription(ctx, memberID)
	require.NoError(t, err)

	icsStr, err := GenerateICalForTokenWithOptions(ctx, db, token, 14, SupportCalendarOptions{})
	require.NoError(t, err)
	assert.Contains(t, icsStr, "HAT day (Alice)", "the subscriber's own assignment must appear in their calendar feed")
	// ICS dates use the compact YYYYMMDD form per RFC 5545; compare
	// against the same form so this doesn't break on the date format.
	compactDate := date.Format("20060102")
	assert.Contains(t, icsStr, compactDate, "the assignment date must appear in the event payload")
}
