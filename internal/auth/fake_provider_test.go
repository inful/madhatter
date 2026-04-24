package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// TestFakeProvider_Name tests that the fake provider returns the correct name.
func TestFakeProvider_Name(t *testing.T) {
	//nolint:gosec // Test-only OAuth configuration values.
	config := ProviderConfig{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost:8080/auth/callback",
		AuthURL:      "/auth/fake/login",
		TokenURL:     "/auth/fake/token",
		UserInfoURL:  "/auth/fake/userinfo",
		Scope:        "read:user",
	}

	provider := NewFakeProvider(config)
	assert.Equal(t, "fake", provider.Name())
}

// TestFakeProvider_GetAuthURL tests that the fake provider returns a valid auth URL.
func TestFakeProvider_GetAuthURL(t *testing.T) {
	//nolint:gosec // Test-only OAuth configuration values.
	config := ProviderConfig{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost:8080/auth/callback",
		AuthURL:      "/auth/fake/login",
		TokenURL:     "/auth/fake/token",
		UserInfoURL:  "/auth/fake/userinfo",
		Scope:        "read:user",
	}

	provider := NewFakeProvider(config)
	state := "test-state-123"
	authURL := provider.GetAuthURL(state)

	// Should contain the state parameter
	assert.Contains(t, authURL, "state="+url.QueryEscape(state))
	// Should be the fake login endpoint
	assert.Contains(t, authURL, "/auth/fake/login")
}

// TestFakeProvider_ExchangeCode tests that the fake provider returns fake tokens.
func TestFakeProvider_ExchangeCode(t *testing.T) {
	//nolint:gosec // Test-only OAuth configuration values.
	config := ProviderConfig{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost:8080/auth/callback",
		AuthURL:      "/auth/fake/login",
		TokenURL:     "/auth/fake/token",
		UserInfoURL:  "/auth/fake/userinfo",
		Scope:        "read:user",
	}

	provider := NewFakeProvider(config)
	ctx := context.Background()
	token, err := provider.ExchangeCode(ctx, "fake-code-123")

	require.NoError(t, err)
	assert.NotNil(t, token)
	assert.Contains(t, token.AccessToken, "fake-access-token-")
	assert.Equal(t, "Bearer", token.TokenType)
	assert.True(t, token.Expiry.After(time.Now()))
}

// TestFakeProvider_GetUserInfo tests that the fake provider returns fake user info.
func TestFakeProvider_GetUserInfo(t *testing.T) {
	//nolint:gosec // Test-only OAuth configuration values.
	config := ProviderConfig{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost:8080/auth/callback",
		AuthURL:      "/auth/fake/login",
		TokenURL:     "/auth/fake/token",
		UserInfoURL:  "/auth/fake/userinfo",
		Scope:        "read:user",
	}

	provider := NewFakeProvider(config)
	ctx := context.Background()
	token := &oauth2.Token{
		AccessToken: "fake-token",
	}

	userInfo, err := provider.GetUserInfo(ctx, token)

	require.NoError(t, err)
	assert.NotNil(t, userInfo)
	assert.Equal(t, "dev@example.com", userInfo.Email)
	assert.Equal(t, "Development User", userInfo.Name)
	assert.Equal(t, "dev-user-1", userInfo.ID)
	assert.Equal(t, "devuser", userInfo.Username)
}

// TestFakeProvider_GetOAuthConfig tests that the fake provider returns valid OAuth config.
func TestFakeProvider_GetOAuthConfig(t *testing.T) {
	//nolint:gosec // Test-only OAuth configuration values.
	config := ProviderConfig{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost:8080/auth/callback",
		AuthURL:      "/auth/fake/login",
		TokenURL:     "/auth/fake/token",
		UserInfoURL:  "/auth/fake/userinfo",
		Scope:        "read:user",
	}

	provider := NewFakeProvider(config)
	oauthConfig := provider.GetOAuthConfig()

	assert.NotNil(t, oauthConfig)
	assert.Equal(t, "test-client", oauthConfig.ClientID)
	assert.Equal(t, "test-secret", oauthConfig.ClientSecret)
	assert.Equal(t, "http://localhost:8080/auth/callback", oauthConfig.RedirectURL)
	assert.Equal(t, "/auth/fake/login", oauthConfig.Endpoint.AuthURL)
	assert.Equal(t, "/auth/fake/token", oauthConfig.Endpoint.TokenURL)
	assert.Contains(t, oauthConfig.Scopes, "read:user")
}

// TestFakeCallbackHandler_HandleLogin tests the fake login flow.
func TestFakeCallbackHandler_HandleLogin(t *testing.T) {
	handler := NewFakeCallbackHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/fake/login?state=test-state", nil)
	w := httptest.NewRecorder()

	handler.HandleLogin(w, req)

	// Should redirect to callback
	assert.Equal(t, http.StatusSeeOther, w.Code)

	// Should set oauth_state cookie
	cookies := w.Result().Cookies()
	var stateCookie *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name == "oauth_state" {
			stateCookie = cookie
			break
		}
	}
	require.NotNil(t, stateCookie, "oauth_state cookie should be set")
	assert.Equal(t, "/auth/callback", stateCookie.Path)
	assert.Equal(t, StateCookieExpiry, stateCookie.MaxAge)

	// Should redirect to callback URL with fake code and state
	location := w.Header().Get("Location")
	assert.Contains(t, location, "/auth/callback")
	assert.Contains(t, location, "code=fake-code-")
	assert.Contains(t, location, "state=")
	assert.Contains(t, location, "provider=fake")
}

// TestFakeCallbackHandler_HandleLogin_UniqueStates tests that each login generates unique states.
func TestFakeCallbackHandler_HandleLogin_UniqueStates(t *testing.T) {
	handler := NewFakeCallbackHandler()

	// Make two requests
	req1 := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/fake/login", nil)
	w1 := httptest.NewRecorder()
	handler.HandleLogin(w1, req1)

	req2 := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/fake/login", nil)
	w2 := httptest.NewRecorder()
	handler.HandleLogin(w2, req2)

	// Extract states from redirect URLs
	location1 := w1.Header().Get("Location")
	location2 := w2.Header().Get("Location")

	// Parse URLs to get state parameter
	u1, _ := url.Parse(location1)
	u2, _ := url.Parse(location2)

	state1 := u1.Query().Get("state")
	state2 := u2.Query().Get("state")

	// States should be different (unique)
	assert.NotEqual(t, state1, state2)
	// Both should have the dev-state prefix
	assert.True(t, strings.HasPrefix(state1, "dev-state-"))
	assert.True(t, strings.HasPrefix(state2, "dev-state-"))
}

// TestGetDevelopmentLoginHTML tests that the shared HTML is valid.
func TestGetDevelopmentLoginHTML(t *testing.T) {
	html := GetDevelopmentLoginHTML()

	// Should contain key elements
	assert.Contains(t, html, "<!DOCTYPE html>")
	assert.Contains(t, html, "Development Mode")
	assert.Contains(t, html, "Fake OAuth Authentication")
	assert.Contains(t, html, "dev@example.com")
	assert.Contains(t, html, "/auth/fake/login")
	assert.Contains(t, html, "bulma")
	assert.Contains(t, html, "fa-flask")

	// Should be valid HTML structure
	assert.Contains(t, html, "<html>")
	assert.Contains(t, html, "<head>")
	assert.Contains(t, html, "<body>")
	assert.Contains(t, html, "</html>")
}

// TestFakeProvider_Integration tests the complete fake OAuth flow.
func TestFakeProvider_Integration(t *testing.T) {
	// Setup
	//nolint:gosec // Test-only OAuth configuration values.
	config := ProviderConfig{
		ClientID:     "dev-client",
		ClientSecret: "dev-secret",
		RedirectURL:  "http://localhost:8080/auth/callback",
		AuthURL:      "/auth/fake/login",
		TokenURL:     "/auth/fake/token",
		UserInfoURL:  "/auth/fake/userinfo",
		Scope:        "read:user",
	}

	provider := NewFakeProvider(config)
	handler := NewFakeCallbackHandler()

	// Step 1: Get auth URL
	state := "test-state-123"
	authURL := provider.GetAuthURL(state)
	assert.Contains(t, authURL, "/auth/fake/login")

	// Step 2: Simulate login redirect
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, authURL, nil)
	w := httptest.NewRecorder()
	handler.HandleLogin(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)

	// Step 3: Extract callback URL
	callbackURL := w.Header().Get("Location")
	assert.Contains(t, callbackURL, "/auth/callback")

	// Parse callback URL to extract code and state
	u, _ := url.Parse(callbackURL)
	code := u.Query().Get("code")
	stateFromCallback := u.Query().Get("state")
	providerParam := u.Query().Get("provider")

	assert.NotEmpty(t, code)
	assert.NotEmpty(t, stateFromCallback)
	assert.Equal(t, "fake", providerParam)

	// Step 4: Exchange code for token
	ctx := context.Background()
	token, err := provider.ExchangeCode(ctx, code)
	require.NoError(t, err)
	assert.NotNil(t, token)
	assert.Contains(t, token.AccessToken, "fake-access-token-")

	// Step 5: Get user info
	userInfo, err := provider.GetUserInfo(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, "dev@example.com", userInfo.Email)
	assert.Equal(t, "Development User", userInfo.Name)
}

// TestDevelopmentLoginHTML_ContainsSecurityWarnings tests the HTML has proper warnings.
func TestDevelopmentLoginHTML_ContainsSecurityWarnings(t *testing.T) {
	html := GetDevelopmentLoginHTML()

	// Should contain security warnings
	assert.Contains(t, html, "DEVELOPMENT MODE ENABLED")
	assert.Contains(t, html, "DO NOT use this in production")
	assert.Contains(t, html, "exclamation-triangle")

	// Should explain how it works
	assert.Contains(t, html, "How it works")
	assert.Contains(t, html, "fake user")
	assert.Contains(t, html, "automatically becomes an admin")
}

// TestDevelopmentLoginHTML_UsesBulma tests the HTML uses Bulma framework.
func TestDevelopmentLoginHTML_UsesBulma(t *testing.T) {
	html := GetDevelopmentLoginHTML()

	// Should include Bulma CSS
	assert.Contains(t, html, "bulma")
	assert.Contains(t, html, "cdn.jsdelivr.net/npm/bulma")

	// Should use Bulma classes
	assert.Contains(t, html, "hero")
	assert.Contains(t, html, "container")
	assert.Contains(t, html, "title")
	assert.Contains(t, html, "button")
	assert.Contains(t, html, "is-primary")
	assert.Contains(t, html, "is-large")
}

// TestDevelopmentLoginHTML_UsesFontAwesome tests the HTML uses Font Awesome.
func TestDevelopmentLoginHTML_UsesFontAwesome(t *testing.T) {
	html := GetDevelopmentLoginHTML()

	// Should include Font Awesome
	assert.Contains(t, html, "font-awesome")
	assert.Contains(t, html, "cdnjs.cloudflare.com")
	assert.Contains(t, html, "fa-")
}

// TestDevelopmentLoginHTML_FormAction tests the form action is correct.
func TestDevelopmentLoginHTML_FormAction(t *testing.T) {
	html := GetDevelopmentLoginHTML()

	// Form should POST to the fake login endpoint
	assert.Contains(t, html, `action="/auth/fake/login"`)
	assert.Contains(t, html, `method="GET"`)
	assert.Contains(t, html, `type="submit"`)
	assert.Contains(t, html, "Login as Development User")
}

// TestDevelopmentLoginHTML_BackLink tests the back link is present.
func TestDevelopmentLoginHTML_BackLink(t *testing.T) {
	html := GetDevelopmentLoginHTML()

	// Should have a back to home link
	assert.Contains(t, html, `href="/"`)
	assert.Contains(t, html, "Back to Home")
}
