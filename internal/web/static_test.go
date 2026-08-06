package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStaticHandler_ServesVendoredAssets walks every vendored
// asset path and verifies the handler returns the right content
// with the right Content-Type. Pinning this in a test prevents
// regressions where (a) an upstream dep is removed from the
// assets/ directory but the base template still references it,
// or (b) the http.FileServer MIME type is wrong for webfonts
// (Go's mime package returns application/octet-stream for
// .woff2 unless we override it).
func TestStaticHandler_ServesVendoredAssets(t *testing.T) {
	cases := []struct {
		path        string
		expectCT    string
		expectBytes []byte // substring of file content
	}{
		{
			path:        "/static/htmx/htmx.min.js",
			expectCT:    "application/javascript",
			expectBytes: []byte("htmx"),
		},
		{
			path:        "/static/bulma/bulma.min.css",
			expectCT:    "text/css",
			expectBytes: []byte("bulma"),
		},
		{
			path:        "/static/fontawesome/all.min.css",
			expectCT:    "text/css",
			expectBytes: []byte("fontawesome"),
		},
		{
			// The webfont extensions that Go's mime package
			// doesn't know about. If the Content-Type is
			// application/octet-stream the browser still loads
			// the file but the font-rendering pipeline may fall
			// back. Pin it to the IANA-registered types.
			path:        "/static/webfonts/fa-solid-900.woff2",
			expectCT:    "font/woff2",
			expectBytes: []byte("wOF2"),
		},
		{
			path:     "/static/webfonts/fa-regular-400.ttf",
			expectCT: "font/ttf",
		},
	}

	for _, tc := range cases {
		t.Run(filepath.Base(tc.path), func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			staticHandler().ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			ct := rec.Header().Get("Content-Type")
			assert.True(t, strings.HasPrefix(ct, tc.expectCT),
				"Content-Type = %q, want prefix %q", ct, tc.expectCT)
			// Long-lived cache: the file names are version-pinned.
			assert.Contains(t, rec.Header().Get("Cache-Control"), "immutable",
				"static assets should be served with long-lived immutable cache headers")
			if len(tc.expectBytes) > 0 {
				body, err := io.ReadAll(rec.Body)
				require.NoError(t, err)
				assert.Contains(t, string(body), string(tc.expectBytes),
					"body should contain the expected magic bytes / version marker")
			}
		})
	}
}

// TestStaticHandler_PathTraversalRefused asserts the handler does
// NOT let `..` escape the assets/ subtree. The http.FileServer
// guards this by default, but we lock the guard with a test so
// anyone changing the wrapping layer notices.
func TestStaticHandler_PathTraversalRefused(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static/../templates.go", nil)
	rec := httptest.NewRecorder()
	staticHandler().ServeHTTP(rec, req)
	// http.FileServer resolves .. inside the served root and serves
	// the file or 404s; it never escapes outside. The test cares
	// that the response is NOT the source file — i.e. the wrapper
	// didn't accidentally widen the root.
	body, _ := io.ReadAll(rec.Body)
	assert.NotContains(t, string(body), "parseTemplates",
		"static handler must not serve files outside the assets/ root")
}
