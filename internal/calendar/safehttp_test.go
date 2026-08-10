package calendar

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSafeHTTPURL pins the URL-safety filter that decides which meeting
// URLs we propagate into iCalendar events. Anything not a parseable
// http/https URL with a host is rejected — the security-sensitive cases
// are javascript:, data:, file:, and the like, which would otherwise let
// an XSS payload slip into a calendar subscription.
func TestSafeHTTPURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", "", ""},
		{"http", "http://example.com", "http://example.com"},
		{"https", "https://example.com", "https://example.com"},
		{"https with port", "https://example.com:8443/path", "https://example.com:8443/path"},
		{"https with query and fragment", "https://meet.example.com/room/abc?token=xyz#section", "https://meet.example.com/room/abc?token=xyz#section"},
		{"ip host", "http://10.0.0.1:8080/", "http://10.0.0.1:8080/"},
		{"localhost", "http://localhost/", "http://localhost/"},

		// Reject everything that isn't http/https with a host.
		{"javascript scheme", "javascript:alert(1)", ""},
		{"data scheme", "data:text/html,<script>alert(1)</script>", ""},
		{"file scheme", "file:///etc/passwd", ""},
		{"ftp scheme", "ftp://example.com/", ""},
		{"mailto scheme", "mailto:victim@example.com", ""},
		{"vbscript scheme", "vbscript:msgbox(1)", ""},
		{"ws scheme", "ws://example.com/", ""},
		{"ssh scheme", "ssh://user@example.com", ""},
		{"missing scheme", "example.com", ""},
		{"scheme only no host", "https://", ""},
		{"relative path", "/some/path", ""},
		{"opaque value", "not a url at all", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := safeHTTPURL(tc.raw)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestSafeHTTPURL_PreservesRawString documents that the function returns
// the original raw string on success — not a re-formatted URL. Callers
// (e.g. the iCalendar link builder) rely on the raw string to preserve
// any non-canonical formatting the source trusted.
func TestSafeHTTPURL_PreservesRawString(t *testing.T) {
	t.Parallel()

	const raw = "https://meet.example.com/room?x=1&y=2"
	assert.Equal(t, raw, safeHTTPURL(raw))
}
