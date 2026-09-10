package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStaticHandler_ServesFaviconAssets pins the favicon
// surface (#61). The handler must serve /static/favicon.ico
// and /static/apple-touch-icon.png with the right Content-Type
// so browsers + iOS home-screen pinning pick them up. Without
// these files the browser tab shows a generic placeholder and
// iOS users see a blank preview when they pin the app.
func TestStaticHandler_ServesFaviconAssets(t *testing.T) {
	cases := []struct {
		path     string
		expectCT string
	}{
		{
			path:     "/static/favicon.ico",
			expectCT: "image/x-icon",
		},
		{
			path:     "/static/apple-touch-icon.png",
			expectCT: "image/png",
		},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), "GET", tc.path, nil)
			rec := httptest.NewRecorder()
			staticHandler().ServeHTTP(rec, req)

			require.Equal(t, 200, rec.Code,
				"favicon asset %q must be served; got status %d body=%s",
				tc.path, rec.Code, rec.Body.String())
			assert.Equal(t, tc.expectCT, strings.SplitN(rec.Header().Get("Content-Type"), ";", 2)[0],
				"%q must carry Content-Type %q", tc.path, tc.expectCT)
			assert.NotEmpty(t, rec.Body.Bytes(),
				"%q must serve a non-empty body", tc.path)
			// Favicons are vendored / pinned assets — long cache.
			assert.Contains(t, rec.Header().Get("Cache-Control"), "immutable",
				"%q must use the immutable cache header (long-lived vendored asset)", tc.path)
		})
	}
}

// TestBaseTemplate_FaviconLinkTags pins the HTML surface —
// the base template must declare both the standard favicon
// link tag (browser tab + bookmarks) and the apple-touch-icon
// link tag (iOS home-screen pinning) so every page renders the
// icons correctly without per-page configuration.
//
// The test renders login.html (a child template) which wraps
// base.html, so the link tags land in the rendered output.
// Rendering base.html directly returns empty because
// base.html uses {{define "base"}}; it's invoked via
// {{template "base" .}} from a page template.
func TestBaseTemplate_FaviconLinkTags(t *testing.T) {
	tmpl, err := parseTemplates()
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	require.NoError(t, tmpl.ExecuteTemplate(rec, "login.html", map[string]any{
		"Template": "login",
	}))
	body := rec.Body.String()

	assert.Contains(t, body, `rel="icon"`,
		"the base template must declare a favicon <link rel=\"icon\">")
	assert.Contains(t, body, `href="/static/favicon.ico"`,
		"the favicon link must point at /static/favicon.ico")
	assert.Contains(t, body, `rel="apple-touch-icon"`,
		"iOS home-screen pinning needs an apple-touch-icon <link>")
	assert.Contains(t, body, `href="/static/apple-touch-icon.png"`,
		"the apple-touch-icon must point at /static/apple-touch-icon.png")
}
