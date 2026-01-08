package web

import (
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
		parseTemplates()
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

	// Create handler - this will parse templates
	handler := NewHandler(mockDB, mockAuthManager, mockMiddleware)

	// Test data that would be passed to the template
	data := map[string]any{
		"Providers": []string{"forgejo", "gitlab"},
	}

	// Try to execute the login template
	w := httptest.NewRecorder()

	// Execute template directly to test for errors
	err := handler.tmpl.ExecuteTemplate(w, "login.html", data)

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

	// This should not panic
	assert.NotPanics(t, func() {
		NewHandler(mockDB, mockAuthManager, mockMiddleware)
	})
}

// TestRegisterRoutes verifies routes can be registered without template errors.
func TestRegisterRoutes(t *testing.T) {
	mockDB := &database.DB{}
	mockAuthManager := &auth.AuthManager{}
	mockMiddleware := &auth.Middleware{}

	// Create handler
	handler := NewHandler(mockDB, mockAuthManager, mockMiddleware)

	// Get router
	router := handler.Router()

	// Verify router is not nil
	require.NotNil(t, router)
}

// TestAllTemplatesParse verifies all templates can be parsed without errors
func TestAllTemplatesParse(t *testing.T) {
	mockDB := &database.DB{}
	mockAuthManager := &auth.AuthManager{}
	mockMiddleware := &auth.Middleware{}

	// Create handler - this will parse templates
	handler := NewHandler(mockDB, mockAuthManager, mockMiddleware)

	// All templates should be loaded without errors
	require.NotNil(t, handler.tmpl, "Template should be loaded")

	// List of all templates that should exist
	templates := []string{
		"login.html",
		"dashboard.html",
		"team.html",
		"leave_report.html",
		"schedule_current.html",
		"schedule_generate.html",
		"calendar.html",
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

// TestAllTemplatesWithData verifies all templates work with common data structures
func TestAllTemplatesWithData(t *testing.T) {
	mockDB := &database.DB{}
	mockAuthManager := &auth.AuthManager{}
	mockMiddleware := &auth.Middleware{}

	// Create handler
	handler := NewHandler(mockDB, mockAuthManager, mockMiddleware)

	// Create test data that templates expect
	dashboardData := map[string]any{
		"Assignments": []map[string]any{
			{"Date": "2026-01-10", "Member": "Test User", "IsCover": false, "IsLeave": false},
		},
		"User": map[string]any{"Email": "test@example.com", "IsAdmin": true},
		"IsAdmin": true,
	}

	teamData := map[string]any{
		"Members": []map[string]any{
			{"ID": 1, "Name": "Test User", "Email": "test@example.com", "Active": true},
		},
		"User": map[string]any{"Email": "test@example.com", "IsAdmin": true},
		"IsAdmin": true,
	}

	leaveData := map[string]any{
		"Members": []map[string]any{
			{"ID": 1, "Name": "Test User", "Email": "test@example.com"},
		},
		"Leave": []map[string]any{},
		"User": map[string]any{"Email": "test@example.com", "IsAdmin": true},
		"IsAdmin": true,
	}

	scheduleData := map[string]any{
		"Assignments": []map[string]any{
			{"Date": "2026-01-10", "Member": "Test User", "IsCover": false, "IsLeave": false},
		},
		"User": map[string]any{"Email": "test@example.com", "IsAdmin": true},
		"IsAdmin": true,
	}

	generateData := map[string]any{
		"Members": []map[string]any{
			{"ID": 1, "Name": "Test User", "Email": "test@example.com"},
		},
		"User": map[string]any{"Email": "test@example.com", "IsAdmin": true},
		"IsAdmin": true,
	}

	calendarData := map[string]any{
		"Subscriptions": []map[string]any{
			{"Token": "test-token", "Name": "Test Calendar", "CreatedAt": "2026-01-08"},
		},
		"User": map[string]any{"Email": "test@example.com", "IsAdmin": true},
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
		{"ScheduleCurrent", "schedule_current.html", scheduleData},
		{"ScheduleGenerate", "schedule_generate.html", generateData},
		{"Calendar", "calendar.html", calendarData},
		{"Login", "login.html", loginData},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			err := handler.tmpl.ExecuteTemplate(w, tc.template, tc.data)
			require.NoError(t, err, "Template %s should execute with data", tc.template)
			assert.Equal(t, 200, w.Code, "Template %s should return 200", tc.template)
			assert.NotEmpty(t, w.Body.String(), "Template %s should produce output", tc.template)
		})
	}
}
