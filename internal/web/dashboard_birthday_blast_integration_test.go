package web

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadDashboardData_BirthdayBlast_IntegrationFlow is the
// regression test for the confetti-blast-doesn't-fire bug
// (issue: blast never fires for the birthday kid). Existing
// tests call renderDashboardForTest with a hand-built data map
// that bypasses loadTodayContext, OR call funEffectsFor directly,
// OR test loadDashboardData with no birthday member — none of
// them exercise the integrated data flow with a birthday.
//
// The bug: data["UpcomingBirthdays"] was populated AFTER
// loadTodayContext ran, so hasBirthdayToday(data) always
// returned false at the point funEffectsFor was called. The
// birthday banner rendered correctly because the template reads
// UpcomingBirthdays directly, but the data-birthday-confetti
// attribute was always false.
//
// This test pins the integration: loadDashboardData with a
// member whose birthday is today, on a business day, must set
// FunEffectsBirthday=true so the JS recipe fires.
//
// The probe uses today's MM-DD so the test is independent of
// the wall clock. loadDashboardData uses time.Now() for its
// "now" so the assertion holds for any test run date.
func TestLoadDashboardData_BirthdayBlast_IntegrationFlow(t *testing.T) {
	db, h, cleanup := setupDashboardTestDB(t)
	defer cleanup()

	// setupDashboardTestDB builds the Handler via a literal
	// so the fun-effects gates are Go zero values (false).
	// This test exercises the birthday-blast gate specifically,
	// so flip it on — the test pins the data flow, not the
	// env-var resolution (handler_fun_effects_test.go covers
	// that separately).
	h.birthdayConfettiEnabled = true

	ctx := context.Background()

	// Pin the birthdate to today's MM-DD — but reject weekend
	// and holiday dates so the test is deterministic. If today
	// is Sat/Sun, skip; otherwise loadTodayContext will mark
	// today as not a business day and the blast gate would
	// short-circuit. (The bug is independent of the weekend
	// guard; the test exercises the integration on a business
	// day specifically.)
	now := time.Now().UTC()
	weekday := now.Weekday()
	if weekday == time.Saturday || weekday == time.Sunday {
		t.Skipf("today is %s; birthday-blast gate short-circuits on weekends", weekday)
	}

	bday := time.Date(1990, now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com", &bday)
	require.NoError(t, err)

	data := map[string]any{}
	h.loadDashboardData(ctx, data)

	assert.True(t, data["FunEffectsBirthday"].(bool),
		"FunEffectsBirthday must be true on a birthday-today business day; got %v (UpcomingBirthdays=%v)",
		data["FunEffectsBirthday"], data["UpcomingBirthdays"])
}
