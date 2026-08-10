package web

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

	// The fade must be a pseudo-element on the matrix wrap, anchored
	// to the right edge, with a gradient that fades to transparent so
	// the underlying table content is visible through the fade.
	assert.Contains(t, body, ".schedule-matrix-wrap::after",
		"scroll hint fade must be a pseudo-element on the matrix wrap")
	assert.Contains(t, body, "linear-gradient(to left,",
		"fade must run from right (opaque) to left (transparent)")
	assert.Contains(t, body, "rgba(255, 255, 255, 0.95)",
		"fade must start near-white to match the table background")
	assert.Contains(t, body, "rgba(255, 255, 255, 0)",
		"fade must end transparent so the table content shows through")
	assert.Contains(t, body, "pointer-events: none",
		"fade must not block clicks on the rightmost table cells")

	// The wrap must be position:relative for the absolute-positioned
	// pseudo-element to anchor to it. Without this, the fade would
	// float to the top-left of the page.
	assert.Contains(t, body, ".schedule-matrix-wrap {",
		"matrix wrap must be defined as the fade anchor")
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

// TestDashboard_HATBanner guards the top-level HAT banner that
// surfaces the most-asked question of a rota app: who is on support
// today. The banner is rendered between the status card and the
// schedule card, and is suppressed when CurrentHATName is empty
// (the schedule maintenance guarantees a primary assignment for
// today, but the template must still gracefully handle the empty
// case — first day of operation, fixture gaps, etc.).
//
// Three cases covered:
//   - Normal: HAT today is Alice, no leave flag → banner shows Alice
//     and no "on leave" status.
//   - On leave: HAT today is Bob (the cover), CurrentHATIsOnLeave is
//     true → banner shows Bob with "(Alice) on leave" status note.
//   - Empty: CurrentHATName is empty → banner is suppressed entirely.
func TestDashboard_HATBanner(t *testing.T) {
	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	t.Run("renders primary HAT", func(t *testing.T) {
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
		assert.Contains(t, body, `class="hat-banner card mb-4"`,
			"HAT banner wrapper must be present")
		assert.Contains(t, body, "HAT today",
			"banner must include the 'HAT today' label")
		assert.Contains(t, body, "Alice",
			"banner must include the HAT's name")
		assert.Contains(t, body, `href="/calendar"`,
			"banner must include a link to the schedule")
		// When the HAT isn't on leave, no "on leave" status note
		// should appear.
		assert.NotContains(t, body, "on leave",
			"banner must not show 'on leave' when the HAT is on the rota")
	})

	t.Run("renders cover with on-leave status", func(t *testing.T) {
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
		assert.Contains(t, body, "<span class=\"hat-banner-name\">Bob</span>",
			"banner must show the cover's name as the on-call person")
		assert.Contains(t, body, "Alice on leave",
			"banner must show the primary's name in the on-leave status note")
	})

	t.Run("suppressed when no HAT today", func(t *testing.T) {
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
		assert.NotContains(t, body, "HAT today",
			"banner must be suppressed when no HAT is set")
		assert.NotContains(t, body, `class="hat-banner"`,
			"banner wrapper must not render when no HAT is set")
	})
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
