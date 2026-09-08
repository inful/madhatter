package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// renderDashboardForNoOneWFH executes the dashboard template with a
// hand-built scheduleMatrix and the TodayNoOneWFH / TodayIsBusinessDay
// flags set per call. The other dashboard sections gracefully
// degrade to "no data" when their inputs are missing, which is
// exactly what we want for an isolated test of the no-WFH marker.
func renderDashboardForNoOneWFH(t *testing.T, todayNoOneWFH, todayIsBusinessDay, fullTeamOnSite bool) string {
	t.Helper()

	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	// Two-column matrix: today has zero WFH and 3 on-site (the
	// happy case for the marker), tomorrow has 1 WFH. Today's
	// LeaveCount is 1 so the full-team flag stays false by
	// default — but the test fixture mirrors the data map's
	// fullTeamOnSite parameter so the matrix and the data
	// layer stay in sync. The dashboard banner reads from the
	// data map (TodayFullTeamOnSite); the matrix column header
	// and mobile card read from the matrix's day FullTeamOnSite
	// field. Setting both consistently is what lets the test
	// assert mutual exclusion at either layer.
	matrix := scheduleMatrix{
		Days: []scheduleMatrixDay{
			{
				DateISO:        "2026-09-08",
				DateDisplay:    "Tue 8 Sep",
				IsToday:        true,
				AtWorkCount:    3,
				WFHCount:       0,
				LeaveCount:     1,
				FullTeamOnSite: fullTeamOnSite,
			},
			{
				DateISO:     "2026-09-09",
				DateDisplay: "Wed 9 Sep",
				AtWorkCount: 2,
				WFHCount:    1,
			},
		},
		Rows: []scheduleMatrixRow{
			{Member: database.TeamMember{Name: "Alice"}, Cells: []scheduleMatrixCell{
				{Status: "onsite", Label: "On-site"},
				{Status: "onsite", Label: "On-site"},
			}},
		},
	}

	data := map[string]any{
		"User":                map[string]any{"Email": "alice@example.com", "Name": "Alice"},
		"IsAdmin":             false,
		"Template":            "dashboard",
		"ScheduleMatrix":      matrix,
		"TodayIsWeekend":      false,
		"TodayIsHoliday":      false,
		"TodayIsBusinessDay":  todayIsBusinessDay,
		"TodayFullTeamOnSite": fullTeamOnSite,
		"TodayNoOneWFH":       todayNoOneWFH,
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))
	return w.Body.String()
}

// TestDashboard_NoOneWFH_BannerAndMatrixBadge pins the no-WFH
// marker surfaces when the flag is true and it is a business day
// and the team isn't fully on-site (so the full-team banner
// doesn't take precedence):
//   - the dashboard banner above the status card reads
//     "No one is WFH today" with the meeting-friendly subtitle,
//   - the matrix column header for today carries the .no-wfh-badge
//     chip with the 🏢 emoji and "All in" label,
//   - the mobile day card for today carries the .mobile-no-wfh-badge
//     chip and the .mobile-day-card-no-wfh border class.
//
// Assertion shape: attribute-quoted `class="..."` strings for
// the rendered elements (vs. the bare class name which would
// also match the CSS rule definitions in the style block), and
// `>...<` for the badge label which only appears as element
// content (not as a `title=` or `aria-label=` attribute value).
func TestDashboard_NoOneWFH_BannerAndMatrixBadge(t *testing.T) {
	body := renderDashboardForNoOneWFH(t, true, true, false)

	// Banner: the informational surface above the status card.
	// The banner div concatenates three classes (notification +
	// no-wfh-banner + mb-4) into one attribute, so the substring
	// check uses the middle-class-token form (`notification
	// no-wfh-banner mb-4"`) — that string is unique to the
	// banner element because no CSS rule or other element uses
	// that exact three-class concatenation in the rendered HTML.
	assert.Contains(t, body, `notification no-wfh-banner mb-4"`,
		"the dashboard banner must carry the .no-wfh-banner class for the blue gradient styling")
	assert.Contains(t, body, `class="no-wfh-banner-title"`,
		"the dashboard banner must have a title block (mirrors the full-team banner structure)")
	// The unique title text appears only as element content of
	// the banner — the badge uses it as a `title=` attribute
	// value, so `>No one is WFH today<` (element-content form)
	// distinguishes the banner from the badge.
	assert.Contains(t, body, `>No one is WFH today<`,
		"the dashboard banner must show its title when TodayNoOneWFH is true")

	// Matrix column header: the today column carries the chip.
	assert.Contains(t, body, `class="no-wfh-badge"`,
		"the today column header must render the .no-wfh-badge chip when no one is WFH")
	assert.Contains(t, body, ">🏢 All in<",
		"the no-wfh badge must carry the 🏢 emoji and 'All in' label")

	// Column tint: the no-wfh-col gradient is what makes the
	// column visually distinct from the surrounding days without
	// the user having to read the chip text. The rendered class
	// list is `day-col today-col no-wfh-col` with the no-wfh
	// class as the last one, so the substring `no-wfh-col"`
	// (with closing quote) matches only the HTML element, not
	// the CSS rule definition.
	assert.Contains(t, body, `no-wfh-col"`,
		"the today column must carry the .no-wfh-col class for the soft blue tint")

	// Mobile variant: same chip, border-left accent. The mobile
	// day card's class list concatenates mobile-day-card +
	// mobile-day-card-today + mobile-day-card-no-wfh into a
	// single class attribute, so the substring is the trailing
	// portion of that attribute (with closing quote, never
	// followed by another class name). Same for the badge.
	assert.Contains(t, body, `mobile-day-card-no-wfh"`,
		"the mobile day card must carry the .mobile-day-card-no-wfh border class")
	assert.Contains(t, body, `class="mobile-no-wfh-badge"`,
		"the mobile day card must carry the .mobile-no-wfh-badge chip")
}

// TestDashboard_NoOneWFH_BannerSuppressedOnWeekend pins the
// TodayIsBusinessDay gate: even with TodayNoOneWFH=true, the
// banner must NOT render on a weekend. The matrix would have no
// WFH rows for a weekend day (no one's working), so the flag
// would be structurally false, but the explicit guard makes
// the contract readable — the helper layer enforces the data
// gate; the template enforces the day-type gate.
//
// Assertion shape: we check for the visible title text ("No one
// is WFH today") and the banner-style class names (quoted form
// `class="no-wfh-banner-title"` so they match the rendered HTML
// only, not the CSS rule definitions). The badge text "🏢 All in"
// is matrix-driven (see TestDashboard_NoOneWFH_BadgeAppearsExactlyOnce
// for the matrix-driven badge) so it stays present even when
// the banner-suppressed gate is fired.
func TestDashboard_NoOneWFH_BannerSuppressedOnWeekend(t *testing.T) {
	body := renderDashboardForNoOneWFH(t, true, false, false)

	assert.NotContains(t, body, `notification no-wfh-banner mb-4"`,
		"the dashboard banner must not render when TodayIsBusinessDay=false")
	assert.NotContains(t, body, `class="no-wfh-banner-title"`,
		"the dashboard banner title block must not render when TodayIsBusinessDay=false")
}

// TestDashboard_NoOneWFH_BannerSuppressedWhenFullTeamOnSite pins
// the mutual exclusion between the two banners: when both
// TodayNoOneWFH and TodayFullTeamOnSite hold, only the louder
// gold full-team banner renders. The blue no-WFH banner would
// be redundant — every full-team day is also a no-WFH day
// (FullTeamOnSite is a stricter case).
//
// This test mirrors the matrix state with the data-map state
// (FullTeamOnSite on the today column header), so the column
// header and mobile card also read off the matrix's
// FullTeamOnSite and render the gold chip instead of the blue
// one. Without that mirroring, the matrix column header would
// render the no-wfh badge while the data banner renders the
// full-team banner — visually inconsistent.
func TestDashboard_NoOneWFH_BannerSuppressedWhenFullTeamOnSite(t *testing.T) {
	body := renderDashboardForNoOneWFH(t, true, true, true)

	assert.Contains(t, body, "Full team on-site today!",
		"the full-team banner must render when TodayFullTeamOnSite=true")
	assert.NotContains(t, body, `notification no-wfh-banner mb-4"`,
		"the no-WFH banner must NOT render when the full-team banner wins")
	assert.NotContains(t, body, `class="no-wfh-banner-title"`,
		"the no-wfh banner title block must not render when the full-team banner wins")
	assert.NotContains(t, body, `class="no-wfh-badge"`,
		"the no-wfh column badge must NOT render when the full-team banner wins")
}

// TestDashboard_NoOneWFH_BannerSuppressedWhenFlagFalse pins the
// negative case: TodayNoOneWFH=false (someone IS working from
// home today), the banner stays hidden. The matrix column
// header and mobile card render off the matrix's WFHCount /
// AtWorkCount directly, not off the TodayNoOneWFH flag, so they
// can legitimately still show the no-wfh chip in this test
// scenario. The assertion is scoped to the banner-only
// artifacts (the title text and the banner-specific classes).
func TestDashboard_NoOneWFH_BannerSuppressedWhenFlagFalse(t *testing.T) {
	body := renderDashboardForNoOneWFH(t, false, true, false)

	assert.NotContains(t, body, `notification no-wfh-banner mb-4"`,
		"the dashboard banner must not render when TodayNoOneWFH=false")
	assert.NotContains(t, body, `class="no-wfh-banner-title"`,
		"the dashboard banner title block must not render when TodayNoOneWFH=false")
}

// TestDashboard_NoOneWFH_ColumnBadgeMutuallyExclusiveWithFullTeam
// pins the same mutual-exclusion rule for the matrix column
// header. On a day where both flags would otherwise fire, only
// the celebratory gold chip renders.
//
// Assertion shape: visible text for the badges, attribute-quoted
// class names for the rendered elements. The bare `full-team-col`
// / `no-wfh-col` substrings match the CSS rule definitions as
// well as the rendered `<th class="...">` tag, so the column-tint
// assertions check for the attribute-quoted form (`class="...col"`)
// which only appears in the rendered HTML, not the style block.
func TestDashboard_NoOneWFH_ColumnBadgeMutuallyExclusiveWithFullTeam(t *testing.T) {
	// Build a matrix where the today column has FullTeamOnSite=true
	// AND would also satisfy the no-one-WFH condition. The full-team
	// celebration wins in both the banner and the column badge.
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	matrix := scheduleMatrix{
		Days: []scheduleMatrixDay{
			{
				DateISO:        "2026-09-08",
				DateDisplay:    "Tue 8 Sep",
				IsToday:        true,
				AtWorkCount:    3,
				WFHCount:       0,
				LeaveCount:     0,
				FullTeamOnSite: true,
			},
		},
		Rows: []scheduleMatrixRow{
			{Member: database.TeamMember{Name: "Alice"}, Cells: []scheduleMatrixCell{
				{Status: "onsite", Label: "On-site"},
			}},
		},
	}

	data := map[string]any{
		"User":                map[string]any{"Email": "alice@example.com", "Name": "Alice"},
		"IsAdmin":             false,
		"Template":            "dashboard",
		"ScheduleMatrix":      matrix,
		"TodayIsWeekend":      false,
		"TodayIsHoliday":      false,
		"TodayIsBusinessDay":  true,
		"TodayFullTeamOnSite": true,
		"TodayNoOneWFH":       true,
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))
	body := w.Body.String()

	assert.Contains(t, body, `class="full-team-badge"`,
		"the full-team badge must render when both flags hold")
	assert.NotContains(t, body, `class="no-wfh-badge"`,
		"the no-wfh badge must NOT render when the full-team badge wins")
	assert.Contains(t, body, `class="day-col today-col full-team-col"`,
		"the column tint must be the full-team gold, not the no-wfh blue")
	assert.NotContains(t, body, ` no-wfh-col "`,
		"the column tint must not carry the no-wfh-col class when the full-team tint wins")
}

// TestDashboard_NoOneWFH_BadgeAppearsExactlyOnce pins that the
// no-wfh badge and chip text appear exactly once each on the
// page when the flag is true — guards against a future refactor
// accidentally emitting the chip twice (e.g. desktop + mobile
// rendering paths both firing on a viewport that supports both).
//
// The assertion uses `class="no-wfh-badge"` (with the `class=""`
// attribute syntax) so it matches the HTML element, not the CSS
// rule that defines the class. The badge wrapper class name
// appears in both the style block (`.no-wfh-badge {`) and the
// HTML body (`<div class="no-wfh-badge">`), so the bare class
// name substring would match twice — once in the CSS, once in
// the rendered element.
func TestDashboard_NoOneWFH_BadgeAppearsExactlyOnce(t *testing.T) {
	body := renderDashboardForNoOneWFH(t, true, true, false)

	badgeCount := strings.Count(body, `class="no-wfh-badge"`)
	assert.Equal(t, 1, badgeCount,
		"the no-wfh badge must appear exactly once on the page")

	labelCount := strings.Count(body, ">All in<")
	assert.Equal(t, 1, labelCount,
		"the 'All in' label must appear exactly once on the page")
}

// TestTodayNoOneWFH_NoWFHAndAtWork pins the helper's headline
// case: today has zero WFH rows and at least one person at
// work, so the helper returns true. The WFHCount=0 captures the
// "no-one-WFH" condition; the AtWorkCount>0 captures the
// "someone is at work" guard.
func TestTodayNoOneWFH_NoWFHAndAtWork(t *testing.T) {
	data := map[string]any{
		"ScheduleMatrix": scheduleMatrix{
			Days: []scheduleMatrixDay{
				{IsToday: true, AtWorkCount: 3, WFHCount: 0},
			},
		},
	}
	assert.True(t, todayNoOneWFH(data),
		"today with zero WFH and 3 at-work members must trip the no-one-WFH flag")
}

// TestTodayNoOneWFH_SuppressedWhenSomeoneIsWFH pins the negative
// axis on the WFHCount test: any WFH today suppresses the flag,
// even if there's still a large at-work count.
func TestTodayNoOneWFH_SuppressedWhenSomeoneIsWFH(t *testing.T) {
	data := map[string]any{
		"ScheduleMatrix": scheduleMatrix{
			Days: []scheduleMatrixDay{
				{IsToday: true, AtWorkCount: 5, WFHCount: 1},
			},
		},
	}
	assert.False(t, todayNoOneWFH(data),
		"today with even one WFH must NOT trip the no-one-WFH flag")
}

// TestTodayNoOneWFH_SuppressedWhenNobodyAtWork pins the
// AtWorkCount guard: a day with zero WFH and zero at-work is
// the edge case (everyone on leave, or an empty team, or a
// holiday the matrix doesn't carry). "No one is WFH" is
// trivially true but the implication "everyone at work is in
// the office" is meaningless when nobody is at work. The
// helper must return false so the template doesn't show a
// misleading "perfect for an in-person meeting" banner on a
// day with no in-person anything.
func TestTodayNoOneWFH_SuppressedWhenNobodyAtWork(t *testing.T) {
	data := map[string]any{
		"ScheduleMatrix": scheduleMatrix{
			Days: []scheduleMatrixDay{
				{IsToday: true, AtWorkCount: 0, WFHCount: 0},
			},
		},
	}
	assert.False(t, todayNoOneWFH(data),
		"a today with zero WFH AND zero at-work must NOT trip the no-one-WFH flag")
}

// TestTodayNoOneWFH_SuppressedOnNonTodayDay pins that the helper
// only looks at the today-flagged column. A tomorrow column
// with zero WFH must NOT trip today's flag.
func TestTodayNoOneWFH_SuppressedOnNonTodayDay(t *testing.T) {
	data := map[string]any{
		"ScheduleMatrix": scheduleMatrix{
			Days: []scheduleMatrixDay{
				{IsToday: false, AtWorkCount: 3, WFHCount: 0},
				{IsToday: true, AtWorkCount: 2, WFHCount: 1},
			},
		},
	}
	assert.False(t, todayNoOneWFH(data),
		"only the today-flagged column drives the helper; other days must not trip it")
}

// TestTodayNoOneWFH_MissingMatrix pins the defensive default:
// when the matrix didn't load, the helper returns false rather
// than guessing. Mirrors todayFullTeamOnSite's contract.
func TestTodayNoOneWFH_MissingMatrix(t *testing.T) {
	data := map[string]any{}
	assert.False(t, todayNoOneWFH(data),
		"missing matrix must produce a false (defensive default)")
}
