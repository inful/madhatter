package auth

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFakeCallbackHandler_HandleLogin_SecureOnTLS pins the
// security review finding #13: when the request reached the
// fake-callback handler over TLS, the oauth_state cookie must
// carry Secure=true so a network attacker can't capture it on
// a non-HTTPS hop. The fake provider is dev-only, but if an
// operator ever ships --development to production behind a TLS
// proxy, the cookie must still be Secure.
func TestFakeCallbackHandler_HandleLogin_SecureOnTLS(t *testing.T) {
	handler := NewFakeCallbackHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/fake/login", nil)
	req.TLS = &tls.ConnectionState{} // simulate a direct-TLS request
	w := httptest.NewRecorder()

	handler.HandleLogin(w, req)

	cookie := findCookie(w, "oauth_state")
	require.NotNil(t, cookie, "oauth_state cookie should be set on the fake-login response")
	assert.True(t, cookie.Secure,
		"the fake-login oauth_state cookie must be Secure when the request reached the server over TLS")
}

// TestFakeCallbackHandler_HandleLogin_SecureBehindTLSProxy pins
// the X-Forwarded-Proto branch: when the request reached the
// server over plain HTTP but a TLS-terminating reverse proxy set
// X-Forwarded-Proto=https, the cookie must also be Secure. This
// mirrors the production AuthManager.HandleLogin behavior so
// the fake provider matches it.
func TestFakeCallbackHandler_HandleLogin_SecureBehindTLSProxy(t *testing.T) {
	handler := NewFakeCallbackHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/fake/login", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()

	handler.HandleLogin(w, req)

	cookie := findCookie(w, "oauth_state")
	require.NotNil(t, cookie, "oauth_state cookie should be set when behind a TLS proxy")
	assert.True(t, cookie.Secure,
		"the fake-login oauth_state cookie must be Secure when X-Forwarded-Proto=https")
}

// TestFakeCallbackHandler_HandleLogin_NotSecureOnPlainHTTP
// preserves the existing dev-friendly default: when the
// request reached the server over plain HTTP (no TLS, no
// X-Forwarded-Proto), the cookie must NOT carry Secure=true
// because the browser would refuse to set it at all and the
// local dev login would break.
func TestFakeCallbackHandler_HandleLogin_NotSecureOnPlainHTTP(t *testing.T) {
	handler := NewFakeCallbackHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/fake/login", nil)
	// No req.TLS, no X-Forwarded-Proto — the dev default.
	w := httptest.NewRecorder()

	handler.HandleLogin(w, req)

	cookie := findCookie(w, "oauth_state")
	require.NotNil(t, cookie)
	assert.False(t, cookie.Secure,
		"the fake-login oauth_state cookie must NOT be Secure over plain HTTP (dev mode default)")
}

// findCookie is a small helper that returns the named cookie
// from the response, or nil if absent. Keeps the three tests
// above focused on the Secure-attribute pin rather than the
// cookie-walking boilerplate.
func findCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}
