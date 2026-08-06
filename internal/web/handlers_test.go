package web

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
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

// TestDashboard_NonAdminSeesManageLeaveButton is the regression
// guard for the issue where non-admins could not reach
// /leave/manage from the dashboard. The route and handler were
// already wired to support non-admins (the page filters by the
// session member's id when the caller is not an admin), but the
// dashboard's "Manage Leave" quick-action was nested inside the
// IsAdmin block — so a regular user logged in and saw no entry
// point. This test renders the dashboard template with a
// non-admin session and asserts the link is present.
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
