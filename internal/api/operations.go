package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/inful/madhatter/internal/auth"
)

func (s *Server) registerOperations(development bool) {
	// Team Operations
	huma.Register(s.api, huma.Operation{
		OperationID: "add-team-member",
		Method:      http.MethodPost,
		Path:        "/api/v1/team",
		Summary:     "Add a new team member",
		Tags:        []string{"Team"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleAddTeam)

	huma.Register(s.api, huma.Operation{
		OperationID: "list-team-members",
		Method:      http.MethodGet,
		Path:        "/api/v1/team",
		Summary:     "List all active team members",
		Tags:        []string{"Team"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleListTeam)

	huma.Register(s.api, huma.Operation{
		OperationID: "update-team-member",
		Method:      http.MethodPut,
		Path:        "/api/v1/team/{id}",
		Summary:     "Update a team member",
		Tags:        []string{"Team"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleUpdateTeam)

	huma.Register(s.api, huma.Operation{
		OperationID: "delete-team-member",
		Method:      http.MethodDelete,
		Path:        "/api/v1/team/{id}",
		Summary:     "Delete a team member",
		Tags:        []string{"Team"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleDeleteTeam)

	// Leave Operations
	huma.Register(s.api, huma.Operation{
		OperationID: "report-leave",
		Method:      http.MethodPost,
		Path:        "/api/v1/leave",
		Summary:     "Report leave for a team member",
		Tags:        []string{"Leave"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleReportLeave)

	huma.Register(s.api, huma.Operation{
		OperationID: "list-leave",
		Method:      http.MethodGet,
		Path:        "/api/v1/leave",
		Summary:     "List leave records",
		Tags:        []string{"Leave"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleListLeave)

	huma.Register(s.api, huma.Operation{
		OperationID: "update-leave",
		Method:      http.MethodPut,
		Path:        "/api/v1/leave/{id}",
		Summary:     "Update a leave record",
		Tags:        []string{"Leave"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleUpdateLeave)

	huma.Register(s.api, huma.Operation{
		OperationID: "delete-leave",
		Method:      http.MethodDelete,
		Path:        "/api/v1/leave/{id}",
		Summary:     "Delete a leave record",
		Tags:        []string{"Leave"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleDeleteLeave)

	// Schedule Operations
	huma.Register(s.api, huma.Operation{
		OperationID: "generate-schedule",
		Method:      http.MethodPost,
		Path:        "/api/v1/schedule/generate",
		Summary:     "Generate schedule for date range",
		Tags:        []string{"Schedule"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleGenerateSchedule)

	// Calendar Operations
	huma.Register(s.api, huma.Operation{
		OperationID: "subscribe-calendar",
		Method:      http.MethodPost,
		Path:        "/api/v1/calendar/subscribe",
		Summary:     "Create calendar subscription",
		Tags:        []string{"Calendar"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleSubscribeCalendar)

	// ICS Feed (no auth required - uses token in URL)
	s.router.Get("/api/v1/calendar/{token}/ics", s.handleCalendarICS)

	// Development mode fake auth routes
	if development && s.authManager != nil {
		fakeHandler := auth.NewFakeCallbackHandler()
		s.router.HandleFunc("/auth/fake/login", fakeHandler.HandleLogin)
	}

	// Holiday Operations
	huma.Register(s.api, huma.Operation{
		OperationID: "get-holidays",
		Method:      http.MethodGet,
		Path:        "/api/v1/holidays",
		Summary:     "Get upcoming holidays",
		Tags:        []string{"Holidays"},
		// Public endpoint - no auth required
	}, s.handleGetHolidays)

	huma.Register(s.api, huma.Operation{
		OperationID: "get-holiday-status",
		Method:      http.MethodGet,
		Path:        "/api/v1/holidays/status",
		Summary:     "Get holiday service status",
		Tags:        []string{"Holidays"},
		// Public endpoint - no auth required
	}, s.handleGetHolidayStatus)

	huma.Register(s.api, huma.Operation{
		OperationID: "refresh-holidays",
		Method:      http.MethodPost,
		Path:        "/api/v1/holidays/refresh",
		Summary:     "Manually refresh holiday data",
		Tags:        []string{"Holidays"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleRefreshHolidays)

	// API Token Operations
	huma.Register(s.api, huma.Operation{
		OperationID: "generate-api-token",
		Method:      http.MethodPost,
		Path:        "/api/v1/tokens/generate",
		Summary:     "Generate a new API token",
		Tags:        []string{"Authentication"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
		},
	}, s.handleGenerateAPIToken)

	huma.Register(s.api, huma.Operation{
		OperationID: "list-api-tokens",
		Method:      http.MethodGet,
		Path:        "/api/v1/tokens",
		Summary:     "List user's API tokens",
		Tags:        []string{"Authentication"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
		},
	}, s.handleListAPITokens)

	huma.Register(s.api, huma.Operation{
		OperationID: "revoke-api-token",
		Method:      http.MethodDelete,
		Path:        "/api/v1/tokens/{id}",
		Summary:     "Revoke an API token",
		Tags:        []string{"Authentication"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
		},
	}, s.handleRevokeAPIToken)

	huma.Register(s.api, huma.Operation{
		OperationID: "cleanup-expired-tokens",
		Method:      http.MethodPost,
		Path:        "/api/v1/tokens/cleanup",
		Summary:     "Cleanup expired API tokens (admin only)",
		Tags:        []string{"Authentication"},
		Security: []map[string][]string{
			{"sessionAuth": {}},
			{"apiTokenAuth": {}},
		},
	}, s.handleCleanupExpiredTokens)
}
