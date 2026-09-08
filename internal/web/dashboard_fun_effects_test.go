package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestFunEffectsFor_ConfettiFiresOnFullTeamOnSiteBusinessDay pins
// the headline case from issue #59: every active member is
// on-site on a normal business day, confetti must fire. Today is
// a Tuesday in March — outside any December window and outside
// the autumnal-equinox window — so snow and leaves are both
// expected to be false regardless of their enabled flags.
func TestFunEffectsFor_ConfettiFiresOnFullTeamOnSiteBusinessDay(t *testing.T) {
	t.Setenv("CONFETTI_ENABLED", "true")
	t.Setenv("SNOW_ENABLED", "true")
	t.Setenv("LEAVES_ENABLED", "true")

	now := time.Date(2026, time.March, 10, 9, 0, 0, 0, time.UTC) // Tuesday
	confetti, snow, leaves := funEffectsFor(now, true, true, true, true, true)
	assert.True(t, confetti, "confetti must fire when full-team + business day + enabled")
	assert.False(t, snow, "snow must NOT fire outside December even with snow enabled")
	assert.False(t, leaves, "leaves must NOT fire outside September even with leaves enabled")
}

// TestFunEffectsFor_ConfettiSuppressedOnWeekend pins the
// TodayIsBusinessDay gate: a weekend day with a somehow-true
// FullTeamOnSite flag must NOT trigger confetti. Mirrors the
// banner's TodayIsBusinessDay gate — neither the banner nor the
// celebration should appear on a weekend.
func TestFunEffectsFor_ConfettiSuppressedOnWeekend(t *testing.T) {
	now := time.Date(2026, time.March, 14, 9, 0, 0, 0, time.UTC) // Saturday
	confetti, snow, leaves := funEffectsFor(now, true, false, true, true, true)
	assert.False(t, confetti, "confetti must NOT fire on a weekend")
	assert.False(t, snow, "snow must NOT fire in March regardless")
	assert.False(t, leaves, "leaves must NOT fire on a weekend regardless of date")
}

// TestFunEffectsFor_ConfettiSuppressedOnHoliday pins the same
// gate on the holiday axis: TodayIsBusinessDay=false on a
// holiday, even with full team on-site and confetti enabled.
func TestFunEffectsFor_ConfettiSuppressedOnHoliday(t *testing.T) {
	now := time.Date(2026, time.March, 17, 9, 0, 0, 0, time.UTC) // Tuesday (St. Patrick's, illustrative)
	confetti, _, _ := funEffectsFor(now, true, false, true, true, true)
	assert.False(t, confetti, "confetti must NOT fire when TodayIsBusinessDay=false")
}

// TestFunEffectsFor_ConfettiSuppressedWhenNotFullTeam pins the
// negative side of the full-team condition: someone is WFH or on
// leave, so the schedule matrix's FullTeamOnSite flag is false,
// so confetti must not fire even on a business day.
func TestFunEffectsFor_ConfettiSuppressedWhenNotFullTeam(t *testing.T) {
	now := time.Date(2026, time.March, 10, 9, 0, 0, 0, time.UTC)
	confetti, _, _ := funEffectsFor(now, false, true, true, true, true)
	assert.False(t, confetti, "confetti must NOT fire when someone is WFH or on leave")
}

// TestFunEffectsFor_ConfettiGateEnvDisabled pins the
// CONFETTI_ENABLED=false escape hatch. The issue is celebratory
// by default, but an operator who finds it distracting on every
// full-team day can disable the effect per deployment.
func TestFunEffectsFor_ConfettiGateEnvDisabled(t *testing.T) {
	now := time.Date(2026, time.March, 10, 9, 0, 0, 0, time.UTC)
	confetti, _, _ := funEffectsFor(now, true, true, false, true, true)
	assert.False(t, confetti, "confetti must NOT fire when CONFETTI_ENABLED=false")
}

// TestFunEffectsFor_SnowFiresThroughoutDecember pins the snow
// trigger window: every day in December (1 through 31) fires the
// snow effect when SNOW_ENABLED=true, regardless of weekday or
// business-day status. "Snow in December" is a calendar window,
// not a "team is here" condition — even an empty office gets a
// wintery mood when the calendar says December.
func TestFunEffectsFor_SnowFiresThroughoutDecember(t *testing.T) {
	for day := 1; day <= 31; day++ {
		now := time.Date(2026, time.December, day, 12, 0, 0, 0, time.UTC)
		_, snow, leaves := funEffectsFor(now, false, false, true, true, true)
		assert.True(t, snow, "snow must fire on December %d regardless of presence", day)
		assert.False(t, leaves, "leaves must NOT fire in December regardless of presence", day)
	}
}

// TestFunEffectsFor_SnowNotDecember pins the negative case: every
// non-December month must NOT trigger snow. The test runs every
// month with a fixed day to confirm the month test is the only
// thing the snow branch cares about.
func TestFunEffectsFor_SnowNotDecember(t *testing.T) {
	for month := time.January; month <= time.December; month++ {
		if month == time.December {
			continue
		}
		now := time.Date(2026, month, 15, 12, 0, 0, 0, time.UTC)
		_, snow, leaves := funEffectsFor(now, true, true, true, true, true)
		assert.False(t, snow, "snow must NOT fire in %s", month)
		assert.False(t, leaves, "leaves must NOT fire in %s regardless", month)
	}
}

// TestFunEffectsFor_SnowGateEnvDisabled pins the SNOW_ENABLED=false
// escape hatch. Snow affects every December day, so an operator
// who finds the effect distracting has a single flag to disable
// it without touching the confetti path.
func TestFunEffectsFor_SnowGateEnvDisabled(t *testing.T) {
	now := time.Date(2026, time.December, 15, 12, 0, 0, 0, time.UTC)
	_, snow, _ := funEffectsFor(now, true, true, true, false, true)
	assert.False(t, snow, "snow must NOT fire when SNOW_ENABLED=false")
}

// TestFunEffectsFor_AllDisabledWithAllInputsTrue pins the
// all-gates-disabled case: even with a full-team business day in
// December, every effect must stay quiet when every env flag is
// false. Sanity check that the per-effect AND-of-gates pattern
// works as expected.
func TestFunEffectsFor_AllDisabledWithAllInputsTrue(t *testing.T) {
	now := time.Date(2026, time.December, 15, 12, 0, 0, 0, time.UTC)
	confetti, snow, leaves := funEffectsFor(now, true, true, false, false, false)
	assert.False(t, confetti, "confetti must NOT fire when CONFETTI_ENABLED=false")
	assert.False(t, snow, "snow must NOT fire when SNOW_ENABLED=false")
	assert.False(t, leaves, "leaves must NOT fire when LEAVES_ENABLED=false")
}

// TestFunEffectsFor_ConfettiAndSnowFireTogether pins the combined
// positive case for the two always-on effects: a full-team
// business day in December fires both effects. Confetti is the
// celebratory moment, snow is the seasonal mood; they are
// independent and can fire simultaneously.
func TestFunEffectsFor_ConfettiAndSnowFireTogether(t *testing.T) {
	now := time.Date(2026, time.December, 15, 9, 0, 0, 0, time.UTC) // Monday in December
	confetti, snow, leaves := funEffectsFor(now, true, true, true, true, true)
	assert.True(t, confetti, "confetti must fire when full-team + business day in December")
	assert.True(t, snow, "snow must fire in December")
	assert.False(t, leaves, "leaves must NOT fire in December")
}

// TestFunEffectsFor_LeavesFiresOnAutumnalEquinoxWithFullTeam pins
// the headline case for the leaves effect: September 23 (the
// first day of autumn), full team on-site, business day, all
// three gates enabled. Confetti also fires because the leaves
// effect piggybacks on the same full-team condition.
func TestFunEffectsFor_LeavesFiresOnAutumnalEquinoxWithFullTeam(t *testing.T) {
	now := time.Date(2026, time.September, 23, 9, 0, 0, 0, time.UTC) // Wednesday
	confetti, snow, leaves := funEffectsFor(now, true, true, true, true, true)
	assert.True(t, confetti, "confetti must also fire when full-team + business day")
	assert.False(t, snow, "snow must NOT fire in September even with snow enabled")
	assert.True(t, leaves, "leaves must fire on September 23 with full team on a business day")
}

// TestFunEffectsFor_LeavesSuppressedBeforeAndAfterSept23 pins the
// date axis: only September 23 triggers the leaves effect. Days
// 22 and 24 of September must NOT fire, even with the full team
// on-site — the equinox is the one specific day.
func TestFunEffectsFor_LeavesSuppressedBeforeAndAfterSept23(t *testing.T) {
	for _, day := range []int{1, 15, 22, 24, 30} {
		now := time.Date(2026, time.September, day, 9, 0, 0, 0, time.UTC)
		_, _, leaves := funEffectsFor(now, true, true, true, true, true)
		assert.False(t, leaves, "leaves must NOT fire on September %d (only September 23 qualifies)", day)
	}
}

// TestFunEffectsFor_LeavesSuppressedInOtherMonths pins the month
// axis: every other month must NOT trigger the leaves effect on
// day 23, even with full team on-site. The check matters because
// the date test alone could pass if the implementation only
// checked day-of-month — September is what makes the date
// autumnal.
func TestFunEffectsFor_LeavesSuppressedInOtherMonths(t *testing.T) {
	for month := time.January; month <= time.December; month++ {
		if month == time.September {
			continue
		}
		now := time.Date(2026, month, 23, 9, 0, 0, 0, time.UTC)
		_, _, leaves := funEffectsFor(now, true, true, true, true, true)
		assert.False(t, leaves, "leaves must NOT fire on day 23 of %s", month)
	}
}

// TestFunEffectsFor_LeavesSuppressedOnWeekend pins the
// TodayIsBusinessDay gate: a September 23 that lands on a
// weekend (e.g. 2018, 2024, 2029) must NOT trigger leaves even
// with the full team on-site. The matrix would have no rows for
// a weekend day, so the FullTeamOnSite flag is structurally
// false, but the explicit guard makes the contract readable.
func TestFunEffectsFor_LeavesSuppressedOnWeekend(t *testing.T) {
	// 2023-09-23 is a Saturday; pick a year where Sept 23 lands
	// on a weekend to exercise the branch deterministically.
	now := time.Date(2023, time.September, 23, 12, 0, 0, 0, time.UTC)
	_, _, leaves := funEffectsFor(now, true, false, true, true, true)
	assert.False(t, leaves, "leaves must NOT fire on Sept 23 that is a weekend")
}

// TestFunEffectsFor_LeavesGateEnvDisabled pins the
// LEAVES_ENABLED=false escape hatch. The effect is tied to one
// specific day a year, so the operator-level disable matters
// even more than for the daily-running snow storm — an operator
// who wants to keep their dashboard quiet on the equinox can
// flip one flag.
func TestFunEffectsFor_LeavesGateEnvDisabled(t *testing.T) {
	now := time.Date(2026, time.September, 23, 9, 0, 0, 0, time.UTC)
	_, _, leaves := funEffectsFor(now, true, true, true, true, false)
	assert.False(t, leaves, "leaves must NOT fire when LEAVES_ENABLED=false")
}

// TestFunEffectsFor_LeavesSuppressedWhenNotFullTeam pins the
// negative side of the full-team condition for the leaves
// effect: someone is WFH or on leave on the equinox, so the
// FullTeamOnSite flag is false, so leaves must NOT fire. The
// user's request was specifically "everybody on-site wins the
// effect" — partial coverage skips the celebration.
func TestFunEffectsFor_LeavesSuppressedWhenNotFullTeam(t *testing.T) {
	now := time.Date(2026, time.September, 23, 9, 0, 0, 0, time.UTC)
	_, _, leaves := funEffectsFor(now, false, true, true, true, true)
	assert.False(t, leaves, "leaves must NOT fire on Sept 23 when someone is WFH or on leave")
}
