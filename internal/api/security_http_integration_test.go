package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
