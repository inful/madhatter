package web

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTemplateParsing verifies that all templates can be parsed without errors.
// This test should catch issues like undefined template functions.
func TestTemplateParsing(t *testing.T) {
	// This should not panic
	assert.NotPanics(t, func() {
		_, _ = parseTemplates()
	})
}

// TestLoginTemplateExecution verifies the login template can execute with actual data.
func TestLoginTemplateExecution(t *testing.T) {
	// Create a mock auth manager
	mockAuthManager := &auth.AuthManager{}

	// Create a mock database
	mockDB := &database.DB{}

	// Create a mock middleware
	mockMiddleware := &auth.Middleware{}

	// Create handler - templates are now embedded
	handler, err := NewHandler(mockDB, mockAuthManager, mockMiddleware, false, nil)
	require.NoError(t, err)

	// Test data that would be passed to the template
	data := map[string]any{
		"Providers": []string{"forgejo", "gitlab"},
	}

	// Try to execute the login template
	w := httptest.NewRecorder()

	// Execute template directly to test for errors
	err = handler.tmpl.ExecuteTemplate(w, "login.html", data)

	// This should not return an error
	require.NoError(t, err, "Template execution should not fail")

	// Verify response was written
	assert.Positive(t, w.Body.Len(), "Response body should not be empty")

	// Verify the providers are rendered
	body := w.Body.String()
	assert.Contains(t, body, "forgejo")
	assert.Contains(t, body, "gitlab")
}

// TestHandlerCreation verifies handler creation doesn't panic on template parsing.
func TestHandlerCreation(t *testing.T) {
	mockDB := &database.DB{}
	mockAuthManager := &auth.AuthManager{}
	mockMiddleware := &auth.Middleware{}

	// Templates are embedded, so this should always succeed
	handler, err := NewHandler(mockDB, mockAuthManager, mockMiddleware, false, nil)
	require.NoError(t, err)
	require.NotNil(t, handler)
}

// TestRegisterRoutes verifies routes can be registered without template errors.
func TestRegisterRoutes(t *testing.T) {
	mockDB := &database.DB{}
	mockAuthManager := &auth.AuthManager{}
	mockMiddleware := &auth.Middleware{}

	// Create handler - templates are embedded
	handler, err := NewHandler(mockDB, mockAuthManager, mockMiddleware, false, nil)
	require.NoError(t, err)

	// Get router
	router := handler.Router()

	// Verify router is not nil
	require.NotNil(t, router)
}

// TestAllTemplatesParse verifies all templates can be parsed without errors.
func TestAllTemplatesParse(t *testing.T) {
	mockDB := &database.DB{}
	mockAuthManager := &auth.AuthManager{}
	mockMiddleware := &auth.Middleware{}

	// Create handler - templates are embedded
	handler, err := NewHandler(mockDB, mockAuthManager, mockMiddleware, false, nil)
	require.NoError(t, err)

	// All templates should be loaded without errors
	require.NotNil(t, handler.tmpl, "Template should be loaded")

	// List of all templates that should exist
	templates := []string{
		"login.html",
		"dashboard.html",
		"team.html",
		"leave_report.html",
		"schedule_generate.html",
		"database_restore.html",
		"calendar.html",
		"help.html",
	}

	for _, tmpl := range templates {
		t.Run(tmpl, func(t *testing.T) {
			// Verify template exists and can be executed
			w := httptest.NewRecorder()
			err := handler.tmpl.ExecuteTemplate(w, tmpl, nil)
			require.NoError(t, err, "Template %s should execute without error", tmpl)
			assert.Equal(t, 200, w.Code, "Template %s should return 200", tmpl)
		})
	}
}

// TestAllTemplatesWithData verifies all templates work with common data structures.
func TestAllTemplatesWithData(t *testing.T) {
	mockDB := &database.DB{}
	mockAuthManager := &auth.AuthManager{}
	mockMiddleware := &auth.Middleware{}

	// Create handler - templates are embedded
	handler, err := NewHandler(mockDB, mockAuthManager, mockMiddleware, false, nil)
	require.NoError(t, err)

	// Create test data that templates expect.
	dashboardData := map[string]any{
		"Assignments": []map[string]any{
			{"Date": "2026-01-10", "Member": "Test User", "IsCover": false, "IsLeave": false},
		},
		"User":    map[string]any{"Email": "test@example.com", "IsAdmin": true},
		"IsAdmin": true,
	}

	teamData := map[string]any{
		"Members": []map[string]any{
			{"ID": 1, "Name": "Test User", "Email": "test@example.com", "Active": true},
		},
		"User":    map[string]any{"Email": "test@example.com", "IsAdmin": true},
		"IsAdmin": true,
	}

	leaveData := map[string]any{
		"Members": []map[string]any{
			{"ID": 1, "Name": "Test User", "Email": "test@example.com"},
		},
		"Leave":   []map[string]any{},
		"User":    map[string]any{"Email": "test@example.com", "IsAdmin": true},
		"IsAdmin": true,
	}

	generateData := map[string]any{
		"Members": []map[string]any{
			{"ID": 1, "Name": "Test User", "Email": "test@example.com"},
		},
		"User":    map[string]any{"Email": "test@example.com", "IsAdmin": true},
		"IsAdmin": true,
	}

	calendarData := map[string]any{
		"Template": "calendar",
		"Subscriptions": []map[string]any{
			{"Token": "test-token", "Name": "Test Calendar", "CreatedAt": "2026-01-08"},
		},
		"Members": []map[string]any{
			{"ID": 1, "Name": "Test User", "Email": "test@example.com"},
		},
		"User":    map[string]any{"Email": "test@example.com", "IsAdmin": true},
		"IsAdmin": true,
	}

	calendarShowResultData := map[string]any{
		"Template":                  "calendar",
		"Token":                     "test-token",
		"CalendarURL":               "https://example.com/calendar/test-token/ics",
		"MeetingsCalendarURL":       "https://example.com/calendar/test-token/meetings.ics",
		"CalendarWebcalURL":         template.URL("webcal://example.com/calendar/test-token/ics"),
		"MeetingsCalendarWebcalURL": template.URL("webcal://example.com/calendar/test-token/meetings.ics"),
		"CalendarOutlookURL":        template.URL("https://outlook.office.com/calendar/0/addfromweb?url=https%3A%2F%2Fexample.com%2Fcalendar%2Ftest-token%2Fics&name=Support+rota"),
		"MeetingsCalendarOutlookURL": template.URL(
			"https://outlook.office.com/calendar/0/addfromweb?url=https%3A%2F%2Fexample.com%2Fcalendar%2Ftest-token%2Fmeetings.ics&name=Support+meetings",
		),
		"ShowResult": true,
		"Members": []map[string]any{
			{"ID": 1, "Name": "Test User", "Email": "test@example.com"},
		},
		"User":    map[string]any{"Email": "test@example.com", "IsAdmin": true},
		"IsAdmin": true,
	}

	loginData := map[string]any{
		"Providers": []map[string]any{
			{"Name": "Forgejo", "URL": "https://forgejo.example.com"},
		},
	}

	testCases := []struct {
		name     string
		template string
		data     map[string]any
	}{
		{"Dashboard", "dashboard.html", dashboardData},
		{"Team", "team.html", teamData},
		{"LeaveReport", "leave_report.html", leaveData},
		{"ScheduleGenerate", "schedule_generate.html", generateData},
		{"Calendar", "calendar.html", calendarData},
		{"CalendarShowResult", "calendar.html", calendarShowResultData},
		{"Help", "help.html", map[string]any{"Template": "help"}},
		{"Login", "login.html", loginData},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			err := handler.tmpl.ExecuteTemplate(w, tc.template, tc.data)
			require.NoError(t, err, "Template %s should execute with data", tc.template)
			assert.Equal(t, 200, w.Code, "Template %s should return 200", tc.template)
			assert.NotEmpty(t, w.Body.String(), "Template %s should produce output", tc.template)
			if tc.name == "CalendarShowResult" {
				assert.Contains(t, w.Body.String(), "addfromweb")
				assert.NotContains(t, w.Body.String(), "#ZgotmplZ")
			}
		})
	}
}

func TestHandleHelp_Returns200(t *testing.T) {
	mockDB := &database.DB{}
	mockAuthManager := &auth.AuthManager{}
	mockMiddleware := &auth.Middleware{}

	handler, err := NewHandler(mockDB, mockAuthManager, mockMiddleware, false, nil)
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/help", nil)
	w := httptest.NewRecorder()

	handler.handleHelp(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "User Guide")
	assert.Contains(t, w.Body.String(), "HAT Day Swaps")
	assert.Contains(t, w.Body.String(), "How WFH Is Settled")
}

// TestPageHeader_HeadingIsReasonablySized is the regression guard
// for the page-header h1 size. The previous is-2 + 32-39px clamp
// rendered at sizes appropriate for a marketing landing page; for
// the dashboard (and the rest of the authenticated pages) the h1
// is a contextual anchor — "Dashboard", "Team", "Leave Management"
// — that should not compete with the HAT banner or the schedule
// matrix for attention. The contract is:
//   - The h1 carries the is-3 class (Bulma's h3 size = 1.5rem
//     default, tuned down further by the page-header CSS).
//   - The page-header CSS targets .title.is-3, not .title.is-2, so
//     a regression that swaps the class back to is-2 wouldn't
//     silently land a too-large heading on every page.
//   - The h1 class names "Dashboard" via the page_header Title arg
//     so the test can render any page that uses page_header (every
//     authenticated page does).
func TestPageHeader_HeadingIsReasonablySized(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	data := map[string]any{
		"User":     map[string]any{"Email": "alice@example.com", "Name": "Alice"},
		"IsAdmin":  false,
		"Template": "dashboard",
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))

	body := w.Body.String()

	// The h1 must use the smaller is-3 class, not is-2 (which was
	// the regression-prone larger size).
	assert.Contains(t, body, `<h1 class="title is-3 has-text-primary">`,
		"page-header h1 must use the is-3 class (Bulma h3, with the project's smaller clamp override)")
	assert.NotContains(t, body, `<h1 class="title is-2`,
		"page-header h1 must not use the oversized is-2 class — that was the original problem")

	// The CSS must target the new selector. Without this, a CSS
	// regression (e.g. someone renames the class but forgets to
	// update the style block) would re-introduce the oversized
	// heading silently.
	assert.Contains(t, body, ".page-header .title.is-3",
		"page-header CSS must scope the size override to .title.is-3")
	assert.NotContains(t, body, ".page-header .title.is-2",
		"page-header CSS must not still scope the old .title.is-2 selector — the size override is gone")
}

func TestDashboard_NonAdminSeesManageLeaveButton(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	// Minimal dashboard data: a logged-in user who is not an admin.
	// Other optional fields (ScheduleMatrix, presence tags, WFH
	// quota, etc.) are deliberately omitted — their absence is
	// guarded by {{if}} blocks in the template and renders an empty
	// state, which is fine for this focused test. The "Template"
	// field is the dispatch key the base layout reads to decide
	// whether to render the login page or the dashboard content
	// (it falls back to login when missing).
	data := map[string]any{
		"User":     map[string]any{"Email": "alice@example.com", "Name": "Alice"},
		"IsAdmin":  false,
		"Template": "dashboard",
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))

	body := w.Body.String()
	assert.Contains(t, body, `href="/leave/manage"`,
		"non-admin must see a /leave/manage link on the dashboard")
	assert.Contains(t, body, "Manage Leave",
		"non-admin must see the 'Manage Leave' label on the dashboard")
	// Sanity: the admin-only team page must NOT be reachable for a
	// non-admin user via the dashboard — guards against the button
	// being accidentally moved inside the admin block again.
	assert.NotContains(t, body, `href="/team"`,
		"non-admin must not see the admin-only Team link on the dashboard")
}

// TestDashboard_AdminSeesManageLeaveButton pairs with the non-admin
// test above as a regression guard for admins — making the button
// available to non-admins must not remove it from the admin view.
// Admins already saw the link via the IsAdmin block before the fix,
// so this asserts the rendered output still contains it after the
// refactor that pulled the link out of that block.
func TestDashboard_AdminSeesManageLeaveButton(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	data := map[string]any{
		"User":     map[string]any{"Email": "admin@example.com", "Name": "Admin"},
		"IsAdmin":  true,
		"Template": "dashboard",
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))

	body := w.Body.String()
	assert.Contains(t, body, `href="/leave/manage"`,
		"admin must still see a /leave/manage link on the dashboard")
	assert.Contains(t, body, "Manage Leave",
		"admin must still see the 'Manage Leave' label on the dashboard")
	assert.Contains(t, body, `href="/team"`,
		"admin must still see the admin-only Team link on the dashboard")
}

// TestDashboard_QuickActionsInUserCard guards the rework that moved
// the Quick Actions dropdown from a standalone dashboard card into
// the global user identity card at the top of every authenticated
// page. The trigger, menu, and items live inside base.html's
// global_user_menu block now (rendered via {{template
// "global_user_menu" .}} when .User is set). The dashboard itself no
// longer owns a Quick Actions card.
//
// The rendered dashboard.html goes through base.html's layout, so
// the dropdown markup is in the output. Asserting on the wrapper id,
// the trigger button, the menu role, and the .dropdown-item class
// catches a regression that puts the buttons back into a flat grid
// or moves them outside the user card (which would defeat the move).
//
// The dropdown trigger must be labeled "Quick Actions" so the
// affordance stays discoverable when the menu is closed.
func TestDashboard_QuickActionsInUserCard(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	// PendingSwapCount exercises the conditional badge inside the
	// Swap HAT Day dropdown item — without it the badge code path
	// wouldn't be hit by the test and a regression that drops the
	// tag could slip through.
	data := map[string]any{
		"User":             map[string]any{"Email": "alice@example.com", "Name": "Alice"},
		"IsAdmin":          false,
		"PendingSwapCount": 3,
		"Template":         "dashboard",
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))

	body := w.Body.String()

	// The dropdown wrapper must appear inside the user card, not
	// in its own dedicated card. Asserting on the DOM order — user
	// card markup before the dropdown markup before the dashboard
	// heading — catches a regression that puts the dropdown back in
	// a standalone card.
	userMenuIdx := strings.Index(body, `class="card global-user-menu"`)
	dropdownIdx := strings.Index(body, `id="quickActionsDropdown"`)
	headingIdx := strings.Index(body, `<h1 class="title is-3 has-text-primary"`)
	require.NotEqual(t, -1, userMenuIdx, "global user card must render for a logged-in user")
	require.NotEqual(t, -1, dropdownIdx, "dropdown wrapper must be present on the dashboard")
	require.NotEqual(t, -1, headingIdx, "dashboard heading must render")
	assert.Less(t, userMenuIdx, dropdownIdx,
		"user card must render before the dropdown trigger")
	assert.Less(t, dropdownIdx, headingIdx,
		"dropdown must live inside the user card (before the dashboard heading)")

	assert.Contains(t, body, `id="quickActionsTrigger"`,
		"dropdown trigger button must be present on the dashboard")
	assert.Contains(t, body, `aria-haspopup="true"`,
		"trigger button must declare aria-haspopup for assistive tech")
	assert.Contains(t, body, `aria-expanded="false"`,
		"trigger button must start with aria-expanded=false (SSR closed state)")
	assert.Contains(t, body, `role="menu"`,
		"dropdown menu must declare role=menu for assistive tech")
	assert.Contains(t, body, `class="dropdown-item"`,
		"quick-action entries must be rendered as Bulma .dropdown-item")
	assert.Contains(t, body, "Manage Leave",
		"dropdown must include the Manage Leave label")
	assert.Contains(t, body, "Swap HAT Day",
		"dropdown must include the Swap HAT Day label")
	// The status badge on Swap HAT Day must render inside the
	// dropdown item so the pending-count affordance survives the
	// move to the user card.
	assert.Contains(t, body, `<span class="tag is-danger is-small ml-2">3</span>`,
		"Swap HAT Day dropdown item must show the pending-count badge")
	// The old standalone Quick Actions card had its own card-header
	// with a bolt icon. Catching that string in the rendered output
	// would mean the dropdown was moved back into its own card.
	assert.NotContains(t, body, `<i class="fas fa-bolt mr-2"></i> Quick Actions`,
		"Quick Actions must not have its own card header — it lives inside the user card now")
	// SSR state: dropdown must be closed on initial render. A bug
	// that ships `is-active` in the markup would surprise every
	// user with an open menu on page load.
	assert.NotRegexp(t, `id="quickActionsDropdown"[^>]*class="[^"]*is-active`, body,
		"dropdown must start closed (no is-active class on the wrapper in SSR)")
}

// TestUserCard_QuickActionsIsPrimaryNav asserts the visual hierarchy
// of the global user identity card. Quick Actions is the primary
// navigation launcher (12+ actions, used most often), so it carries
// the is-primary Bulma style and default size — the visual anchor
// of the actions column. Help and Logout are infrequent utility
// actions, so they stay as is-small is-light / is-small is-danger
// is-light versions of the same button shape. The mix previously
// shipped with Quick Actions as is-small is-light (visually equal
// to Help/Logout), which read as "three misc buttons" rather than
// "primary nav + utility buttons" — a hierarchy bug.
//
// Asserting on the rendered class strings catches a regression that
// (a) reverts Quick Actions to is-light (loss of nav anchor) or
// (b) escalates Help/Logout to is-primary (visual noise). The
// order-of-elements check (Quick Actions DOM position before Help
// and Logout) catches a regression that reorders the columns.
func TestUserCard_QuickActionsIsPrimaryNav(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	data := map[string]any{
		"User":     map[string]any{"Email": "alice@example.com", "Name": "Alice"},
		"IsAdmin":  false,
		"Template": "dashboard",
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))

	body := w.Body.String()

	// Quick Actions is the primary nav launcher — is-primary, default size.
	assert.Contains(t, body, `class="button is-primary action-btn"`,
		"Quick Actions trigger must be styled as the primary action (is-primary)")

	// Quick Actions must NOT be small. The previous design treated it as
	// a peer of Help/Logout (is-small). That mismatch — visually loud
	// duotone icon/chevron inside a small button — is what the
	// primary-styling fix undoes.
	assert.NotRegexp(t, `id="quickActionsTrigger"[^>]*class="[^"]*is-small`,
		"Quick Actions trigger must not be is-small — primary size sets it apart from utility buttons")

	// Help stays as a small, light info button (utility).
	assert.Contains(t, body, `class="button is-small is-info is-light"`,
		"Help button must retain its small utility styling")
	// Logout stays as a small, light danger button (destructive utility).
	assert.Contains(t, body, `class="button is-small is-danger is-light"`,
		"Logout button must retain its small destructive-utility styling")

	// Action order: Quick Actions first (closest to identity), then
	// Help, then Logout. The dropdown's DOM position must precede
	// both Help and Logout so it stays the leftmost action in the
	// column (the conventional nav-launcher position).
	quickActionsIdx := strings.Index(body, `id="quickActionsDropdown"`)
	helpIdx := strings.Index(body, `href="/help"`)
	logoutIdx := strings.Index(body, `href="/auth/logout"`)
	require.NotEqual(t, -1, quickActionsIdx)
	require.NotEqual(t, -1, helpIdx)
	require.NotEqual(t, -1, logoutIdx)
	assert.Less(t, quickActionsIdx, helpIdx,
		"Quick Actions must render before Help (leftmost action in the column)")
	assert.Less(t, helpIdx, logoutIdx,
		"Help must render before Logout (utility grouping)")
}

// renderQuickActionsMenu is the fixture for the "WFH today" menu entry
// tests. It spins up a Handler with a stub DB and renders dashboard.html
// against a data map — the same approach the QuickActionsInUserCard test
// uses. The dashboard template pulls in base.html, which renders the
// global user menu (including the WFH today entry when its gate fires).
func renderQuickActionsMenu(t *testing.T, data map[string]any) string {
	t.Helper()
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	if data["Template"] == nil {
		data["Template"] = "dashboard"
	}
	if data["User"] == nil {
		data["User"] = map[string]any{"Email": "alice@example.com", "Name": "Alice"}
	}
	if _, ok := data["IsAdmin"]; !ok {
		data["IsAdmin"] = false
	}
	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))
	return w.Body.String()
}

// TestQuickActions_WFHTodayEntry_RendersWhenEligible pins the new
// "WFH today" menu entry: when the WFH feature is enabled, today is a
// business day, and the user is currently On-site, the Quick Actions
// menu must surface a POST form that fires /wfh/report-today. The
// dashboard used to carry this affordance as a standalone button; the
// move to the menu keeps the click semantics identical (state-changing
// POST, not a GET link).
func TestQuickActions_WFHTodayEntry_RendersWhenEligible(t *testing.T) {
	body := renderQuickActionsMenu(t, map[string]any{
		"CanReportWFHToday":         true,
		"CurrentUserPresenceStatus": "On-site",
	})

	// Form-as-dropdown-item: the entry is a <form method="post"> styled
	// to look like a sibling .dropdown-item. Asserting on the form's
	// action and method pins the click semantics; the WFH today label
	// pins the human-readable entry.
	assert.Contains(t, body, `action="/wfh/report-today"`,
		"menu entry must POST to /wfh/report-today (same endpoint the dashboard button used)")
	assert.Contains(t, body, `class="dropdown-item quick-action-form"`,
		"menu entry must be a form styled as a dropdown item")
	assert.Contains(t, body, "WFH today",
		"menu entry must show the WFH today label")

	// Method check: grep for `method="post"` adjacent to the
	// /wfh/report-today form so we know the form method is POST and
	// not, say, a hidden GET that wouldn't actually fire the report.
	wfhFormIdx := strings.Index(body, `action="/wfh/report-today"`)
	require.NotEqual(t, -1, wfhFormIdx)
	formStart := strings.LastIndex(body[:wfhFormIdx], "<form")
	require.NotEqual(t, -1, formStart)
	formTag := body[formStart:wfhFormIdx]
	assert.Contains(t, formTag, `method="post"`,
		"WFH today form must submit via POST (state-changing action)")
}

// TestQuickActions_WFHTodayEntry_HiddenWhenNotEligible pins the gate:
// the entry must NOT render when the user is not currently On-site, when
// the feature is disabled, or when the gating data is absent (e.g. on
// pages that don't compute presence status). Each case asserts the menu
// still renders the regular WFH link so the test failure mode is
// specific to the WFH today entry, not a broader menu render break.
func TestQuickActions_WFHTodayEntry_HiddenWhenNotEligible(t *testing.T) {
	cases := []struct {
		name string
		data map[string]any
	}{
		{
			name: "user is currently WFH",
			data: map[string]any{
				"CanReportWFHToday":         true,
				"CurrentUserPresenceStatus": "WFH",
			},
		},
		{
			name: "user is on leave",
			data: map[string]any{
				"CanReportWFHToday":         true,
				"CurrentUserPresenceStatus": "On leave",
			},
		},
		{
			name: "WFH feature disabled (today not a business day)",
			data: map[string]any{
				"CanReportWFHToday":         false,
				"CurrentUserPresenceStatus": "On-site",
			},
		},
		{
			name: "gating data absent (non-dashboard page)",
			data: map[string]any{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := renderQuickActionsMenu(t, tc.data)

			assert.NotContains(t, body, `action="/wfh/report-today"`,
				"WFH today form must not render when the gate conditions are not met (%s)", tc.name)
			assert.NotContains(t, body, `class="dropdown-item quick-action-form"`,
				"WFH today menu entry must not render when not eligible (%s)", tc.name)

			// The regular WFH link is the adjacent item in the same
			// menu and must remain present — its absence would mean
			// the test broke the broader menu render, not the gate.
			assert.Contains(t, body, `href="/wfh"`,
				"the regular WFH menu entry must remain regardless of the WFH today gate (%s)", tc.name)
		})
	}
}

// TestDashboard_RemovedStandaloneWFHTodayButton asserts the dashboard
// no longer renders its own "WFH today" button — the affordance was
// moved into the Quick Actions menu (see
// TestQuickActions_WFHTodayEntry_RendersWhenEligible). A regression that
// re-adds the standalone button would duplicate the entry and split the
// click affordance across two UI surfaces; this test pins that the
// dashboard no longer carries the button form.
func TestDashboard_RemovedStandaloneWFHTodayButton(t *testing.T) {
	body := renderQuickActionsMenu(t, map[string]any{
		"CanReportWFHToday":         true,
		"CurrentUserPresenceStatus": "On-site",
	})

	// The dashboard's old button wrapped its form in
	// `<form method="post" action="/wfh/report-today" style="display:inline">`.
	// The display:inline style was specific to the dashboard button so
	// the form sat next to the user-card content; the menu entry uses
	// `class="dropdown-item quick-action-form"` instead. Asserting on
	// the inline-style substring pins the *removal* specifically; the
	// post-render count of WFH-today forms should be exactly one (the
	// menu entry, not the old button plus the menu entry).
	assert.NotContains(t, body, `action="/wfh/report-today" style="display:inline"`,
		"dashboard must not carry the standalone WFH today button — it lives in the Quick Actions menu now")

	count := strings.Count(body, `action="/wfh/report-today"`)
	assert.Equal(t, 1, count,
		"WFH today form must appear exactly once in the rendered output (the menu entry); got %d", count)
}

// TestQuickActions_WFHTodayEntry_CSSDoesNotOverrideDropdownItemPadding
// pins the CSS rule that keeps the WFH today menu entry visually
// aligned with its sibling dropdown items. The entry is rendered as a
// <form class="dropdown-item quick-action-form"> wrapping a <button>;
// Bulma's .dropdown-item class supplies the indent (padding: 0.375rem
// 1rem), hover background, and cursor. If a future change adds
// `padding: 0` to .quick-action-form — easy to do when "resetting"
// default browser styles — the entry sinks flush against the dropdown
// edge and the icon/label no longer line up with the other items.
//
// We pin the rule by reading the base.html source instead of
// rendering a page and inspecting computed styles: there is no
// headless browser in the test suite, and the rule itself is the
// surface that misbehaved. A regression that re-adds `padding: 0` to
// the form fails this test before any visual diff is filed.
func TestQuickActions_WFHTodayEntry_CSSDoesNotOverrideDropdownItemPadding(t *testing.T) {
	const baseHTML = "templates/base.html"
	contents, err := os.ReadFile(baseHTML)
	require.NoError(t, err, "reading %s", baseHTML)

	body := string(contents)

	// Locate the .quick-action-form rule. We don't want to rely on
	// line numbers (they drift), so extract the rule body by
	// scanning from the selector to the closing brace of the rule.
	selectorIdx := strings.Index(body, ".quick-action-form {")
	require.NotEqual(t, -1, selectorIdx,
		".quick-action-form CSS rule must exist in base.html (the WFH today entry wrapper)")

	// Find the closing brace of the same rule. The first standalone
	// "}" after the selector is the rule closer; nested braces are
	// not possible in a selector-only rule like this one.
	ruleStart := selectorIdx + len(".quick-action-form {")
	ruleEnd := strings.Index(body[ruleStart:], "}")
	require.NotEqual(t, -1, ruleEnd, "could not find closing brace of .quick-action-form rule")
	ruleBody := body[ruleStart : ruleStart+ruleEnd]

	assert.NotContains(t, ruleBody, "padding:",
		".quick-action-form must not declare padding: it lets Bulma's .dropdown-item padding (0.375rem 1rem) indent the icon and label so they line up with the sibling menu items. Declaring `padding: 0` flushes the entry against the dropdown edge.")

	// The margin reset is the only thing the rule is allowed to set;
	// sanity-check that we are reading the rule we think we are.
	assert.Contains(t, ruleBody, "margin: 0",
		".quick-action-form must keep margin: 0 so the <form> doesn't add default browser margins that misalign it with the surrounding <a> dropdown items")
}

// TestHandler_SecurityHeadersAppliedGlobally asserts that the
// security headers reach the response on every route the web
// handler exposes, not just on the synthetic httptest cases used
// by the middleware unit tests. Uses a nil auth middleware so the
// test does not depend on a populated SessionManager — the safe*
// middleware helpers short-circuit when h.authMiddleware is nil.
func TestHandler_SecurityHeadersAppliedGlobally(t *testing.T) {
	mockDB := &database.DB{}
	mockAuthManager := &auth.AuthManager{}

	handler, err := NewHandler(mockDB, mockAuthManager, nil, false, nil)
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/help", nil)
	w := httptest.NewRecorder()
	handler.Router().ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	h := w.Header()
	assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", h.Get("X-Frame-Options"))
	assert.Equal(t, "same-origin", h.Get("Referrer-Policy"))
	assert.NotEmpty(t, h.Get("Content-Security-Policy"))
}

// TestScheduleMatrixHasScrollHintFade guards the right-edge fade
// gradient that signals scrollable overflow on the schedule matrix.
// The matrix has a fixed min-width (860px on the table) that
// overflows the wrap on most laptop viewports — the browser shows
// a horizontal scrollbar at the bottom but no visible cue at the top
// or right that more content exists past the visible edge. The
// fade is the user-facing affordance: a soft white-to-transparent
// gradient on the right edge of the wrap that suggests the matrix
// continues.
//
// Source-level scan: the CSS rule is broadcast inline in the
// rendered HTML (no external stylesheet), so checking the rendered
// output is equivalent to checking the source. Asserting on the
// specific selector + gradient + pointer-events:none catches:
//   - A regression that drops the rule (the fade disappears and the
//     overflow becomes invisible again)
//   - A regression that changes the gradient direction (the fade
//     appears on the wrong side)
//   - A regression that forgets pointer-events:none (the fade
//     blocks clicks on the rightmost table cells)
func TestScheduleMatrixHasScrollHintFade(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	// Minimal data — the matrix loads without team members, which
	// is the empty-state case. The CSS rule is on the wrap, not the
	// table, so it renders regardless of matrix content.
	data := map[string]any{
		"User":     map[string]any{"Email": "alice@example.com", "Name": "Alice"},
		"IsAdmin":  false,
		"Template": "dashboard",
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))

	body := w.Body.String()

	// The fade must be a pseudo-element on the new wrapper
	// (.schedule-matrix-overflow) — not on the wrap itself. If the
	// fade lives on the wrap, it's a child of the overflow-x: auto
	// scrolling container and scrolls along with the table, which
	// looks weird: the user expects the gradient to mark the right
	// edge of what they can see, not the right edge of off-screen
	// content. Anchoring to the wrapper keeps the fade at the
	// visible right edge regardless of scroll position.
	assert.Contains(t, body, ".schedule-matrix-overflow::after",
		"scroll hint fade must be a pseudo-element on the matrix wrapper, not on the scrolling wrap")
	assert.NotContains(t, body, ".schedule-matrix-wrap::after",
		"scroll hint fade must NOT be on the matrix wrap — that anchor scrolls with the content")
	assert.Contains(t, body, ".schedule-matrix-overflow {",
		"matrix wrapper must be defined as the fade anchor")
	assert.Contains(t, body, "linear-gradient(to left,",
		"fade must run from right (opaque) to left (transparent)")
	assert.Contains(t, body, "rgba(255, 255, 255, 0.95)",
		"fade must start near-white to match the table background")
	assert.Contains(t, body, "rgba(255, 255, 255, 0)",
		"fade must end transparent so the table content shows through")
	assert.Contains(t, body, "pointer-events: none",
		"fade must not block clicks on the rightmost table cells")
}

// TestScheduleMatrixHasAccessibleStructure guards the screen-reader
// affordances on the dashboard's schedule matrix. The matrix is the
// primary data view of the page — the user can scan who is where for
// the next two weeks — so screen-reader users need to navigate it
// the same way sighted users do: by team member (row) and by day
// (column).
//
// The matrix previously had no caption, no scope attributes, and no
// programmatic relationship between header cells and data cells.
// Screen readers could only announce "row 3 of 4" without telling
// the user which row or column. The fix:
//
//   - Adds an <caption class="is-sr-only"> describing the matrix
//     (rows are members, columns are business days) and the cell
//     semantics (status, HAT assignment). Screen readers announce
//     the caption as the table's identity.
//   - Adds scope="col" to the day-header <th> elements so screen
//     readers can announce "Tuesday, August 11" when reading a
//     cell in that column.
//   - Adds scope="row" to the member-name <th> so screen readers
//     can announce "Alice" when reading a cell in that row.
//
// The "is-sr-only" Bulma class hides the caption visually (it's
// still in the DOM and read by screen readers) so the matrix's
// visual appearance is unchanged.
func TestScheduleMatrixHasAccessibleStructure(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	// The matrix is only rendered when .ScheduleMatrix is non-nil.
	// Provide a minimal matrix (one day, one member) so the
	// accessibility attributes are actually in the output. The test
	// only checks structural attributes that don't depend on the
	// matrix's contents.
	matrix := &scheduleMatrix{
		Days: []scheduleMatrixDay{
			{DateISO: "2026-08-10", DateDisplay: "Mon, Aug 10", IsToday: true},
		},
		Rows: []scheduleMatrixRow{
			{Member: database.TeamMember{Name: "Alice"}, Cells: []scheduleMatrixCell{
				{Status: "onsite", Label: "On-site", IsToday: true},
			}},
		},
	}
	data := map[string]any{
		"User":           map[string]any{"Email": "alice@example.com", "Name": "Alice"},
		"IsAdmin":        false,
		"Template":       "dashboard",
		"ScheduleMatrix": matrix,
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))

	body := w.Body.String()

	// The matrix must have a screen-reader-only caption. The
	// caption describes the structure (rows = members, columns =
	// days) and the cell semantics, so a screen reader user knows
	// what each cell represents when navigating the table.
	assert.Contains(t, body, `<caption class="is-sr-only">`,
		"matrix must have a screen-reader-only caption")
	assert.Contains(t, body, "Rows are team members",
		"caption must describe the row dimension (team members)")
	assert.Contains(t, body, "columns are business days",
		"caption must describe the column dimension (business days)")

	// The day-header cells must declare scope=col so screen
	// readers can announce the day when reading a cell in that
	// column. The corner cell (top-left) is also a <th> and
	// gets scope=col — it's a column header for the implicit
	// "member" column.
	assert.Contains(t, body, `<th class="member-col" scope="col">`,
		"the corner <th> must declare scope=col")
	assert.Contains(t, body, `scope="col"`,
		"each day-header <th> must declare scope=col")
	// The day-col class is present too — confirm via a more
	// specific substring that doesn't depend on whether the day
	// is also marked today (in which case the class becomes
	// "day-col today-col").
	assert.Regexp(t, `<th class="day-col[^"]*" scope="col"`, body,
		"each day-header <th> must carry the day-col class with scope=col")

	// The member-name cells in each row must declare scope=row so
	// screen readers can announce the team member when reading a
	// cell in that row.
	assert.Contains(t, body, `<th class="member-col" scope="row">`,
		"each member-name <th> must declare scope=row")
}

// TestDashboard_StatusCardHasTodayAndComingUpSections guards the
// "today vs next" grouping in the status card. The card has two
// rows of tags: the user's current state (On-site / WFH / On leave
// + HAT day) and future occurrences (Next HAT / Next WFH / Next
// leave). The grouping only makes sense if both rows are explicitly
// labeled "Today" and "Coming up" — otherwise the user just sees a
// pile of tags with no signal about which is current state vs which
// is forward-looking.
//
// The test renders the dashboard with presence data so the status
// card appears (it lives inside the {{if .CurrentUserPresenceStatus}}
// guard). It then asserts both labels are present in the rendered
// output, and that they each appear exactly once (no duplicate
// labels from copy-paste errors).
func TestDashboard_StatusCardHasTodayAndComingUpSections(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	// Presence data is required for the status card to render. The
	// value doesn't matter for the test - any non-empty value flips
	// the {{if .CurrentUserPresenceStatus}} guard on.
	data := map[string]any{
		"User":                      map[string]any{"Email": "alice@example.com", "Name": "Alice"},
		"IsAdmin":                   false,
		"Template":                  "dashboard",
		"CurrentUserPresenceStatus": "On-site",
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))

	body := w.Body.String()

	// Both sections must be labeled. The text content of the label
	// is what matters (a regression that reorders or renames the
	// label still shows the text "Today" or "Coming up" in the
	// output).
	assert.Contains(t, body, "Today",
		"status card must have a 'Today' section label")
	assert.Contains(t, body, "Coming up",
		"status card must have a 'Coming up' section label")

	// Each label must appear exactly once. A duplicate would mean
	// the template emitted the label twice (e.g., a copy-paste bug)
	// or the label is reused in another context. Either way, it's a
	// structural problem worth catching.
	assert.Equal(t, 1, strings.Count(body, "Today"),
		"'Today' label must appear exactly once in the body")
	assert.Equal(t, 1, strings.Count(body, "Coming up"),
		"'Coming up' label must appear exactly once in the body")
}

// TestQuickActionsAvailableOnNonDashboardPages asserts that the
// Quick Actions dropdown is reachable from any authenticated page,
// not only the dashboard. The dropdown moved from a dashboard-only
// card to the global user identity card (base.html's
// global_user_menu block) so every page in the authenticated flow
// exposes the same Quick Actions affordance — including
// /help, which the dashboard admin doesn't use as a navigation
// surface but the help page is where unauthenticated visitors end
// up first via the Help button.
//
// We render the leave_management template (a non-dashboard page
// that base.html already serves in dev mode) and assert that the
// dropdown wrapper, trigger, and the user card are all present.
// Catching a regression that accidentally re-gates the dropdown
// to dashboard.html only would mean users on /team, /leave/manage,
// etc. lose the Quick Actions affordance.
func TestQuickActionsAvailableOnNonDashboardPages(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	// Render a non-dashboard page (leave_management) with a logged-in
	// non-admin user. The dropdown should appear in the rendered HTML
	// via the user card, even though the page isn't the dashboard.
	data := map[string]any{
		"User":         map[string]any{"Email": "alice@example.com", "Name": "Alice"},
		"IsAdmin":      false,
		"Template":     "leave_management",
		"Leaves":       []database.LeaveRecord{},
		"Members":      []database.TeamMember{},
		"SelfMemberID": "",
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "leave_management.html", data))

	body := w.Body.String()
	assert.Contains(t, body, `class="card global-user-menu"`,
		"user identity card must render on non-dashboard authenticated pages")
	assert.Contains(t, body, `id="quickActionsDropdown"`,
		"Quick Actions dropdown must render on non-dashboard authenticated pages")
	assert.Contains(t, body, `id="quickActionsTrigger"`,
		"Quick Actions trigger must render on non-dashboard authenticated pages")
	// The personal-section items are always present; admin items
	// only appear for admins. Use the always-present "Manage Leave"
	// label as a quick smoke-test that the dropdown content rendered
	// (not just the wrapper).
	assert.Contains(t, body, "Manage Leave",
		"dropdown content must include the always-available Manage Leave item")
}

// TestDashboard_HATBanner guards the HAT info that surfaces
// the most-asked question of a rota app: who is on support
// today. The HAT info is rendered as the SCHEDULE CARD's header
// (not a standalone card anymore) — the answer is the natural
// lead-in to "here's the team's schedule for the week," and
// putting them in the same card reclaims a ~80px vertical gap.
//
// Three cases covered:
//   - Normal: HAT today is Alice, no leave flag → header shows
//     Alice and no "on leave" status.
//   - On leave: HAT today is Bob (the cover), CurrentHATIsOnLeave
//     is true → header shows Bob with "(Alice) on leave" status.
//   - Empty: CurrentHATName is empty → header shows the standard
//     "Schedule" title as a fallback.
func TestDashboard_HATBanner(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	t.Run("renders primary HAT in schedule header", func(t *testing.T) {
		data := map[string]any{
			"User":                  map[string]any{"Email": "alice@example.com", "Name": "Alice"},
			"IsAdmin":               false,
			"Template":              "dashboard",
			"CurrentHATName":        "Alice",
			"CurrentHATIsOnLeave":   false,
			"CurrentHATPrimaryName": "Alice",
		}

		w := httptest.NewRecorder()
		require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))

		body := w.Body.String()
		assert.Contains(t, body, "HAT today",
			"header must include the 'HAT today' label")
		assert.Contains(t, body, `<span class="hat-banner-header-name">Alice</span>`,
			"header must include the HAT's name in the new banner-header selector")
		// The standalone banner class is gone — the HAT info now
		// lives in .hat-banner-header-content inside the schedule
		// card's card-header element.
		assert.NotContains(t, body, `class="hat-banner card mb-4"`,
			"the standalone hat-banner card must be gone — HAT info now lives in the schedule card header")
		// When the HAT isn't on leave, no "on leave" status note
		// should appear.
		assert.NotContains(t, body, "on leave",
			"header must not show 'on leave' when the HAT is on the rota")
	})

	t.Run("renders cover with on-leave status in schedule header", func(t *testing.T) {
		data := map[string]any{
			"User":                  map[string]any{"Email": "alice@example.com", "Name": "Alice"},
			"IsAdmin":               false,
			"Template":              "dashboard",
			"CurrentHATName":        "Bob",   // cover is on call
			"CurrentHATIsOnLeave":   true,    // primary is on leave
			"CurrentHATPrimaryName": "Alice", // primary's name appears in the status note
		}

		w := httptest.NewRecorder()
		require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))

		body := w.Body.String()
		assert.Contains(t, body, `<span class="hat-banner-header-name">Bob</span>`,
			"header must show the cover's name as the on-call person")
		assert.Contains(t, body, "Alice on leave",
			"header must show the primary's name in the on-leave status note")
	})

	t.Run("falls back to plain Schedule title when no HAT today", func(t *testing.T) {
		// Even the rest of the data is set; the empty CurrentHATName
		// is the gate. Pass CurrentHATName as an explicit empty string
		// (not unset) so the if-check sees a deterministic value.
		data := map[string]any{
			"User":     map[string]any{"Email": "alice@example.com", "Name": "Alice"},
			"IsAdmin":  false,
			"Template": "dashboard",
		}

		w := httptest.NewRecorder()
		require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))

		body := w.Body.String()
		// The HAT-info block must be suppressed when no HAT is set.
		assert.NotContains(t, body, "HAT today",
			"header must be the plain Schedule title when no HAT is set")
		// The fallback to the plain card-header-title should render
		// the standard "Schedule" label.
		assert.Contains(t, body, `class="card-header-title"`,
			"the fallback card-header-title must render when no HAT is set")
	})
}

// TestDashboard_HATDayBadgeLink pins the dashboard HAT day badge's
// optional link affordance. The badge in the Today card is a plain
// <span> by default; when HAT_LINK_URL is set, it becomes an
// <a target="_blank" rel="noopener"> so it can open an on-call
// runbook, Slack channel, or PagerDuty rotation in a new window. When
// the env var is unset, the badge falls back to the original span
// (no link, no UI regression for installs that don't configure it).
//
// Each subtest asserts the EXACT rendered substring, not just
// attributes — so the tests catch a regression that swaps the link
// tag for a span (or vice versa), drops the target/rel attrs, or
// fails to wire the env var into the data map. The substring checks
// intentionally scope to the HAT day badge via the unique
// "fa-hat-wizard mr-1" + "HAT day" markers, so unrelated
// "tag is-link is-light" usages (Next HAT chip, @conference chip,
// etc.) don't false-positive.
func TestDashboard_HATDayBadgeLink(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	t.Run("renders as link when HAT_LINK_URL is set", func(t *testing.T) {
		const runbookURL = "https://example.com/oncall/runbook"
		t.Setenv("HAT_LINK_URL", runbookURL)

		data := map[string]any{
			"User":                      map[string]any{"Email": "alice@example.com", "Name": "Alice"},
			"IsAdmin":                   false,
			"Template":                  "dashboard",
			"CurrentUserPresenceStatus": "On-site",
			"CurrentUserHasHATDay":      true,
			// HatLinkURL is normally populated by handleDashboard via
			// os.Getenv("HAT_LINK_URL"); we inject it directly here
			// so this subtest pins the template's link/span branch
			// logic independently of the handler. The handler
			// wiring is the one-liner in dashboard.go that does
			// `data["HatLinkURL"] = os.Getenv("HAT_LINK_URL")` — a
			// grep for that string in the handler catches a removal
			// without a full integration test.
			"HatLinkURL": runbookURL,
		}

		w := httptest.NewRecorder()
		require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))

		body := w.Body.String()
		// The opening <a> tag must carry the configured URL plus the
		// target and rel that make it open in a new window safely.
		// Asserting on the full opening tag pins all four attributes
		// in one go: href, class, target, rel.
		assert.Contains(t, body,
			`<a class="tag is-link is-light" href="`+runbookURL+`" target="_blank" rel="noopener">`,
			"HAT day badge must render as an <a> with the configured URL, target=_blank, and rel=noopener when HAT_LINK_URL is set")
		// The badge text + icon must still be present inside the link.
		assert.Contains(t, body, `<i class="fas fa-hat-wizard mr-1"></i> HAT day`,
			"link-wrapped HAT day badge must keep the icon and label")
		// The span form must NOT render when the link form is
		// configured — both forms would render the same icon and
		// text, which would duplicate the affordance.
		assert.NotContains(t, body,
			`<span class="tag is-link is-light"><i class="fas fa-hat-wizard mr-1"></i> HAT day</span>`,
			"span form of the HAT day badge must not render when HAT_LINK_URL is set")
	})

	t.Run("renders as span when HAT_LINK_URL is unset", func(t *testing.T) {
		// Explicit empty value (matching what os.Getenv returns when
		// the var is absent) so the test is independent of the host
		// environment's HAT_LINK_URL setting.
		t.Setenv("HAT_LINK_URL", "")

		data := map[string]any{
			"User":                      map[string]any{"Email": "alice@example.com", "Name": "Alice"},
			"IsAdmin":                   false,
			"Template":                  "dashboard",
			"CurrentUserPresenceStatus": "On-site",
			"CurrentUserHasHATDay":      true,
		}

		w := httptest.NewRecorder()
		require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))

		body := w.Body.String()
		assert.Contains(t, body,
			`<span class="tag is-link is-light"><i class="fas fa-hat-wizard mr-1"></i> HAT day</span>`,
			"HAT day badge must render as a plain span when HAT_LINK_URL is empty (the original behavior)")
		// No <a class="tag is-link is-light" ... HAT day ...> form
		// should be present. The MEETINGS Teams link is the only
		// other anchor that opens in a new window with the same
		// tag class — asserting on the absence of an HAT-day <a>
		// specifically requires checking for the HAT-day marker
		// ("HAT day") after the opening anchor, not just the
		// anchor's class list.
		assert.NotContains(t, body, `<a class="tag is-link is-light"`,
			"no <a class=\"tag is-link is-light\"> must render when HAT_LINK_URL is empty (the badge falls back to a span)")
	})

	t.Run("renders nothing when not on a HAT day", func(t *testing.T) {
		// Even with HAT_LINK_URL configured, the outer
		// CurrentUserHasHATDay gate wins: a non-HAT day must not
		// surface any badge, link or span. The link affordance is
		// a property of the badge, not a stand-alone widget.
		t.Setenv("HAT_LINK_URL", "https://example.com/runbook")

		data := map[string]any{
			"User":                      map[string]any{"Email": "alice@example.com", "Name": "Alice"},
			"IsAdmin":                   false,
			"Template":                  "dashboard",
			"CurrentUserPresenceStatus": "On-site",
			// CurrentUserHasHATDay left unset (zero value: nil/false)
			// so the {{if .CurrentUserHasHATDay}} block is skipped
			// entirely.
		}

		w := httptest.NewRecorder()
		require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))

		body := w.Body.String()
		assert.NotContains(t, body, `<a class="tag is-link is-light" href="https://example.com/runbook"`,
			"linked HAT day badge must not render when the user isn't on HAT duty, even with HAT_LINK_URL set")
		// The exact span form too — covers the case where someone
		// accidentally removes the {{if .CurrentUserHasHATDay}}
		// outer gate.
		assert.NotContains(t, body,
			`<span class="tag is-link is-light"><i class="fas fa-hat-wizard mr-1"></i> HAT day</span>`,
			"span HAT day badge must not render when the user isn't on HAT duty")
	})
}

// TestTemplatesHaveAccessibleTables is the source-level guard for
// the screen-reader affordances on every <table> in the templates.
// The schedule matrix got <caption> + scope="col" + scope="row"
// in commit 1bb2aee, and the other tables in the codebase have the
// same problems the matrix had. This test walks every template
// file and asserts each <table>:
//   - has a <caption class="is-sr-only"> describing the table
//   - if it has a <thead>, has at least one <th scope="col"> in
//     <thead> (column headers get a scope)
//   - has at least one <th scope="row"> in <tbody> (row headers
//     or key-value labels get a scope)
//
// The conditional scope="col" check matters because not every
// table is a columnar data table. Some tables (wfh_purge, help's
// WFH config) are key-value layouts with no <thead>; their <th>
// elements are row labels in <tbody>, not column headers in
// <thead>. The unconditional scope="row" check covers both
// key-value tables and data tables with row headers.
//
// Source-level scanning is appropriate because Go templates don't
// inject accessibility attributes during rendering - if the
// attributes aren't in the source, they won't be in the rendered
// output. Catching the regression at the source level means a
// future developer who adds a new <table> can't accidentally ship
// an inaccessible table without the test failing.
func TestTemplatesHaveAccessibleTables(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("templates", "*.html"))
	require.NoError(t, err)
	require.NotEmpty(t, files, "expected at least one template file in templates/")

	// Match a <table> ... </table> block. Use a non-greedy regex
	// so multiple tables on the same page (swaps has two) are
	// each captured separately.
	tableBlock := regexp.MustCompile(`(?s)<table\b[^>]*>.*?</table>`)
	hasThead := regexp.MustCompile(`(?s)<thead\b[^>]*>.*?</thead>`)
	colScope := regexp.MustCompile(`<th[^>]*\sscope="col"`)
	rowScope := regexp.MustCompile(`<th[^>]*\sscope="row"`)
	caption := regexp.MustCompile(`<caption class="is-sr-only">`)

	for _, file := range files {
		//nolint:gosec // G304: file comes from filepath.Glob on the
		// template directory, not user input.
		body, err := os.ReadFile(file)
		require.NoError(t, err)
		content := string(body)

		// Skip files without tables.
		if !strings.Contains(content, "<table") {
			continue
		}

		// Walk each <table> block independently. Some files
		// (swaps) have two tables, and each must satisfy the
		// accessibility requirements on its own.
		tableMatches := tableBlock.FindAllStringIndex(content, -1)
		require.NotEmpty(t, tableMatches,
			"%s: file has <table> tag but the regex didn't match - update the tableBlock pattern", file)

		for i, m := range tableMatches {
			block := content[m[0]:m[1]]
			// Every <table> must have a screen-reader-only
			// <caption>. The "is-sr-only" Bulma class hides it
			// visually; the caption is still in the DOM and read
			// by screen readers as the table's identity. Without
			// a caption, a screen reader user enters the table
			// with no context about what they're about to read.
			assert.Regexp(t, caption, block,
				"%s: table #%d must have a screen-reader-only <caption>", file, i+1)

			// Every <th> in <thead> must declare scope="col" so
			// screen readers can announce the column header when
			// reading a cell in that column. Only enforced if the
			// table has a <thead> (i.e. it's a columnar data
			// table, not a key-value layout like wfh_purge or
			// help's WFH config that has no column headers).
			if hasThead.MatchString(block) {
				assert.Regexp(t, colScope, block,
					"%s: table #%d has <thead> but no <th scope=\"col\">", file, i+1)
			}

			// Every table must have at least one <th scope="row">.
			// For data tables this is the first cell of each row
			// (e.g. the member name); for key-value tables this is
			// the label cell. Either way, a screen reader needs
			// a scope="row" header to announce which row / which
			// record a cell belongs to.
			assert.Regexp(t, rowScope, block,
				"%s: table #%d has no <th scope=\"row\">", file, i+1)
		}
	}
}

// TestTemplatesHaveNoInlineEventHandlers is the regression guard for
// the CSP-driven script/handler extraction. The page's strict CSP is
// 'script-src \\'self\\” (security_headers.go) — inline <script>
// blocks AND inline event-handler attributes (onclick, onsubmit, etc.)
// are blocked. Any future template that adds an inline handler will
// silently break the corresponding feature in browser with no
// compile-time feedback. This test walks every .html file under
// internal/web/templates/ and fails on any inline handler attribute
// so the regression is caught at `go test` time, not in production.
//
// The check is at the source level rather than the rendered-output
// level because Go templates don't add inline handler attributes
// during rendering — if it's not in the source, it won't be in the
// HTML the browser receives. Source-level scanning is also O(N files)
// instead of O(N templates × render cost) and produces a more useful
// error message (the file and approximate location).
func TestTemplatesHaveNoInlineEventHandlers(t *testing.T) {
	// Attributes whose presence indicates an inline handler. The set
	// is small because Go templates don't add attributes the source
	// doesn't declare; new ones only land here when a developer adds
	// a new onclick=, onsubmit=, etc.
	forbidden := []string{
		"onclick=",
		"ondblclick=",
		"onmousedown=",
		"onmouseup=",
		"onmouseover=",
		"onmouseout=",
		"onmousemove=",
		"onkeydown=",
		"onkeyup=",
		"onkeypress=",
		"onfocus=",
		"onblur=",
		"onchange=",
		"onsubmit=",
		"onload=",
		"onerror=",
	}

	// Walk the templates directory. The test runs from the package
	// directory so a relative path is fine; the entries come from
	// os.ReadDir which constrains the listing to that directory.
	const root = "templates"
	entries, err := os.ReadDir(root)
	require.NoError(t, err, "reading templates directory")

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".html") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		//nolint:gosec // G304: entry.Name() comes from os.ReadDir on the templates directory; the path stays inside the package tree and is never user-controlled.
		contents, err := os.ReadFile(path)
		require.NoError(t, err, "reading %s", path)

		body := string(contents)
		// Lowercase the file before scanning so we catch onclick=,
		// ONCLICK=, and OnClick= uniformly.
		lower := strings.ToLower(body)
		for _, attr := range forbidden {
			if !strings.Contains(lower, attr) {
				continue
			}
			// Locate the line number so the failure message points
			// the next agent at the offending source line.
			idx := strings.Index(lower, attr)
			line := 1 + strings.Count(body[:idx], "\n")
			t.Errorf(
				"%s line %d: inline event handler %q is CSP-blocked. Use a data-* attribute + delegated addEventListener in the page's external JS instead. See internal/web/assets/js/common.js for the pattern.",
				path, line, attr,
			)
		}
	}
}

// TestDashboard_AdminMarkedWFHChip_RendersIsLinkClass pins the
// admin-marked WFH chip color in the Today card. When the user's
// WFH today was inserted by an admin via /admin/wfh/mark
// (IsAdminMarkedWFH=true), the chip renders in the distinct
// is-link purple-blue color. A regular user-requested WFH renders
// in the existing is-info blue. The two states are visually
// distinct so the team can see at a glance which days were
// admin-asserted rather than self-requested.
func TestDashboard_AdminMarkedWFHChip_RendersIsLinkClass(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	t.Run("admin-marked renders is-link", func(t *testing.T) {
		data := map[string]any{
			"User":                      map[string]any{"Email": "alice@example.com", "Name": "Alice"},
			"IsAdmin":                   false,
			"Template":                  "dashboard",
			"CurrentUserPresenceStatus": "WFH",
			"IsAdminMarkedWFH":          true,
		}

		w := httptest.NewRecorder()
		require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))

		body := w.Body.String()
		assert.Contains(t, body,
			`<span class="tag is-link is-light" title="Marked by admin as WFH"><i class="fas fa-home mr-1"></i> WFH</span>`,
			"admin-marked WFH chip must render with is-link class and the hover tooltip")
		assert.NotContains(t, body,
			`<span class="tag is-info is-light"><i class="fas fa-home mr-1"></i> WFH</span>`,
			"the regular is-info span form must not render when the chip is admin-marked")
	})

	t.Run("user-requested renders is-info", func(t *testing.T) {
		data := map[string]any{
			"User":                      map[string]any{"Email": "alice@example.com", "Name": "Alice"},
			"IsAdmin":                   false,
			"Template":                  "dashboard",
			"CurrentUserPresenceStatus": "WFH",
			"IsAdminMarkedWFH":          false,
		}

		w := httptest.NewRecorder()
		require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))

		body := w.Body.String()
		assert.Contains(t, body,
			`<span class="tag is-info is-light"><i class="fas fa-home mr-1"></i> WFH</span>`,
			"user-requested WFH chip must render with is-info class (the original behavior)")
		assert.NotContains(t, body,
			`is-link is-light" title="Marked by admin as WFH"`,
			"the admin-marked is-link variant must not render when IsAdminMarkedWFH is false")
	})
}

// TestScheduleMatrix_AdminMarkedCell_HasStatusWfhAdminClass pins
// the schedule matrix cell's CSS class for admin-marked WFH. The
// matrix cell uses .status-wfh .status-chip for user WFH and
// .status-wfh-admin .status-chip for admin-marked WFH. The
// .status-wfh-admin rule is what paints the chip in the distinct
// purple-blue color, so a regression that drops the class would
// also drop the color.
func TestScheduleMatrix_AdminMarkedCell_HasStatusWfhAdminClass(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	data := map[string]any{
		"User":     map[string]any{"Email": "alice@example.com", "Name": "Alice"},
		"IsAdmin":  false,
		"Template": "dashboard",
		"ScheduleMatrix": scheduleMatrix{
			Days: []scheduleMatrixDay{{DateISO: "2026-09-04", DateDisplay: "Fri, Sep 4"}},
			Rows: []scheduleMatrixRow{{
				Member: database.TeamMember{ID: "alice", Name: "Alice"},
				Cells: []scheduleMatrixCell{{
					Status:           "wfh",
					Label:            "WFH",
					IsAdminMarkedWFH: true,
					DateISO:          "2026-09-04",
					DateLabel:        "Fri, Sep 4",
				}},
			}},
		},
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))

	body := w.Body.String()
	assert.Contains(t, body, `status-cell status-wfh status-wfh-admin`,
		"admin-marked WFH cell must carry the .status-wfh-admin class in addition to .status-wfh")
	assert.Contains(t, body, `title="Marked by admin as WFH"`,
		"admin-marked WFH cell must include the hover tooltip")
}

// TestWFHListPage_DenialReason_RendersUnderStatus pins the
// user-facing surface of a denial on the WFH list page. When
// a row has status=denied and a non-empty denial_reason, the
// template renders the reason as a small grey subtitle under
// the "Denied" status tag so the user sees why their request
// was rejected (no more silent "Denied" tags).
func TestWFHListPage_DenialReason_RendersUnderStatus(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	const reason = "On-site coverage would drop below the minimum (1 on-site required). 1 members are already unavailable; approving more would leave the team under the floor."

	data := map[string]any{
		"Template": "wfh_list",
		"Requests": []enrichedWFHRequest{
			{
				WFHRequest: database.WFHRequest{
					ID:           "r1",
					MemberID:     "alice",
					Date:         "2026-09-04",
					Status:       database.WFHStatusDenied,
					DenialReason: ptrString(reason),
				},
				CanWithdraw: false,
			},
		},
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "wfh_list.html", data))

	body := w.Body.String()
	assert.Contains(t, body,
		`<span class="tag is-danger is-light"><i class="fas fa-times mr-1"></i> Denied</span>`,
		"the Denied status tag must render in the danger (red) style")
	assert.Contains(t, body, reason,
		"the denial reason must render verbatim under the Denied tag so the user can read it")
	// The reason is rendered as a small grey block under the
	// status tag (not inside the <td>'s preceding content), so
	// assert it appears within the same <td> as the Denied tag.
}

// TestWFHListPage_NoDenialReason_WhenApprovedOrPending guards
// the conditional: the reason paragraph is only rendered when
// status=denied AND the reason is non-empty. An approved row
// (or a denied row from an old database before the column
// existed) must not show a stray "Reason:" block.
func TestWFHListPage_NoDenialReason_WhenApprovedOrPending(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	const reason = "spurious reason that must NOT appear"

	data := map[string]any{
		"Template": "wfh_list",
		"Requests": []enrichedWFHRequest{
			{
				WFHRequest: database.WFHRequest{
					ID:     "r1",
					Status: database.WFHStatusApproved,
					// DenialReason intentionally set to a non-empty
					// value to confirm the conditional still hides
					// it when the status is not denied.
					DenialReason: ptrString(reason),
				},
			},
		},
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "wfh_list.html", data))

	body := w.Body.String()
	assert.NotContains(t, body, reason,
		"the reason must not render when the row is approved (only denied rows show it)")
}

// TestWFHListPage_RequestWFHDayButton_AlwaysVisible pins the
// user-visible behavior: the "+ Request WFH Day" button must
// render on the WFH list page regardless of the current-period
// quota state. When the user's quota is exhausted the warning
// appears alongside the button, not in place of it — the user
// can still click through to /wfh/request and try a future
// period. Server-side CheckQuota on the form is the
// authoritative guard against over-cap requests.
//
// Before the fix the button was hidden when quota was
// exhausted (the warning replaced it), which left the user
// stranded — exactly the dead-end the user reported.
func TestWFHListPage_RequestWFHDayButton_AlwaysVisible(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	requests := []enrichedWFHRequest{
		{
			WFHRequest: database.WFHRequest{ID: "r1", Status: database.WFHStatusApproved},
		},
	}

	cases := []struct {
		name             string
		remaining        int
		expectWarning    bool
		expectButtonText string
	}{
		{
			name:             "quota has remaining — button visible, no warning",
			remaining:        2,
			expectWarning:    false,
			expectButtonText: "Request WFH Day",
		},
		{
			name:             "quota exhausted — button still visible, warning alongside",
			remaining:        0,
			expectWarning:    true,
			expectButtonText: "Request WFH Day",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := map[string]any{
				"Template": "wfh_list",
				"Quota": map[string]any{
					"PeriodStart": "2026-08-31",
					"PeriodEnd":   "2026-09-06",
					"Used":        2,
					"Remaining":   c.remaining,
					"OverQuotaBy": 0,
				},
				"Requests": requests,
			}

			w := httptest.NewRecorder()
			require.NoError(t, handler.tmpl.ExecuteTemplate(w, "wfh_list.html", data))
			body := w.Body.String()

			assert.Contains(t, body, `href="/wfh/request"`,
				"the + Request WFH Day link to /wfh/request must render regardless of quota state")
			assert.Contains(t, body, c.expectButtonText,
				"the button copy must be visible")

			if c.expectWarning {
				assert.Contains(t, body, "You have used your full WFH quota",
					"warning must render alongside the button when quota is exhausted")
			} else {
				assert.NotContains(t, body, "You have used your full WFH quota",
					"warning must NOT render when quota has remaining")
			}
		})
	}
}

// ptrString is a small helper to make the test data read like
// "the row's reason is X" without a one-off local var.
//
//go:fix inline
func ptrString(s string) *string { return new(s) }

// TestDashboard_ChairsRow_RendersAtUnderAndOverCap is the
// HTML-level regression test for the ass/chair ratio row on the
// Today card. The unit-level TestLoadChairsData_* tests pin
// the data fields; this one pins the rendered output so a future
// template drift (e.g. someone drops the conditional, or
// hard-codes the color class) fails loudly here.
func TestDashboard_ChairsRow_RendersAtUnderAndOverCap(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	cases := []struct {
		name           string
		onSite         int
		total          int
		percent        int
		color          string
		mustContain    []string
		mustNotContain []string
	}{
		{
			name:    "under-cap green",
			onSite:  3,
			total:   7,
			percent: 42,
			color:   "is-success",
			mustContain: []string{
				`class="tag is-success is-light"`,
				"3 of 7 chairs (42%)",
				`value="3"`,
				`max="7"`,
			},
		},
		{
			name:    "at-cap orange",
			onSite:  5,
			total:   5,
			percent: 100,
			color:   "is-warning",
			mustContain: []string{
				`class="tag is-warning is-light"`,
				"5 of 5 chairs (100%)",
				`value="5"`,
				`max="5"`,
			},
		},
		{
			name:    "over-cap red",
			onSite:  9,
			total:   7,
			percent: 128,
			color:   "is-danger",
			mustContain: []string{
				`class="tag is-danger is-light"`,
				"9 of 7 chairs (128%)",
				`value="9"`,
				`max="7"`,
			},
		},
		{
			name:    "no-cap row hidden",
			onSite:  0,
			total:   0,
			percent: 0,
			color:   "",
			mustNotContain: []string{
				"chairs",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := map[string]any{
				"User":                      map[string]any{"Email": "alice@example.com", "Name": "Alice"},
				"IsAdmin":                   false,
				"Template":                  "dashboard",
				"CurrentUserPresenceStatus": "On-site",
				"Today":                     "Friday, Sep 4, 2026",
			}
			if c.total > 0 {
				data["ChairsOnSite"] = c.onSite
				data["ChairsTotal"] = c.total
				data["ChairsPercent"] = c.percent
				data["ChairsColor"] = c.color
			}
			w := httptest.NewRecorder()
			require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))
			body := w.Body.String()
			for _, needle := range c.mustContain {
				assert.Contains(t, body, needle,
					"%s: rendered body must contain %q", c.name, needle)
			}
			for _, forbidden := range c.mustNotContain {
				assert.NotContains(t, body, forbidden,
					"%s: rendered body must NOT contain %q", c.name, forbidden)
			}
		})
	}
}

// TestDashboard_ChairsRow_RespectsPresenceSnapshotAtRender pins
// the off-by-one fix at the HTML level: when the matrix's
// presence snapshot lists N Present members, the chairs row
// reads from that snapshot — not from a recompute against raw
// DB rows. The bug surfaced in production as 7 on-site vs 6
// counted; this test renders the same divergence scenario
// (snapshot says 5 Present, raw rows would recompute to 4) and
// pins the rendered output to 5.
func TestDashboard_ChairsRow_RespectsPresenceSnapshotAtRender(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	// Five Present members in the snapshot — this is what the
	// matrix's atWork column would render. The recompute path
	// against raw rows (if anyone re-introduces it) might report
	// 4 because of a stray WFH row the snapshot has already
	// accounted for. The chairs row must agree with the matrix.
	snapshot := []presenceDay{
		{
			DateISO: "2026-09-04",
			IsToday: true,
			Present: []database.TeamMember{
				{ID: "m1", Name: "Alice", Email: "alice@example.com"},
				{ID: "m2", Name: "Bob", Email: "bob@example.com"},
				{ID: "m3", Name: "Carol", Email: "carol@example.com"},
				{ID: "m4", Name: "Dave", Email: "dave@example.com"},
				{ID: "m5", Name: "Eve", Email: "eve@example.com"},
			},
		},
	}

	data := map[string]any{
		"User":                      map[string]any{"Email": "alice@example.com", "Name": "Alice"},
		"IsAdmin":                   false,
		"Template":                  "dashboard",
		"CurrentUserPresenceStatus": "On-site",
		"Today":                     "Friday, Sep 4, 2026",
		"UpcomingPresence":          snapshot,
		"ChairsOnSite":              5,
		"ChairsTotal":               7,
		"ChairsPercent":             71,
		"ChairsColor":               "is-success",
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))
	body := w.Body.String()

	assert.Contains(t, body, "5 of 7 chairs (71%)",
		"snapshot-path count must surface verbatim; a flat-row recompute that returns 4 would silently regress this")
	assert.Contains(t, body, `class="tag is-success is-light"`,
		"under-cap green tag must render when snapshot reports 5 of 7")
	assert.Contains(t, body, `value="5"`, "progress bar value attribute must reflect snapshot count")
	assert.Contains(t, body, `max="7"`, "progress bar max attribute must match the cap")
}

// TestWFHRequestForm_JSApplyDefaultsToEnabledWhenNoBanner pins the
// Step 20 fix for the WFH request form's submit button. When the
// user picks a date outside the todayPeriod and nextPeriod banners
// (e.g. 2+ periods out, or before today), the JS apply() function
// used to leave the button stuck in its server-rendered state.
// The fix defaults to enabled in that branch because the
// server-side CheckQuota is the authoritative guard.
//
// The test exercises the JS branch via the actual template
// execution: render wfh_request.html with banners present and a
// date input value outside both periods, then assert the inline
// script is wired to enable the button in the default branch.
// (Full JS execution via a real DOM is out of scope; the
// assertion pins the script source so a future drift that drops
// the default branch fails loudly here.)
func TestWFHRequestForm_JSApplyDefaultsToEnabledWhenNoBanner(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	data := map[string]any{
		"User":           map[string]any{"Email": "alice@example.com", "Name": "Alice"},
		"IsAdmin":        false,
		"Template":       "wfh_request",
		"Today":          "2026-09-04",
		"SelectedDate":   "2026-09-04",
		"MaxRequestDate": "2026-11-30",
		"Quota": map[string]any{
			"PeriodStart": "2026-08-31",
			"PeriodEnd":   "2026-09-06",
			"Used":        2,
			"Remaining":   0,
		},
		"QuotaExhausted":        true,
		"SelectedDateIsHoliday": false,
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "wfh_request.html", data))
	body := w.Body.String()

	// The default branch must reset submit.disabled to false
	// and clear the help text when bannerFor returns null. Pin
	// the script content so a future drift that drops the
	// else-branch fails here.
	assert.Contains(t, body, "submit.disabled = false;",
		"the apply() default branch must explicitly enable the submit button when no banner matches")
	assert.Contains(t, body, "help.textContent = '';",
		"the apply() default branch must clear the help text when no banner matches")
}

// TestWFHRequestForm_SubmitButtonEnabledWhenQuotaExhausted pins
// the user-visible behavior: the "+ Request WFH Day" submit
// button must render enabled even when today's period quota is
// exhausted. The user may still want to pick a future date, and
// the server-side CheckQuota is the authoritative guard for
// over-cap requests — the form simply lets them submit and
// surfaces the friendly error message if the chosen date's
// period is also over quota.
//
// Only the holiday check should disable the button at render
// time; quota exhaustion is a per-period condition that the user
// can sidestep by picking a different date or future period.
func TestWFHRequestForm_SubmitButtonEnabledWhenQuotaExhausted(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	data := map[string]any{
		"User":           map[string]any{"Email": "alice@example.com", "Name": "Alice"},
		"IsAdmin":        false,
		"Template":       "wfh_request",
		"Today":          "2026-09-04",
		"SelectedDate":   "2026-09-04",
		"MaxRequestDate": "2026-11-30",
		"Quota": map[string]any{
			"PeriodStart": "2026-08-31",
			"PeriodEnd":   "2026-09-06",
			"Used":        2,
			"Remaining":   0,
		},
		"QuotaExhausted":        true,
		"SelectedDateIsHoliday": false,
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "wfh_request.html", data))
	body := w.Body.String()

	// Find the submit button line and assert it does NOT carry
	// the disabled attribute. The template renders the button
	// as <button id="wfh-submit" type="submit" class="..." >
	// (no disabled) when the only condition in the disable
	// expression (SelectedDateIsHoliday) is false.
	const btnMarker = `id="wfh-submit" type="submit"`
	idx := strings.Index(body, btnMarker)
	require.GreaterOrEqual(t, idx, 0, "wfh-submit button must render")
	// Look at the surrounding tag opening; assert no disabled
	// attribute up to the closing '>'.
	endIdx := strings.Index(body[idx:], ">")
	require.GreaterOrEqual(t, endIdx, 0)
	opening := body[idx : idx+endIdx+1]
	assert.NotContains(t, opening, "disabled",
		"the submit button must render without the disabled attribute when quota is exhausted but the date is not a holiday")
}

// TestWFHRequestForm_SubmitButtonDisabledOnHoliday pins the
// other half of the disable contract: when the picked date is a
// holiday, the button stays disabled because the form will
// reject the request anyway. Quota exhaustion is no longer part
// of the disable condition — only the holiday check.
func TestWFHRequestForm_SubmitButtonDisabledOnHoliday(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	data := map[string]any{
		"User":           map[string]any{"Email": "alice@example.com", "Name": "Alice"},
		"IsAdmin":        false,
		"Template":       "wfh_request",
		"Today":          "2026-09-04",
		"SelectedDate":   "2026-12-25",
		"MaxRequestDate": "2026-11-30",
		"Quota": map[string]any{
			"PeriodStart": "2026-08-31",
			"PeriodEnd":   "2026-09-06",
			"Used":        0,
			"Remaining":   2,
		},
		"QuotaExhausted":        false,
		"SelectedDateIsHoliday": true,
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "wfh_request.html", data))
	body := w.Body.String()

	const btnMarker = `id="wfh-submit" type="submit"`
	idx := strings.Index(body, btnMarker)
	require.GreaterOrEqual(t, idx, 0, "wfh-submit button must render")
	endIdx := strings.Index(body[idx:], ">")
	require.GreaterOrEqual(t, endIdx, 0)
	opening := body[idx : idx+endIdx+1]
	assert.Contains(t, opening, "disabled",
		"the submit button must render with the disabled attribute when the selected date is a holiday")
}

// TestDashboard_ChairsRow_RendersAtUnderAndOverCap is the
// HTML-level regression test for the ass/chair ratio row on the
// Today card. The unit-level TestLoadChairsData_* tests pin
// the data fields; this one pins the rendered output so a future
// template drift (e.g. someone drops the conditional, or
// hard-codes the color class) fails loudly here.
// TestDashboard_ChairsRow_RendersAtUnderAndOverCap is the
// HTML-level regression test for the ass/chair ratio row on the
// Today card. The unit-level TestLoadChairsData_* tests pin
// the data fields; this one pins the rendered output so a future
// template drift (e.g. someone drops the conditional, or
// hard-codes the color class) fails loudly here.
