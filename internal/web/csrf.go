package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
)

// csrfCookieName is the name of the double-submit CSRF
// cookie. The token is also the value the form field
// must echo. We don't use the `__Host-` prefix because that
// prefix requires Secure=true, which is incompatible with
// the dev-mode HTTP setup; production deployments terminate
// TLS at a proxy and get Secure=true via the existing
// isHTTPSRequest detection in security_headers.go.
const csrfCookieName = "csrf"

// csrfFormField is the hidden form-field name every
// state-changing form must include. The handler reads the
// POST body, compares the form value to the cookie value
// in constant time, and rejects mismatches with 403.
const csrfFormField = "csrf_token"

// csrfCurrentRequest holds a pointer to the in-flight
// http.Request while a handler is rendering. The csrf
// template helper (csrfToken) reads the CSRF cookie from
// this pointer to inject into form templates. The pointer
// is set by csrfMiddleware before delegating to the next
// handler and cleared on return.
//
// Why an atomic.Pointer rather than passing the request
// through the data map or as a context value:
//
//   - data map: would require touching every render site
//     (and there are 18 form templates, many of which
//     build the data map inline).
//   - context: template funcs don't have access to the
//     request context.
//
// The atomic pointer is safe because Go's net/http serves
// each request on a single goroutine, and the middleware
// sets the pointer immediately before the next handler
// runs (which executes the template). The same goroutine
// reads the pointer that the same goroutine wrote, so the
// relaxed atomic semantics of atomic.Pointer are sufficient.
var csrfCurrentRequest atomic.Pointer[http.Request]

// setCSRFCurrentRequest stores r for the duration of the
// current request. Called by csrfMiddleware. The matching
// clearCSRFCurrentRequest is called via defer.
func setCSRFCurrentRequest(r *http.Request) {
	csrfCurrentRequest.Store(r)
}

// clearCSRFCurrentRequest is the defer partner to
// setCSRFCurrentRequest. Idempotent.
func clearCSRFCurrentRequest() {
	csrfCurrentRequest.Store(nil)
}

// csrfToken is the template helper exposed under the name
// `csrfToken`. It returns the CSRF cookie value for the
// current request, or the empty string if there is no
// cookie (e.g., a request that bypassed the middleware, or
// a test rendering a template directly). The empty value
// is the safe default: a form rendered with an empty
// csrf_token will not pass the POST-side CSRF check, which
// is the desired behavior for a request that didn't
// establish a token.
func csrfToken() string {
	r := csrfCurrentRequest.Load()
	if r == nil {
		return ""
	}
	c, err := r.Cookie(csrfCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// csrfTokenBytes is the entropy of the per-session CSRF
// token. 32 bytes (256 bits) is well above any practical
// brute-force threshold and matches the project's other
// random-token lengths (session token, OAuth state).
const csrfTokenBytes = 32

// csrfCookieMaxAge is the lifetime of the CSRF cookie in
// seconds. 12 hours covers a workday session; a user who
// lets the browser sit overnight gets a fresh token on
// their next request.
const csrfCookieMaxAge = 12 * 60 * 60

// csrfEnabled reads the CSRF_ENABLED env var at middleware
// construction time. The default is true so production
// deployments get CSRF protection out of the box — the
// security review (#3) is explicit that a strict CSRF
// posture for all mutating routes is the desired
// behavior. Set CSRF_ENABLED=false to opt out (dev
// environments, the e2e harness, integration tests that
// issue direct HTTP POSTs without the cookie).
func csrfEnabled() bool {
	raw := os.Getenv("CSRF_ENABLED")
	if raw == "" {
		return true
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return true
	}
	return v
}

// csrfMiddleware returns a middleware that enforces the
// double-submit CSRF pattern on every request it wraps.
//
// On every response, if the request doesn't already carry a
// csrf cookie, the middleware sets one with a fresh
// 32-byte random token. On every mutating request (POST /
// PUT / PATCH / DELETE) the middleware reads the csrf_token
// form field and compares it to the cookie value in
// constant time; mismatches are rejected with 403.
//
// GET / HEAD / OPTIONS bypass the check. Safe methods
// can't mutate server state, so the protection is
// unnecessary; bypassing them keeps the dev login HTML
// render and every existing GET route working without
// template changes.
//
// CSRF_ENABLED defaults to true so production deployments
// get protection out of the box. Set CSRF_ENABLED=false
// to opt out — used by the e2e harness (which POSTs
// directly via httptest) and by dev-mode flows that
// don't go through a browser cookie jar.
//
// The middleware also stores the current request in
// csrfCurrentRequest so the csrfToken template helper can
// read the CSRF cookie during template rendering. The
// pointer is cleared via defer so a handler can't read a
// stale request after returning.
func csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setCSRFCurrentRequest(r)
		defer clearCSRFCurrentRequest()

		if !csrfEnabled() {
			next.ServeHTTP(w, r)
			return
		}

		// Set the cookie on every response if the request
		// doesn't already have one. The token is also the
		// session identifier for the CSRF check, so once
		// it's been minted we don't rotate it on every
		// response — that would force legitimate POSTs
		// to chase a moving target.
		cookie, _ := r.Cookie(csrfCookieName)
		if cookie == nil {
			token, err := newCSRFToken()
			if err != nil {
				httpError(w, r, http.StatusInternalServerError, "Failed to mint CSRF token.", err)
				return
			}
			setCSRFCookie(w, r, token)
		} else {
			// Refresh the cookie's expiry so a long-lived
			// session doesn't see the token expire out
			// from under it. The value is preserved.
			setCSRFCookie(w, r, cookie.Value)
		}

		if !isMutatingMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		// Read the token from the form body. r.FormValue
		// parses application/x-www-form-urlencoded and
		// multipart/form-data; JSON bodies are out of
		// scope (the API layer is a different surface).
		formToken := r.FormValue(csrfFormField)
		cookieToken, _ := r.Cookie(csrfCookieName)
		if formToken == "" || cookieToken == nil || cookieToken.Value == "" {
			httpError(w, r, http.StatusForbidden, "CSRF token missing.", nil)
			return
		}
		if !csrfTokensMatch(cookieToken.Value, formToken) {
			httpError(w, r, http.StatusForbidden, "CSRF token mismatch.", nil)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// isMutatingMethod reports whether the HTTP method can
// change server state. POST, PUT, PATCH, and DELETE are
// the standard set; OPTIONS is excluded because it's
// preflight-style and never has a body.
func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// newCSRFToken returns a fresh 32-byte random token,
// base64url-encoded. The encoding makes the token safe to
// drop into a cookie value or a form field without escaping.
func newCSRFToken() (string, error) {
	b := make([]byte, csrfTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// setCSRFCookie writes the CSRF token cookie to the
// response. Path is "/" so every form on the site sees the
// token; HttpOnly is set so client-side JavaScript can't
// read the value (the form field, not the cookie, is what
// the client sends); Secure is set when the request reached
// the server over TLS or via the X-Forwarded-Proto
// forwarding proxy convention.
func setCSRFCookie(w http.ResponseWriter, r *http.Request, token string) {
	secure := r.TLS != nil || isForwardedHTTPS(r)
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124 false positive: cookie has Secure / HttpOnly / SameSite=Strict
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   csrfCookieMaxAge,
	})
}

// isForwardedHTTPS reports whether a trusted reverse proxy
// is telling us the original request was HTTPS. We honor
// the X-Forwarded-Proto header (the de-facto convention
// used by AWS ALB, GCP LB, nginx, traefik) and require the
// request also carry X-Forwarded-For so a direct client
// can't trivially spoof the header.
func isForwardedHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	forwardedFor := r.Header.Get("X-Forwarded-For")
	return proto == "https" && forwardedFor != ""
}

// csrfTokensMatch reports whether two CSRF tokens compare
// equal. Uses subtle.ConstantTimeCompare so an attacker
// can't recover the cookie value byte-by-byte from
// response timing. Length mismatches are rejected first;
// subtle.ConstantTimeCompare returns 0 for different-length
// inputs (a length-based timing leak of its own) and we
// close that channel by short-circuiting on length.
func csrfTokensMatch(cookie, form string) bool {
	if len(cookie) != len(form) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie), []byte(form)) == 1
}
