package web

import (
	"net/http"
	"strings"
)

// securityHeadersMiddleware adds the defensive HTTP response headers
// the project's deployment checklist calls for. Apply globally
// below the recovery / logging middleware and above the request
// router.
//
// The headers here are deliberately strict because the app is
// server-rendered HTML with no third-party scripts and no embedded
// iframes; loosen them only with a documented threat model.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()

		// Don't let browsers MIME-sniff a body away from its declared
		// Content-Type. Cheap defense against content-type confusion
		// attacks on user-uploaded content (backup .db downloads etc.).
		h.Set("X-Content-Type-Options", "nosniff")

		// Strict no-iframe. Clickjacking defense for an app that has
		// no legitimate use of being framed.
		h.Set("X-Frame-Options", "DENY")

		// Only send the Referer header to same-origin requests. Stops
		// leaking URL paths (which can include invite tokens in some
		// flows) to third parties via outbound links.
		h.Set("Referrer-Policy", "same-origin")

		// Limit what the browser is allowed to load and where it can
		// send data. 'self' covers our own origin for scripts, styles,
		// images, and connections. data: is whitelisted for img-src
		// only (the avatar / favicon code paths embed tiny data URIs).
		// 'unsafe-inline' is permitted on style-src because the Go html
		// templates include inline style attributes; tightening this
		// requires per-template nonces and is out of scope here.
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"img-src 'self' data:; "+
				"style-src 'self' 'unsafe-inline'; "+
				"script-src 'self'; "+
				"connect-src 'self'; "+
				"frame-ancestors 'none'; "+
				"form-action 'self'; "+
				"base-uri 'self'")

		// HSTS only when the request reached us over HTTPS (direct or
		// via a trusted proxy's X-Forwarded-Proto). Sending HSTS over
		// HTTP leaks the directive to network observers and can lock
		// users out of misconfigured origins.
		if isHTTPSRequest(r) {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}

// isHTTPSRequest reports whether the inbound request reached the
// server over TLS — either via a direct TLS termination, or via a
// trusted reverse proxy that set X-Forwarded-Proto. The proxy
// header is honored only when the request also carries a standard
// Forwarded header (RFC 7239) so a direct client cannot spoof TLS.
func isHTTPSRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); strings.EqualFold(proto, "https") {
		if r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") != "" {
			return true
		}
	}
	return false
}
