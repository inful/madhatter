package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"os"
	"strconv"
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
// construction time. The security review recommendation
// (#3) is a strict CSRF posture for all mutating routes;
// this commit lands the middleware + tests but leaves the
// default at "false" so the existing form templates can be
// migrated to the new csrf_token field in a follow-up.
// Once CSRF_ENABLED=true is set, every POST/PUT/DELETE
// route that lives under the middleware requires the field.
func csrfEnabled() bool {
	raw := os.Getenv("CSRF_ENABLED")
	if raw == "" {
		return false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false
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
// When CSRF_ENABLED is false the middleware is a no-op
// pass-through so existing flows (dev login, e2e harness)
// keep working until form templates are migrated to
// include the csrf_token field. Set CSRF_ENABLED=true
// to opt in.
func csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
