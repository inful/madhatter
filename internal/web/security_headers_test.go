package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSecurityHeadersMiddleware_PresentOnEveryResponse asserts the
// middleware sets the documented defensive headers on every request
// regardless of the inner handler's status code or body shape.
func TestSecurityHeadersMiddleware_PresentOnEveryResponse(t *testing.T) {
	cases := []struct {
		name       string
		handler    http.Handler
		wantStatus int
	}{
		{
			name: "200",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}),
			wantStatus: http.StatusOK,
		},
		{
			name: "404",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			}),
			wantStatus: http.StatusNotFound,
		},
		{
			name: "500",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "Redirect",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/x", http.StatusSeeOther)
			}),
			wantStatus: http.StatusSeeOther,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mw := securityHeadersMiddleware(tc.handler)
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)

			assert.Equal(t, tc.wantStatus, rec.Code)
			h := rec.Header()
			assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"))
			assert.Equal(t, "DENY", h.Get("X-Frame-Options"))
			assert.Equal(t, "same-origin", h.Get("Referrer-Policy"))
			csp := h.Get("Content-Security-Policy")
			assert.Contains(t, csp, "default-src 'self'")
			assert.Contains(t, csp, "frame-ancestors 'none'")
			assert.Contains(t, csp, "form-action 'self'")
			assert.Contains(t, csp, "base-uri 'self'")
		})
	}
}

// TestSecurityHeadersMiddleware_PassesThroughHandlerBehaviour
// verifies the middleware does not interfere with the wrapped
// handler's status code, headers, or body.
func TestSecurityHeadersMiddleware_PassesThroughHandlerBehaviour(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Handler", "yes")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("i am a teapot"))
	})
	mw := securityHeadersMiddleware(handler)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusTeapot, rec.Code)
	assert.Equal(t, "yes", rec.Header().Get("X-Custom-Handler"))
	assert.Equal(t, "i am a teapot", rec.Body.String())
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
}

// TestSecurityHeadersMiddleware_HSTSOnlyOnHTTPS asserts the HSTS
// header is set only when the request reaches us over TLS.
func TestSecurityHeadersMiddleware_HSTSOnlyOnHTTPS(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	mw := securityHeadersMiddleware(handler)

	t.Run("DirectTLS", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/", nil)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		assert.Equal(t, "max-age=63072000; includeSubDomains", rec.Header().Get("Strict-Transport-Security"))
	})

	t.Run("PlainHTTP", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/", nil)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		assert.Empty(t, rec.Header().Get("Strict-Transport-Security"))
	})

	t.Run("ProxyForwardedHTTPSWithForwardedHeader", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("Forwarded", "for=1.2.3.4;proto=https;host=example.com")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		assert.Equal(t, "max-age=63072000; includeSubDomains", rec.Header().Get("Strict-Transport-Security"))
	})

	t.Run("SpoofedProtoAloneIgnored", func(t *testing.T) {
		// A direct client that sends X-Forwarded-Proto without any
		// corresponding Forwarded/X-Forwarded-For is treated as a
		// spoof attempt and HSTS is not set.
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		assert.Empty(t, rec.Header().Get("Strict-Transport-Security"))
	})
}

// TestSecurityHeadersMiddleware_NoHSTSLeakOnHTTP confirms a header
// value of "0" or empty is not produced for HSTS on plain HTTP —
// the failure mode the check guards against is "set when it should
// not be", not "missing when it should be".
func TestSecurityHeadersMiddleware_NoHSTSLeakOnHTTP(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	mw := securityHeadersMiddleware(handler)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	v := rec.Header().Get("Strict-Transport-Security")
	require.False(t, strings.HasPrefix(v, "max-age="),
		"HSTS leaked over plain HTTP: %q", v)
}

// TestSecurityHeadersMiddleware_NoCDNHostsInCSP locks in the
// stricter CSP that comes with the vendored assets. The
// third-party CSS / JS / font files (HTMX, Bulma, FontAwesome)
// all live under /static/ now, so the CSP should NOT list any
// external CDN hosts. A future change that adds an external
// <script> or <link> to the base template needs to vendor the
// asset first; this test will fail and force that conversation.
func TestSecurityHeadersMiddleware_NoCDNHostsInCSP(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	mw := securityHeadersMiddleware(handler)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	require.NotEmpty(t, csp, "CSP must be set")

	// 'self' is the only allowed source. Anything else is a bug:
	// either the asset was added without being vendored, or a CDN
	// URL leaked in via copy-paste.
	require.Contains(t, csp, "default-src 'self'",
		"CSP must keep the strict 'self' default")
	require.Contains(t, csp, "style-src 'self'",
		"style-src must allow same-origin styles")
	require.Contains(t, csp, "script-src 'self'",
		"script-src must allow same-origin scripts")
	require.Contains(t, csp, "font-src 'self'",
		"font-src must allow same-origin fonts (for /static/fontawesome/webfonts)")

	for _, host := range []string{
		"https://cdn.jsdelivr.net",
		"https://cdnjs.cloudflare.com",
		"https://unpkg.com",
	} {
		assert.NotContains(t, csp, host,
			"CSP must not list %s — assets should be vendored under /static/", host)
	}
}
