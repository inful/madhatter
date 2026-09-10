package web

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadDashboardForTest renders the dashboard template against a
// hand-built data map and returns the body as a string. Mirrors
// the helper in dashboard_no_one_wfh_test.go so the new
// birthday tests follow the same pattern as the existing
// celebratory-banners (full-team-banner / no-wfh-banner).
func loadDashboardForTest(t *testing.T, data map[string]any) string {
	t.Helper()
	tmpl, err := parseTemplates()
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	require.NoError(t, tmpl.ExecuteTemplate(rec, "dashboard.html", data))
	return rec.Body.String()
}

// TestDashboard_BirthdayBanner_Today is the happy path: a
// member with a birthdate today renders the banner copy "X's
// birthday today!". Pinned by #60.
func TestDashboard_BirthdayBanner_Today(t *testing.T) {
	db, _, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	today := time.Now().UTC()
	bday := time.Date(1990, today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	id, err := db.AddTeamMember(t.Context(), "Alice", "alice@example.com", &bday)
	require.NoError(t, err)

	got, err := db.GetUpcomingBirthdays(t.Context(), today, 7)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, id, got[0].MemberID)
	assert.Equal(t, 0, got[0].DaysUntil)

	// Probe — render dashboard template with the UpcomingBirthdays
	// already populated.
	body := loadDashboardForTest(t, map[string]any{
		"TodayIsBusinessDay": true,
		"UpcomingBirthdays":  got,
		"Template":           "dashboard",
	})
	assert.Contains(t, body, "Alice",
		"the dashboard must name the birthday member")
	assert.Contains(t, body, "birthday",
		"the dashboard must surface a birthday banner")
	assert.Contains(t, body, "🎂",
		"the dashboard must show the cake emoji")
}

// TestDashboard_BirthdayBanner_ThisWeek pins the "not today
// but in the next 7 days" branch. The banner copy adapts:
// instead of "X's birthday today" it shows the upcoming list.
func TestDashboard_BirthdayBanner_ThisWeek(t *testing.T) {
	db, _, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	now := time.Now().UTC()
	bday := time.Date(1990, now.Month(), now.Day()+3, 0, 0, 0, 0, time.UTC)
	id, err := db.AddTeamMember(t.Context(), "Bob", "bob@example.com", &bday)
	require.NoError(t, err)

	got, err := db.GetUpcomingBirthdays(t.Context(), now, 7)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, id, got[0].MemberID)
	assert.Equal(t, 3, got[0].DaysUntil)

	body := loadDashboardForTest(t, map[string]any{
		"TodayIsBusinessDay": true,
		"UpcomingBirthdays":  got,
		"Template":           "dashboard",
	})
	assert.Contains(t, body, "Bob",
		"the dashboard must name the upcoming birthday member")
	assert.Contains(t, body, "this week",
		"the banner copy must distinguish 'this week' from 'today'")
}

// TestDashboard_BirthdayBanner_SuppressedOnWeekend pins the
// suppress-on-non-business-day guard. The existing celebratory
// banners (full-team, no-wfh) hide on weekends and holidays;
// the birthday banner follows the same rule — a birthday on
// Saturday doesn't deserve a Friday-evening banner that
// follows the admin to their weekend.
func TestDashboard_BirthdayBanner_SuppressedOnWeekend(t *testing.T) {
	db, _, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	now := time.Now().UTC()
	bday := time.Date(1990, now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	_, err := db.AddTeamMember(t.Context(), "Alice", "alice@example.com", &bday)
	require.NoError(t, err)

	got, err := db.GetUpcomingBirthdays(t.Context(), now, 7)
	require.NoError(t, err)

	body := loadDashboardForTest(t, map[string]any{
		"TodayIsBusinessDay": false, // weekend / holiday
		"UpcomingBirthdays":  got,
		"Template":           "dashboard",
	})
	assert.NotContains(t, body, "🎂",
		"the birthday banner must be suppressed when today is not a business day")
}

// TestDashboard_BirthdayBanner_NoBirthdays pins the no-op
// path: when no member has a birthdate in the window, the
// banner doesn't render and the dashboard renders normally.
func TestDashboard_BirthdayBanner_NoBirthdays(t *testing.T) {
	db, _, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	now := time.Now().UTC()
	got, err := db.GetUpcomingBirthdays(t.Context(), now, 7)
	require.NoError(t, err)
	assert.Empty(t, got,
		"sanity: empty result when no member has a birthdate")

	body := loadDashboardForTest(t, map[string]any{
		"TodayIsBusinessDay": true,
		"UpcomingBirthdays":  got,
		"Template":           "dashboard",
	})
	assert.NotContains(t, body, "🎂",
		"no banner when no birthdays are in the window")
}

// TestDashboard_BirthdayBanner_HidesYear pins the privacy
// guard: the banner copy must NOT include the stored year of
// birth. The probe returns BirthYear for admin auditing, but
// the dashboard template must surface only Name + DaysUntil.
func TestDashboard_BirthdayBanner_HidesYear(t *testing.T) {
	db, _, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	now := time.Now().UTC()
	bday := time.Date(1990, now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	_, err := db.AddTeamMember(t.Context(), "Alice", "alice@example.com", &bday)
	require.NoError(t, err)

	got, err := db.GetUpcomingBirthdays(t.Context(), now, 7)
	require.NoError(t, err)
	require.NotEmpty(t, got)
	require.Equal(t, 1990, got[0].BirthYear,
		"sanity: probe preserves the year for audit")

	body := loadDashboardForTest(t, map[string]any{
		"TodayIsBusinessDay": true,
		"UpcomingBirthdays":  got,
		"Template":           "dashboard",
	})
	assert.NotContains(t, body, "1990",
		"the banner must NOT surface the year of birth")
}

// TestDashboard_BirthdayBanner_TwoMembersThisWeek pins the
// "list them all" branch: when 2+ members have birthdays in
// the window, the banner copy lists each name.
func TestDashboard_BirthdayBanner_TwoMembersThisWeek(t *testing.T) {
	db, _, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	now := time.Now().UTC()
	bday1 := time.Date(1990, now.Month(), now.Day()+2, 0, 0, 0, 0, time.UTC)
	bday2 := time.Date(1991, now.Month(), now.Day()+5, 0, 0, 0, 0, time.UTC)
	_, err := db.AddTeamMember(t.Context(), "Alice", "alice@example.com", &bday1)
	require.NoError(t, err)
	_, err = db.AddTeamMember(t.Context(), "Bob", "bob@example.com", &bday2)
	require.NoError(t, err)

	got, err := db.GetUpcomingBirthdays(t.Context(), now, 7)
	require.NoError(t, err)
	require.Len(t, got, 2)

	body := loadDashboardForTest(t, map[string]any{
		"TodayIsBusinessDay": true,
		"UpcomingBirthdays":  got,
		"Template":           "dashboard",
	})
	assert.Contains(t, body, "Alice")
	assert.Contains(t, body, "Bob")
}
