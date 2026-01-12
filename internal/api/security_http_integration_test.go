package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func doJSONRequest(t *testing.T, server *Server, method, path string, body any, headers map[string]string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	var bodyReader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		bodyReader = bytes.NewReader(b)
	} else {
		bodyReader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, bodyReader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}

	w := httptest.NewRecorder()
	server.router.ServeHTTP(w, req)
	return w
}

func findOperationSecurity(t *testing.T, openAPIDoc map[string]any, path, method string) []any {
	t.Helper()

	paths, ok := openAPIDoc["paths"].(map[string]any)
	require.True(t, ok, "openapi doc missing paths")

	pathItem, ok := paths[path].(map[string]any)
	require.True(t, ok, "openapi doc missing path %q", path)

	op, ok := pathItem[method].(map[string]any)
	require.True(t, ok, "openapi doc missing method %q for path %q", method, path)

	sec, _ := op["security"].([]any)
	return sec
}

func securityDeclaresScheme(security []any, scheme string) bool {
	for _, item := range security {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := m[scheme]; ok {
			return true
		}
	}
	return false
}

type openAPIOperation struct {
	ID       string
	Method   string
	Path     string
	Security []any
}

func listOpenAPIOperations(t *testing.T, openAPIDoc map[string]any) []openAPIOperation {
	t.Helper()

	paths, ok := openAPIDoc["paths"].(map[string]any)
	require.True(t, ok, "openapi doc missing paths")

	methods := []string{"get", "post", "put", "delete", "patch"}
	var ops []openAPIOperation

	for p, rawPathItem := range paths {
		pathItem, ok := rawPathItem.(map[string]any)
		if !ok {
			continue
		}
		for _, m := range methods {
			rawOp, ok := pathItem[m]
			if !ok {
				continue
			}
			op, ok := rawOp.(map[string]any)
			if !ok {
				continue
			}
			id, ok := op["operationId"].(string)
			if !ok || id == "" {
				continue
			}
			sec, _ := op["security"].([]any)
			ops = append(ops, openAPIOperation{ID: id, Method: strings.ToUpper(m), Path: p, Security: sec})
		}
	}

	return ops
}

type actorCreds struct {
	Cookie *http.Cookie
	Bearer string
}

func createUserAndSession(t *testing.T, server *Server, email, name string, isAdmin bool) string {
	t.Helper()
	require.NotNil(t, server.sessionManager)

	ctx := context.Background()
	queries := server.db.GetQueries()

	user, err := queries.GetUserByEmail(ctx, email)
	if err != nil {
		adminFlag := int64(0)
		if isAdmin {
			adminFlag = 1
		}
		user, err = queries.CreateUser(ctx, sqlc.CreateUserParams{
			ID:         uuid.New().String(),
			Email:      email,
			Name:       name,
			Provider:   "fake",
			ProviderID: uuid.New().String(),
			IsAdmin:    sql.NullInt64{Int64: adminFlag, Valid: true},
			IsActive:   sql.NullInt64{Int64: 1, Valid: true},
		})
		require.NoError(t, err)
	}

	sessionToken, err := server.sessionManager.CreateSession(ctx, user.ID)
	require.NoError(t, err)
	return sessionToken
}

func generateBearerToken(t *testing.T, server *Server, sessionCookie *http.Cookie, name string) string {
	t.Helper()

	w := doJSONRequest(t, server, http.MethodPost, "/api/v1/tokens/generate", map[string]any{"name": name}, nil, []*http.Cookie{sessionCookie})
	require.Equal(t, http.StatusOK, w.Code)

	var tokenResp struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tokenResp))
	require.NotEmpty(t, tokenResp.Token)
	return tokenResp.Token
}

type requestSpec struct {
	Method        string
	Path          string
	Body          any
	RequiresAdmin bool
}

func TestAPIAuth_AllOperations(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Fetch OpenAPI.
	w := doJSONRequest(t, server, http.MethodGet, "/openapi.json", nil, nil, nil)
	require.Equal(t, http.StatusOK, w.Code)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))

	// Create credentials.
	adminSession := createUserAndSession(t, server, "admin@example.com", "Admin User", true)
	userSession := createUserAndSession(t, server, "user@example.com", "Regular User", false)

	adminCookie := &http.Cookie{Name: "session_token", Value: adminSession, Path: "/"}
	userCookie := &http.Cookie{Name: "session_token", Value: userSession, Path: "/"}

	adminBearer := generateBearerToken(t, server, adminCookie, "admin-bearer")
	userBearer := generateBearerToken(t, server, userCookie, "user-bearer")

	admin := actorCreds{Cookie: adminCookie, Bearer: adminBearer}
	user := actorCreds{Cookie: userCookie, Bearer: userBearer}

	// Seed data so operations can succeed past auth checks.
	ctx := context.Background()
	memberA, err := server.db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = server.db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = server.db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	memberToUpdate, err := server.db.AddTeamMember(ctx, "Update Me", "update@example.com")
	require.NoError(t, err)
	memberToDelete, err := server.db.AddTeamMember(ctx, "Delete Me", "delete@example.com")
	require.NoError(t, err)

	// Generate schedule so leave/ICS endpoints have data.
	require.NoError(t, server.engine.GenerateSchedule(
		ctx,
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
	))

	leaveToUpdate, err := server.db.CreateLeaveRecord(ctx, memberA, "2024-01-17", "2024-01-17")
	require.NoError(t, err)
	leaveToDelete, err := server.db.CreateLeaveRecord(ctx, memberA, "2024-01-18", "2024-01-18")
	require.NoError(t, err)

	calToken, err := server.db.CreateCalendarSubscription(ctx, memberA)
	require.NoError(t, err)

	// Token ID for revoke-api-token (session-auth only). Create then read from DB.
	_ = generateBearerToken(t, server, userCookie, "revoke-me")
	userRow, err := server.db.GetQueries().GetUserByEmail(ctx, "user@example.com")
	require.NoError(t, err)
	tokens, err := server.db.GetQueries().GetAPITokensByUser(ctx, userRow.ID)
	require.NoError(t, err)
	require.NotEmpty(t, tokens)
	revokeTokenID := tokens[0].ID

	builders := map[string]func() requestSpec{
		"add-team-member": func() requestSpec {
			return requestSpec{Method: http.MethodPost, Path: "/api/v1/team", Body: map[string]any{"name": "New Person", "email": "new@example.com"}, RequiresAdmin: true}
		},
		"list-team-members": func() requestSpec {
			return requestSpec{Method: http.MethodGet, Path: "/api/v1/team"}
		},
		"update-team-member": func() requestSpec {
			return requestSpec{Method: http.MethodPut, Path: "/api/v1/team/" + memberToUpdate, Body: map[string]any{"name": "Updated", "email": "updated@example.com"}, RequiresAdmin: true}
		},
		"delete-team-member": func() requestSpec {
			return requestSpec{Method: http.MethodDelete, Path: "/api/v1/team/" + memberToDelete, RequiresAdmin: true}
		},
		"report-leave": func() requestSpec {
			return requestSpec{Method: http.MethodPost, Path: "/api/v1/leave", Body: map[string]any{"member_id": memberA, "start_date": "2024-01-17", "end_date": "2024-01-17"}}
		},
		"list-leave": func() requestSpec {
			return requestSpec{Method: http.MethodGet, Path: "/api/v1/leave"}
		},
		"update-leave": func() requestSpec {
			return requestSpec{Method: http.MethodPut, Path: "/api/v1/leave/" + leaveToUpdate, Body: map[string]any{"member_id": memberA, "start_date": "2024-01-17", "end_date": "2024-01-17", "status": "assigned"}, RequiresAdmin: true}
		},
		"delete-leave": func() requestSpec {
			return requestSpec{Method: http.MethodDelete, Path: "/api/v1/leave/" + leaveToDelete, RequiresAdmin: true}
		},
		"generate-schedule": func() requestSpec {
			return requestSpec{Method: http.MethodPost, Path: "/api/v1/schedule/generate", Body: map[string]any{"start_date": "2024-02-01", "end_date": "2024-02-07"}, RequiresAdmin: true}
		},
		"subscribe-calendar": func() requestSpec {
			return requestSpec{Method: http.MethodPost, Path: "/api/v1/calendar/subscribe", Body: map[string]any{"member_id": memberA}}
		},
		"get-holidays": func() requestSpec {
			return requestSpec{Method: http.MethodGet, Path: "/api/v1/holidays"}
		},
		"get-holiday-status": func() requestSpec {
			return requestSpec{Method: http.MethodGet, Path: "/api/v1/holidays/status"}
		},
		"refresh-holidays": func() requestSpec {
			return requestSpec{Method: http.MethodPost, Path: "/api/v1/holidays/refresh", RequiresAdmin: true}
		},
		"generate-api-token": func() requestSpec {
			return requestSpec{Method: http.MethodPost, Path: "/api/v1/tokens/generate", Body: map[string]any{"name": "extra"}}
		},
		"list-api-tokens": func() requestSpec {
			return requestSpec{Method: http.MethodGet, Path: "/api/v1/tokens"}
		},
		"revoke-api-token": func() requestSpec {
			return requestSpec{Method: http.MethodDelete, Path: "/api/v1/tokens/" + revokeTokenID}
		},
		"cleanup-expired-tokens": func() requestSpec {
			return requestSpec{Method: http.MethodPost, Path: "/api/v1/tokens/cleanup", RequiresAdmin: true}
		},
	}

	ops := listOpenAPIOperations(t, doc)
	var apiOps []openAPIOperation
	for _, op := range ops {
		if strings.HasPrefix(op.Path, "/api/v1/") {
			apiOps = append(apiOps, op)
		}
	}
	require.NotEmpty(t, apiOps, "expected OpenAPI to include /api/v1 operations")

	// Ensure every /api/v1 OpenAPI operation has a corresponding request builder.
	for _, op := range apiOps {
		_, ok := builders[op.ID]
		require.True(t, ok, "missing request builder for operationId %q (%s %s)", op.ID, op.Method, op.Path)
	}

	for _, op := range apiOps {
		op := op
		spec := builders[op.ID]()

		hasSecurity := len(op.Security) > 0
		hasBearer := securityDeclaresScheme(op.Security, "apiTokenAuth")
		hasSession := securityDeclaresScheme(op.Security, "sessionAuth")

		t.Run(op.ID, func(t *testing.T) {
			// 1) Unauthenticated behavior.
			w := doJSONRequest(t, server, spec.Method, spec.Path, spec.Body, nil, nil)
			if hasSecurity {
				assert.Equal(t, http.StatusUnauthorized, w.Code, "%s %s should be 401 when unauthenticated", spec.Method, spec.Path)
			} else {
				assert.NotEqual(t, http.StatusUnauthorized, w.Code, "%s %s should not require auth", spec.Method, spec.Path)
			}

			// 2) Authenticated behavior.
			if !hasSecurity {
				return
			}

			if spec.RequiresAdmin {
				// Non-admin should be forbidden.
				w = doJSONRequest(t, server, spec.Method, spec.Path, spec.Body, nil, []*http.Cookie{user.Cookie})
				assert.Equal(t, http.StatusForbidden, w.Code)

				if hasBearer {
					w = doJSONRequest(t, server, spec.Method, spec.Path, spec.Body, map[string]string{"Authorization": "Bearer " + user.Bearer}, nil)
					assert.Equal(t, http.StatusForbidden, w.Code)
				}

				// Admin should not get auth failures.
				if hasSession {
					w = doJSONRequest(t, server, spec.Method, spec.Path, spec.Body, nil, []*http.Cookie{admin.Cookie})
					assert.NotEqual(t, http.StatusUnauthorized, w.Code)
					assert.NotEqual(t, http.StatusForbidden, w.Code)
				}
				if hasBearer {
					w = doJSONRequest(t, server, spec.Method, spec.Path, spec.Body, map[string]string{"Authorization": "Bearer " + admin.Bearer}, nil)
					assert.NotEqual(t, http.StatusUnauthorized, w.Code)
					assert.NotEqual(t, http.StatusForbidden, w.Code)
				}
				return
			}

			// Non-admin authenticated user should not get 401.
			if hasSession {
				w = doJSONRequest(t, server, spec.Method, spec.Path, spec.Body, nil, []*http.Cookie{user.Cookie})
				assert.NotEqual(t, http.StatusUnauthorized, w.Code)
			}
			if hasBearer {
				w = doJSONRequest(t, server, spec.Method, spec.Path, spec.Body, map[string]string{"Authorization": "Bearer " + user.Bearer}, nil)
				assert.NotEqual(t, http.StatusUnauthorized, w.Code)
			}
		})
	}

	// Non-Huma token-based calendar ICS feed should remain public (token in URL).
	t.Run("calendar-ics-public", func(t *testing.T) {
		w := doJSONRequest(t, server, http.MethodGet, "/api/v1/calendar/"+calToken+"/ics", nil, nil, nil)
		assert.NotEqual(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestAPIAuthDocsVsRuntime_BearerTokenAccepted(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// 1) OpenAPI declares Bearer auth for team endpoints.
	w := doJSONRequest(t, server, http.MethodGet, "/openapi.json", nil, nil, nil)
	require.Equal(t, http.StatusOK, w.Code)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))

	sec := findOperationSecurity(t, doc, "/api/v1/team", "get")
	require.NotEmpty(t, sec, "expected /api/v1/team GET to declare security")
	assert.True(t, securityDeclaresScheme(sec, "sessionAuth"), "expected sessionAuth in OpenAPI security")
	assert.True(t, securityDeclaresScheme(sec, "apiTokenAuth"), "expected apiTokenAuth in OpenAPI security")

	// 2) Without auth, endpoint is denied.
	w = doJSONRequest(t, server, http.MethodGet, "/api/v1/team", nil, nil, nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// 3) With a valid session cookie, endpoint is allowed.
	ctx := context.Background()
	sessionToken, err := server.createTestSession(ctx)
	require.NoError(t, err)

	sessionCookie := &http.Cookie{Name: "session_token", Value: sessionToken, Path: "/"}
	w = doJSONRequest(t, server, http.MethodGet, "/api/v1/team", nil, nil, []*http.Cookie{sessionCookie})
	assert.Equal(t, http.StatusOK, w.Code)

	// 4) Generate a real API token (requires session).
	w = doJSONRequest(t, server, http.MethodPost, "/api/v1/tokens/generate", map[string]any{"name": "test"}, nil, []*http.Cookie{sessionCookie})
	require.Equal(t, http.StatusOK, w.Code)

	var tokenResp struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tokenResp))
	require.NotEmpty(t, tokenResp.Token)

	// 5) Using that token as Bearer auth authenticates requests.
	w = doJSONRequest(t, server, http.MethodGet, "/api/v1/team", nil, map[string]string{"Authorization": "Bearer " + tokenResp.Token}, nil)
	assert.Equal(t, http.StatusOK, w.Code)
}
