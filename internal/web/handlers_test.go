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
