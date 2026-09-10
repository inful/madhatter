package calendar

import (
	"strings"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestICalGenerator_AddBirthday pins the headline case for #60
// follow-up: a member with a birthdate renders as an all-day
// VEVENT with the cake emoji in the SUMMARY. The event uses
// RRULE:FREQ=YEARLY so the calendar stays accurate forever
// without an annual refresh.
func TestICalGenerator_AddBirthday(t *testing.T) {
	generator := NewICalGenerator()

	// Pick a date deliberately NOT today so the test is
	// independent of the wall clock. The year is the
	// privacy-sensitive value — the event must surface only
	// MM-DD in the SUMMARY (see TestICalGenerator_AddBirthday_HidesYear).
	birthDate := time.Date(1990, 5, 15, 0, 0, 0, 0, time.UTC)
	err := generator.AddBirthday("Alice", birthDate)
	require.NoError(t, err)

	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	assert.Contains(t, icalStr, "BEGIN:VEVENT",
		"a birthday event must render as a VEVENT")
	assert.Contains(t, icalStr, "Alice",
		"the SUMMARY must name the birthday member")
	assert.Contains(t, icalStr, "🎂",
		"the SUMMARY must carry the cake emoji (the visual cue)")
	assert.Contains(t, icalStr, "RRULE:FREQ=YEARLY",
		"a birthday event must recur yearly so the calendar stays accurate forever")
	assert.Contains(t, icalStr, "DTSTART;VALUE=DATE:",
		"a birthday event must be an all-day VEVENT")
}

// TestICalGenerator_AddBirthday_HidesYear pins the privacy
// guard: the SUMMARY copy must NOT include the year of birth.
// The probe (db.UpcomingBirthday.BirthYear) preserves the year
// for admin auditing, but the calendar copy surfaces only MM-DD
// — the year is private internal data, same contract as the
// dashboard banner.
func TestICalGenerator_AddBirthday_HidesYear(t *testing.T) {
	generator := NewICalGenerator()

	birthDate := time.Date(1990, 5, 15, 0, 0, 0, 0, time.UTC)
	require.NoError(t, generator.AddBirthday("Alice", birthDate))

	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	assert.NotContains(t, icalStr, "1990",
		"the SUMMARY must NOT surface the year of birth — only MM-DD")
	// The DTSTART only carries the MM-DD, not the year. The
	// RRULE=FREQ=YEARLY handles year-to-year rollover, so the
	// calendar surfaces the event on the right day every year
	// without an annual refresh.
	assert.NotContains(t, icalStr, "DTSTART;VALUE=DATE:1990",
		"DTSTART must use the year of the subscription window, not the year of birth")
}

// TestICalGenerator_AddBirthday_StableUID pins the UID
// contract: every call for the same member must produce a
// VEVENT with the same UID so a calendar client merges the
// entries on regenerate (no duplicate event in the user's
// grid). The calendar client dedupes by UID; the calendar
// generator's contract is "same member → same UID". We pin
// the UID pattern so future refactors can't break it.
func TestICalGenerator_AddBirthday_StableUID(t *testing.T) {
	generator := NewICalGenerator()

	birthDate := time.Date(1990, 5, 15, 0, 0, 0, 0, time.UTC)
	require.NoError(t, generator.AddBirthday("Alice", birthDate))

	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	assert.Contains(t, icalStr, "UID:birthday-Alice-05-15",
		"the UID must be stable across regenerations: birthday-<sanitized-name>-<MM-DD>")
}

// TestICalGenerator_AddBirthday_TwoMembers pins the multi-member
// case: two members render as two distinct VEVENTs, each with
// their own UID and summary.
func TestICalGenerator_AddBirthday_TwoMembers(t *testing.T) {
	generator := NewICalGenerator()

	require.NoError(t, generator.AddBirthday("Alice", time.Date(1990, 5, 15, 0, 0, 0, 0, time.UTC)))
	require.NoError(t, generator.AddBirthday("Bob", time.Date(1992, 9, 3, 0, 0, 0, 0, time.UTC)))

	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	count := strings.Count(icalStr, "BEGIN:VEVENT")
	assert.Equal(t, 2, count, "two distinct members must render two distinct VEVENTs")
	assert.Contains(t, icalStr, "Alice")
	assert.Contains(t, icalStr, "Bob")
}

// TestICalGenerator_AddBirthday_StripsControlCharsFromName is
// the security-review #7 safety net applied to the birthday
// path. CRLF / NUL in the member name must not survive into
// the SUMMARY line (otherwise an attacker who controls a
// member name could forge a new ICS line).
func TestICalGenerator_AddBirthday_StripsControlCharsFromName(t *testing.T) {
	generator := NewICalGenerator()

	birthDate := time.Date(1990, 5, 15, 0, 0, 0, 0, time.UTC)
	malicious := "Alice\r\nX-WR-CALNAME:forged"
	require.NoError(t, generator.AddBirthday(malicious, birthDate))

	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	assert.NotContains(t, icalStr, "\r\n",
		"CRLF in the member name must be stripped before the SUMMARY line")
	// Exactly one BEGIN:VEVENT — a forged CRLF would produce a
	// second VEVENT block.
	assert.Equal(t, 1, strings.Count(icalStr, "BEGIN:VEVENT"),
		"a CRLF payload must NOT forge a new ICS line (one VEVENT block only)")
	// The sanitized form (CRLF → single space) appears on the
	// single SUMMARY line.
	assert.Contains(t, icalStr, "Alice X-WR-CALNAME:forged",
		"the sanitized form must still appear in the SUMMARY (single line)")
}

// TestAddBirthdayEventsFromMembers pins the helper that walks
// a slice of database.CalendarBirthday and emits one event
// per row. This is the calendar's entry point for the "all
// members with a birthdate" probe — both the per-member
// subscription feed and the team-wide feed call it.
func TestAddBirthdayEventsFromMembers(t *testing.T) {
	generator := NewICalGenerator()

	birthdays := []database.CalendarBirthday{
		{Name: "Alice", Birthdate: time.Date(1990, 5, 15, 0, 0, 0, 0, time.UTC)},
		{Name: "Bob", Birthdate: time.Date(1992, 9, 3, 0, 0, 0, 0, time.UTC)},
		{Name: "", Birthdate: time.Date(1990, 5, 15, 0, 0, 0, 0, time.UTC)}, // empty name → skipped
		{Name: "Carol"}, // zero birthdate → skipped
	}
	require.NoError(t, AddBirthdayEventsFromMembers(generator, birthdays))

	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	count := strings.Count(icalStr, "BEGIN:VEVENT")
	assert.Equal(t, 2, count, "only members with a non-empty name AND a real birthdate render")
	assert.Contains(t, icalStr, "Alice")
	assert.Contains(t, icalStr, "Bob")
	assert.NotContains(t, icalStr, "Carol",
		"a member without a birthdate must not render a VEVENT")
}
