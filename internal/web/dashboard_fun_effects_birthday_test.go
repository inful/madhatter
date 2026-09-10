package web

import (
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFunEffectsFor_BirthdayFiresToday pins the headline case
// for #60's follow-up blast: a member has a birthday today
// on a normal business day, the burst must fire. Today is
// March 10 — outside any December window and outside the
// equinox — so confetti, snow and leaves stay false.
func TestFunEffectsFor_BirthdayFiresToday(t *testing.T) {
	now := time.Date(2026, time.March, 10, 9, 0, 0, 0, time.UTC)                       // Tuesday
	_, _, _, birthday := funEffectsFor(now, false, true, true, true, true, true, true) //nolint:dogsled // we only care about the birthday branch
	assert.True(t, birthday,
		"birthday blast must fire on a business day when a member has a birthday today")
}

// TestFunEffectsFor_BirthdaySuppressedOnWeekend pins the
// business-day gate for the blast. A birthday on Saturday
// must NOT fire the blast — the existing banner's
// TodayIsBusinessDay gate applies here too, mirroring the
// full-team and leaves gates.
func TestFunEffectsFor_BirthdaySuppressedOnWeekend(t *testing.T) {
	now := time.Date(2026, time.March, 14, 9, 0, 0, 0, time.UTC)                        // Saturday
	_, _, _, birthday := funEffectsFor(now, false, false, true, true, true, true, true) //nolint:dogsled // we only care about the birthday branch
	assert.False(t, birthday,
		"birthday blast must NOT fire on a weekend even with a birthday today")
}

// TestFunEffectsFor_BirthdaySuppressedOnHoliday pins the
// holiday axis: TodayIsBusinessDay=false on a holiday, even
// with a birthday today and the gate enabled.
func TestFunEffectsFor_BirthdaySuppressedOnHoliday(t *testing.T) {
	now := time.Date(2026, time.March, 17, 9, 0, 0, 0, time.UTC)                        // Tuesday (St. Patrick's, illustrative)
	_, _, _, birthday := funEffectsFor(now, false, false, true, true, true, true, true) //nolint:dogsled // we only care about the birthday branch
	assert.False(t, birthday,
		"birthday blast must NOT fire on a holiday even with a birthday today")
}

// TestFunEffectsFor_BirthdaySuppressedWhenFutureOnly pins the
// "today vs. this week" guard: the banner uses the inclusive
// 7-day window, but the BLAST only fires on the exact day
// (DaysUntil=0). A birthday in 3 days must render the banner
// copy but NOT the burst — the calm "birthday this week"
// copy is the right register, not a confetti blast.
func TestFunEffectsFor_BirthdaySuppressedWhenFutureOnly(t *testing.T) {
	now := time.Date(2026, time.March, 10, 9, 0, 0, 0, time.UTC)
	_, _, _, birthday := funEffectsFor(now, false, true, false, true, true, true, true) //nolint:dogsled // we only care about the birthday branch
	assert.False(t, birthday,
		"birthday blast must NOT fire for a future birthday — the 'this week' banner is enough")
}

// TestFunEffectsFor_BirthdayGateEnvDisabled pins the
// BIRTHDAY_CONFETTI_ENABLED=false escape hatch. The blast is
// celebratory by default; an operator who finds it
// distracting on every birthday can disable it per deployment
// without touching the confetti or snow paths.
func TestFunEffectsFor_BirthdayGateEnvDisabled(t *testing.T) {
	now := time.Date(2026, time.March, 10, 9, 0, 0, 0, time.UTC)
	_, _, _, birthday := funEffectsFor(now, false, true, true, true, true, true, false) //nolint:dogsled // birthdayEnabled=false
	assert.False(t, birthday,
		"birthday blast must NOT fire when BIRTHDAY_CONFETTI_ENABLED=false")
}

// TestFunEffectsFor_BirthdayAndFullTeamFireTogether pins the
// combined positive case: a birthday on a full-team
// business day must fire both the full-team burst and the
// birthday blast. They're independent effects — the user
// gets two distinct celebrations.
func TestFunEffectsFor_BirthdayAndFullTeamFireTogether(t *testing.T) {
	now := time.Date(2026, time.March, 10, 9, 0, 0, 0, time.UTC)
	confetti, _, _, birthday := funEffectsFor(now, true, true, true, true, true, true, true)
	assert.True(t, confetti,
		"confetti must fire when full-team + business day")
	assert.True(t, birthday,
		"birthday blast must also fire when a member has a birthday today")
}

// TestHasBirthdayToday_HappyPath pins the dashboard
// data-layer probe: when UpcomingBirthdays is loaded with
// a member whose DaysUntil=0, hasBirthdayToday() returns
// true. This is the input contract for the funEffectsFor
// call that fun-effects.js reads off the rendered
// data-birthday-confetti attribute.
func TestHasBirthdayToday_HappyPath(t *testing.T) {
	data := map[string]any{
		"UpcomingBirthdays": []database.UpcomingBirthday{
			{Name: "Alice", DaysUntil: 0, BirthdayMonthDay: "09-15"},
		},
	}
	require.True(t, hasBirthdayToday(data),
		"a member with DaysUntil=0 must register as 'birthday today'")
}

// TestHasBirthdayToday_FutureOnly pins the negative path:
// when the banner shows 'X's birthday this week' (DaysUntil=3),
// the probe must NOT fire the blast. The blast only fires on
// the exact day.
func TestHasBirthdayToday_FutureOnly(t *testing.T) {
	data := map[string]any{
		"UpcomingBirthdays": []database.UpcomingBirthday{
			{Name: "Alice", DaysUntil: 3, BirthdayMonthDay: "09-18"},
		},
	}
	require.False(t, hasBirthdayToday(data),
		"a future birthday (DaysUntil=3) must NOT register as 'birthday today'")
}

// TestHasBirthdayToday_NoBirthdays pins the no-op path: an
// empty UpcomingBirthdays slice returns false. The probe
// must not panic on the empty slice or missing key.
func TestHasBirthdayToday_NoBirthdays(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		data := map[string]any{"UpcomingBirthdays": []database.UpcomingBirthday{}}
		require.False(t, hasBirthdayToday(data))
	})
	t.Run("missing key", func(t *testing.T) {
		data := map[string]any{}
		require.False(t, hasBirthdayToday(data))
	})
	t.Run("wrong type", func(t *testing.T) {
		data := map[string]any{"UpcomingBirthdays": "not-a-slice"}
		require.False(t, hasBirthdayToday(data),
			"a type assertion failure must yield false rather than panic")
	})
}
