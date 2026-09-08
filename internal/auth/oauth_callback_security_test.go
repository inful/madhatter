package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/inful/madhatter/internal/database/sqlc"
)

// errorProvider is a stub Provider that returns the given errors
// from ExchangeCode and GetUserInfo. Used to drive the OAuth
// callback down the "upstream failure" branches without standing
// up an actual OAuth server.
type errorProvider struct {
	name        string
	exchangeErr error
	userInfoErr error
}

func (p *errorProvider) Name() string { return p.name }

func (p *errorProvider) GetAuthURL(string) string { return "" }

func (p *errorProvider) ExchangeCode(_ context.Context, _ string) (*oauth2.Token, error) {
	return nil, p.exchangeErr
}

func (p *errorProvider) GetUserInfo(_ context.Context, _ *oauth2.Token) (*UserInfo, error) {
	return nil, p.userInfoErr
}

func (p *errorProvider) GetOAuthConfig() *oauth2.Config { return &oauth2.Config{} }

// errorProviderSetup wires an AuthManager that uses the supplied
// stub provider. Mirrors the boilerplate in handlers_test.go but
// parameterised over the provider so each test can inject its
// own failure mode.
func errorProviderSetup(t *testing.T, db *sqlc.Queries, p Provider) *AuthManager {
	t.Helper()
	encryptor, err := NewTokenEncryptor(false)
	require.NoError(t, err)

	providerFactory := NewProviderFactory(map[string]ProviderConfig{
		p.Name(): {},
	})
	userService := NewUserService(db, encryptor)
	sessionManager := NewSessionManager(db, 24*1e9) // 24h
	authManager := NewAuthManager(providerFactory, userService, sessionManager)
	authManager.RegisterProvider(p)
	return authManager
}

// TestHandleCallback_ExchangeCodeFailureHidesUpstreamError pins
// the security review finding #6: when the OAuth provider's
// ExchangeCode call fails, the callback handler must NOT echo
// the upstream error message back to the client. The full error
// belongs in the server log; the client should see a generic
// "authentication failed" message that gives an attacker no
// useful information about the upstream's state.
func TestHandleCallback_ExchangeCodeFailureHidesUpstreamError(t *testing.T) {
	db := newAuthTestDB(t)
	const upstreamDetail = "upstream confidential detail: api.forgejo.example.com returned 503 Service Unavailable"
	p := &errorProvider{
		name:        "forgejo",
		exchangeErr: errors.New(upstreamDetail),
	}
	authManager := errorProviderSetup(t, db, p)

	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/auth/callback?code=test-code&state=test-state&provider=forgejo",
		nil,
	)
	//nolint:gosec // G124 false positive: cookie attributes are explicit (test fixture, not a real credential).
	req.AddCookie(&http.Cookie{
		Name: "oauth_state", Value: "test-state", Path: "/",
		HttpOnly: true, Secure: false, SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()

	authManager.HandleCallback(w, req)

	body := w.Body.String()
	assert.NotContains(t, body, upstreamDetail,
		"the upstream error message must not be reflected to the client")
	assert.NotContains(t, body, "api.forgejo.example.com",
		"upstream URLs must not leak to the client")
	assert.NotContains(t, body, "503",
		"upstream HTTP status codes must not leak to the client")
	// Status code should still indicate failure (4xx/5xx), not 200.
	assert.GreaterOrEqual(t, w.Code, 400, "callback failure should return an error status")
}

// TestHandleCallback_UserInfoFailureHidesUpstreamError is the
// sibling test for the GetUserInfo branch.
func TestHandleCallback_UserInfoFailureHidesUpstreamError(t *testing.T) {
	db := newAuthTestDB(t)
	const upstreamDetail = "upstream confidential detail: /api/v1/user returned 401 invalid_grant"
	// ExchangeCode succeeds (returns a fake token), GetUserInfo fails.
	p := &errorProvider{
		name:        "forgejo",
		userInfoErr: errors.New(upstreamDetail),
	}
	authManager := errorProviderSetup(t, db, p)

	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/auth/callback?code=test-code&state=test-state&provider=forgejo",
		nil,
	)
	//nolint:gosec // G124 false positive: cookie attributes are explicit (test fixture, not a real credential).
	req.AddCookie(&http.Cookie{
		Name: "oauth_state", Value: "test-state", Path: "/",
		HttpOnly: true, Secure: false, SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()

	authManager.HandleCallback(w, req)

	body := w.Body.String()
	assert.NotContains(t, body, upstreamDetail,
		"the upstream userinfo error message must not be reflected to the client")
	assert.NotContains(t, body, "invalid_grant",
		"upstream error codes must not leak to the client")
	assert.NotContains(t, body, "/api/v1/user",
		"upstream API paths must not leak to the client")
	assert.GreaterOrEqual(t, w.Code, 400, "callback failure should return an error status")
}

// TestHandleCallback_ExchangeCodeFailure_LogsError is a
// secondary pin: the upstream error must still be observable in
// the server log so an operator can diagnose the failure. We
// capture slog output into a buffer and assert the upstream
// detail appears there (so we know it's logged) and DOES NOT
// appear in the response body (so we know it's not leaked).
func TestHandleCallback_ExchangeCodeFailure_LogsError(t *testing.T) {
	db := newAuthTestDB(t)
	const upstreamDetail = "upstream confidential detail: api.forgejo.example.com timeout after 30s"
	p := &errorProvider{
		name:        "forgejo",
		exchangeErr: errors.New(upstreamDetail),
	}
	authManager := errorProviderSetup(t, db, p)

	// Capture slog output for the duration of the test.
	var logBuf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/auth/callback?code=test-code&state=test-state&provider=forgejo",
		nil,
	)
	//nolint:gosec // G124 false positive: cookie attributes are explicit (test fixture, not a real credential).
	req.AddCookie(&http.Cookie{
		Name: "oauth_state", Value: "test-state", Path: "/",
		HttpOnly: true, Secure: false, SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()

	authManager.HandleCallback(w, req)

	logged := logBuf.String()
	assert.Contains(t, logged, upstreamDetail,
		"the upstream error detail must be captured in the server log so operators can diagnose")
}

// newAuthTestDB opens a temp-dir-backed auth-test database. The
// callback handler needs the users + team_members tables; the
// shared test util in handlers_test.go is enough.
func newAuthTestDB(t *testing.T) *sqlc.Queries {
	t.Helper()
	d := setupTestDB(t)
	return d.GetQueries()
}
