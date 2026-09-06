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

// TestGenerateICalForTokenWithOptions_IncludesUpcomingWFHEvents pins
// the new WFH-day calendar affordance. A member with one or more
// approved WFH rows in the lookahead window must surface them as
// separate VEVENTs on the per-member calendar feed so the
// subscriber sees "WFH: <name>" on the day instead of having to
// consult the dashboard. Admin-marked WFH rows must also render,
// distinguished by the "(marked by admin)" banner so subscribers
// can tell a self-requested day from a correction.
func TestGenerateICalForTokenWithOptions_IncludesUpcomingWFHEvents(t *testing.T) {
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

	// Two WFH days: a future self-requested day and today's
	// admin-marked day. Both must surface as VEVENTs in the feed.
	today := time.Now().UTC()
	wfhDate1 := today.AddDate(0, 0, 2).Format("2006-01-02")
	row1, err := db.CreateWFHRequest(ctx, memberID, wfhDate1)
	require.NoError(t, err)
	require.NoError(t, db.UpdateWFHRequestStatus(ctx, row1.ID, database.WFHStatusApproved))
	// MarkAdminWFH needs a real user row to satisfy the marked_by
	// foreign key. Insert directly via SQL so the test is self-
	// contained (the database package doesn't expose CreateUser
	// publicly, but it has an unexported queries field).
	_, err = db.ExecContext(ctx,
		`INSERT INTO users (id, email, name, provider, provider_id, is_admin, is_active) VALUES (?, ?, ?, ?, ?, 1, 1)`,
		"admin-1", "admin@example.com", "Admin", "test", "admin-1")
	require.NoError(t, err)
	err = db.MarkAdminWFH(ctx, "admin-row", memberID, today.Format("2006-01-02"), "admin-1")
	require.NoError(t, err)

	token, err := db.CreateCalendarSubscription(ctx, memberID)
	require.NoError(t, err)

	icsStr, err := GenerateICalForTokenWithOptions(ctx, db, token, 14, SupportCalendarOptions{})
	require.NoError(t, err)

	// Both WFH days surface as VEVENTs.
	assert.Contains(t, icsStr, "SUMMARY:Alice - WFH",
		"per-member WFH events should carry the member-name WFH summary")
	date1Compact := today.AddDate(0, 0, 2).Format("20060102")
	date2Compact := today.Format("20060102")
	assert.Contains(t, icsStr, "DTSTART;VALUE=DATE:"+date1Compact,
		"the future self-requested WFH day should be a VEVENT")
	assert.Contains(t, icsStr, "DTSTART;VALUE=DATE:"+date2Compact,
		"today's admin-marked WFH should also be a VEVENT")
	// The admin-marked day carries the banner in the description.
	assert.Contains(t, icsStr, "(marked by admin)",
		"admin-marked WFH events should carry the banner so subscribers can tell them apart")
}

// TestGenerateICalForTokenWithOptions_ExcludesPendingWFH pins the
// negative case: pending / denied / withdrawn WFH rows must NOT
// show on the calendar. Only confirmed (approved) WFH days
// surface as VEVENTs.
func TestGenerateICalForTokenWithOptions_ExcludesPendingWFH(t *testing.T) {
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

	today := time.Now().UTC()
	pendingDate := today.AddDate(0, 0, 3).Format("2006-01-02")
	pending, err := db.CreateWFHRequest(ctx, memberID, pendingDate)
	require.NoError(t, err)
	require.Equal(t, database.WFHStatusPending, pending.Status)

	token, err := db.CreateCalendarSubscription(ctx, memberID)
	require.NoError(t, err)

	icsStr, err := GenerateICalForTokenWithOptions(ctx, db, token, 14, SupportCalendarOptions{})
	require.NoError(t, err)

	pendingCompact := today.AddDate(0, 0, 3).Format("20060102")
	assert.NotContains(t, icsStr, "DTSTART;VALUE=DATE:"+pendingCompact,
		"pending WFH rows must not surface as VEVENTs on the calendar")
	assert.NotContains(t, icsStr, "WFH: Alice",
		"summary should not appear for a pending-only WFH row")
}

// TestGenerateICalForTokenWithOptions_SuppressesCoveredOriginal pins
// issue #54 — the RED test that reproduces the bug.
//
// Setup: Alice has a HAT day assignment on a future weekday. Bob
// covers for Alice (a cover row with original_assignment_id =
// Alice's row id and member_id = Bob).
//
// Failure mode: when Alice's personal calendar feed is generated, the
// generator walks GetUpcomingAssignments(Alice) and renders every
// row owned by Alice. The cover row has member_id = Bob so it
// doesn't appear in Alice's query at all. But Alice's original
// row is STILL THERE with is_cover=0, so the generator renders a
// "HAT day (Alice)" VEVENT on the day Bob covered for her.
//
// Expected: the covered original must NOT render on Alice's feed.
// Only the cover (which lives on Bob's feed) should surface the
// "HAT day (Bob) (COVER)" event. The dashboard already handles
// this via the presence snapshot's cover-prioritization; the
// calendar generator needs the same treatment.
//
// This test pins the desired behavior and currently fails
// (RED). When the fix lands, this test must pass (GREEN).
func TestGenerateICalForTokenWithTokenWithOptions_SuppressesCoveredOriginal(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	// Pick a future weekday so the assignment is unambiguously
	// inside the lookahead window.
	date := time.Now().UTC().AddDate(0, 0, 5)
	for date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
		date = date.AddDate(0, 0, 1)
	}
	dateStr := date.Format("2006-01-02")

	// Alice's original HAT day.
	originalID, err := db.CreateRotaAssignment(ctx, dateStr, aliceID, false, nil)
	require.NoError(t, err)
	// Bob covers for Alice. The cover row references the original
	// via original_assignment_id so the relationship is queryable.
	_, err = db.CreateRotaAssignment(ctx, dateStr, bobID, true, &originalID)
	require.NoError(t, err)

	aliceToken, err := db.CreateCalendarSubscription(ctx, aliceID)
	require.NoError(t, err)

	aliceICS, err := GenerateICalForTokenWithOptions(ctx, db, aliceToken, 14, SupportCalendarOptions{})
	require.NoError(t, err)

	// The bug: Alice's feed renders the covered original.
	assert.NotContains(t, aliceICS, "HAT day (Alice)",
		"a HAT day that has been covered by Bob must not render on Alice's calendar feed")
	// Specifically, no VEVENT dated compactDate should name Alice.
	// We assert at the level of "the covered day has no Alice-named
	// event" rather than "no event at all" because the assertion
	// would still pass if we accidentally dropped ALL events for the
	// day (a different bug).
	assert.NotContains(t, aliceICS,
		"SUMMARY:HAT day (Alice)",
		"Alice's covered HAT day must not appear in her feed as a HAT event")
	_ = date.Format("20060102") // retained for the docstring's reference

	// Sanity: the cover should still render on Bob's feed (it lives
	// in GetUpcomingAssignments(Bob), not the suppressed list).
	bobToken, err := db.CreateCalendarSubscription(ctx, bobID)
	require.NoError(t, err)
	bobICS, err := GenerateICalForTokenWithOptions(ctx, db, bobToken, 14, SupportCalendarOptions{})
	require.NoError(t, err)
	assert.Contains(t, bobICS, "HAT day (Bob)",
		"Bob's cover should still render on his own feed — covers must surface, not vanish")
}

// TestGenerateOthersICalForToken_SuppressesCoveredOriginal pins the
// matching behavior on the "others" feed: when Alice's HAT day is
// covered by Bob, Member A's "others" feed must show only Bob's
// cover entry — not both Alice's original AND Bob's cover.
//
// Currently the bug also affects this feed: GetAssignmentsByDateRange
// returns both rows (Alice's original and Bob's cover), the filter
// `assignment.MemberID == member.ID` only excludes Alice's row from
// Alice's own feed, and the remaining rows include both
// Alice-original (when another member views) and Bob-cover. We
// expect the others feed for Member C to show only Bob's cover.
func TestGenerateOthersICalForToken_SuppressesCoveredOriginal(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	carolID, err := db.AddTeamMember(ctx, "Carol", "carol@example.com")
	require.NoError(t, err)

	date := time.Now().UTC().AddDate(0, 0, 5)
	for date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
		date = date.AddDate(0, 0, 1)
	}
	dateStr := date.Format("2006-01-02")

	originalID, err := db.CreateRotaAssignment(ctx, dateStr, aliceID, false, nil)
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, dateStr, bobID, true, &originalID)
	require.NoError(t, err)

	carolToken, err := db.CreateCalendarSubscription(ctx, carolID)
	require.NoError(t, err)

	carolOthersICS, err := GenerateOthersICalForToken(ctx, db, carolToken, 14)
	require.NoError(t, err)

	// The covered original (Alice's HAT row) must NOT render on
	// Carol's "others" feed even though Alice is a different member
	// from Carol. The cover by Bob is the only assignment that
	// matters — it carries the actual "who is on HAT" information.
	assert.NotContains(t, carolOthersICS, "HAT day (Alice)",
		"a covered original must not render on any 'others' feed; only the cover should")
	assert.Contains(t, carolOthersICS, "HAT day (Bob)",
		"Bob's cover should render on Carol's 'others' feed — covers must surface, not vanish")
}
