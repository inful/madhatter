package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestFunEffectsFor_ConfettiFiresOnFullTeamOnSiteBusinessDay pins
// the headline case from issue #59: every active member is
// on-site on a normal business day, confetti must fire. Today is
// a Tuesday in March — outside any December window — so snow is
// expected to be false regardless of the SNOW_ENABLED gate.
func TestFunEffectsFor_ConfettiFiresOnFullTeamOnSiteBusinessDay(t *testing.T) {
	t.Setenv("CONFETTI_ENABLED", "true")
	t.Setenv("SNOW_ENABLED", "true")

	now := time.Date(2026, time.March, 10, 9, 0, 0, 0, time.UTC) // Tuesday
	confetti, snow := funEffectsFor(now, true, true, true, true)
	assert.True(t, confetti, "confetti must fire when full-team + business day + enabled")
	assert.False(t, snow, "snow must NOT fire outside December even with snow enabled")
}

// TestFunEffectsFor_ConfettiSuppressedOnWeekend pins the
// TodayIsBusinessDay gate: a weekend day with a somehow-true
// FullTeamOnSite flag must NOT trigger confetti. Mirrors the
// banner's TodayIsBusinessDay gate — neither the banner nor the
// celebration should appear on a weekend.
func TestFunEffectsFor_ConfettiSuppressedOnWeekend(t *testing.T) {
	now := time.Date(2026, time.March, 14, 9, 0, 0, 0, time.UTC) // Saturday
	confetti, snow := funEffectsFor(now, true, false, true, true)
	assert.False(t, confetti, "confetti must NOT fire on a weekend")
	assert.False(t, snow, "snow must NOT fire in March regardless")
}

// TestFunEffectsFor_ConfettiSuppressedOnHoliday pins the same
// gate on the holiday axis: TodayIsBusinessDay=false on a
// holiday, even with full team on-site and confetti enabled.
func TestFunEffectsFor_ConfettiSuppressedOnHoliday(t *testing.T) {
	now := time.Date(2026, time.March, 17, 9, 0, 0, 0, time.UTC) // Tuesday (St. Patrick's, illustrative)
	confetti, _ := funEffectsFor(now, true, false, true, true)
	assert.False(t, confetti, "confetti must NOT fire when TodayIsBusinessDay=false")
}

// TestFunEffectsFor_ConfettiSuppressedWhenNotFullTeam pins the
// negative side of the full-team condition: someone is WFH or on
// leave, so the schedule matrix's FullTeamOnSite flag is false,
// so confetti must not fire even on a business day.
func TestFunEffectsFor_ConfettiSuppressedWhenNotFullTeam(t *testing.T) {
	now := time.Date(2026, time.March, 10, 9, 0, 0, 0, time.UTC)
	confetti, _ := funEffectsFor(now, false, true, true, true)
	assert.False(t, confetti, "confetti must NOT fire when someone is WFH or on leave")
}

// TestFunEffectsFor_ConfettiGateEnvDisabled pins the
// CONFETTI_ENABLED=false escape hatch. The issue is celebratory
// by default, but an operator who finds it distracting on every
// full-team day can disable the effect per deployment.
func TestFunEffectsFor_ConfettiGateEnvDisabled(t *testing.T) {
	now := time.Date(2026, time.March, 10, 9, 0, 0, 0, time.UTC)
	confetti, _ := funEffectsFor(now, true, true, false, true)
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
		_, snow := funEffectsFor(now, false, false, true, true)
		assert.True(t, snow, "snow must fire on December %d regardless of presence", day)
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
		_, snow := funEffectsFor(now, true, true, true, true)
		assert.False(t, snow, "snow must NOT fire in %s", month)
	}
}

// TestFunEffectsFor_SnowGateEnvDisabled pins the SNOW_ENABLED=false
// escape hatch. Snow affects every December day, so an operator
// who finds the effect distracting has a single flag to disable
// it without touching the confetti path.
func TestFunEffectsFor_SnowGateEnvDisabled(t *testing.T) {
	now := time.Date(2026, time.December, 15, 12, 0, 0, 0, time.UTC)
	_, snow := funEffectsFor(now, true, true, true, false)
	assert.False(t, snow, "snow must NOT fire when SNOW_ENABLED=false")
}

// TestFunEffectsFor_BothDisabledWithAllInputsTrue pins the
// all-gates-disabled case: even with a full-team business day in
// December, both effects must stay quiet when both env flags are
// false. Sanity check that the per-effect AND-of-gates pattern
// works as expected.
func TestFunEffectsFor_BothDisabledWithAllInputsTrue(t *testing.T) {
	now := time.Date(2026, time.December, 15, 12, 0, 0, 0, time.UTC)
	confetti, snow := funEffectsFor(now, true, true, false, false)
	assert.False(t, confetti, "confetti must NOT fire when CONFETTI_ENABLED=false")
	assert.False(t, snow, "snow must NOT fire when SNOW_ENABLED=false")
}

// TestFunEffectsFor_BothFireTogether pins the combined positive
// case: a full-team business day in December fires both effects.
// Confetti is the celebratory moment, snow is the seasonal mood;
// they are independent and can fire simultaneously.
func TestFunEffectsFor_BothFireTogether(t *testing.T) {
	now := time.Date(2026, time.December, 15, 9, 0, 0, 0, time.UTC) // Monday in December
	confetti, snow := funEffectsFor(now, true, true, true, true)
	assert.True(t, confetti, "confetti must fire when full-team + business day in December")
	assert.True(t, snow, "snow must fire in December")
}
