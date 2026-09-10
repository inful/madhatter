package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// assetsFS contains every third-party CSS/JS/font asset the web
// templates depend on. Vendoring them here lets the security-headers
// middleware keep its strict default-src 'self' without exception
// (the previous CDN-allow whack-a-mole shipped with v0.19.0 and
// v0.19.2 broke the page layout twice as new dependencies were
// discovered). The upstream sources are:
//
//   - HTMX 1.9.10  (https://unpkg.com/htmx.org@1.9.10) — script
//   - Bulma 0.9.4   (https://cdn.jsdelivr.net/npm/bulma@0.9.4) — CSS
//   - FontAwesome 6.4.0 (https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.4.0)
//     — CSS + 7 webfonts (brands/regular/solid/v4compatibility × ttf/woff2)
//   - canvas-confetti 1.9.3 (https://cdn.jsdelivr.net/npm/canvas-confetti@1.9.3)
//     — script, used by the dashboard's full-team-on-site celebration
//     (see issues/59). Vendored so the strict CSP stays at 'self'
//     and the confetti can't be silently broken by an upstream outage.
//
// Update this set in lockstep with base.html: any new <script> or
// <link> tag in the base template needs a corresponding file here.
//
//go:embed assets
var assetsFS embed.FS

// staticHandler serves /static/* from the embedded assets. The
// wrapper layer overrides the Content-Type for the file extensions
// Go's mime package doesn't recognize (font/woff2, font/ttf) and
// sets a long-lived Cache-Control header because the file names are
// version-pinned (htmx.min.js, bulma.min.css, all.min.css, and
// versioned FontAwesome webfonts); a deploy that changes any of
// them ships as a new release tag, not a hot reload.
func staticHandler() http.Handler {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		// embed.FS guarantees the assets/ prefix exists at compile time;
		// a missing sub would be a build bug, not a runtime condition.
		panic("staticHandler: assets/ subdirectory missing: " + err.Error())
	}
	fs := http.FileServer(http.FS(sub))
	return http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ct := staticContentType(r.URL.Path)
		if ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.Header().Set("Cache-Control", cacheControlFor(r.URL.Path))
		fs.ServeHTTP(w, r)
	}))
}

// cacheControlFor returns the right Cache-Control header for a
// static asset path. The vendored third-party libs are pinned
// in static.go's package doc and never change without a release
// tag — they get the long-lived immutable cache. App-owned JS
// is shipped in every release and must NOT be aggressively
// cached, otherwise a user upgrading from one release to the
// next keeps the OLD app-owned JS even though the vendored deps
// updated. The fix: app-owned assets use must-revalidate with
// a short max-age so the browser revalidates on the next
// page load without a round trip when the response is 304, and
// picks up the new file when it isn't.
//
// Vendored asset paths live in a vendor subdirectory
// (htmx/, bulma/, fontawesome/) OR are a vendored top-level
// file (canvas-confetti.browser.min.js). App-owned assets are
// everything else — top-level JS in /static/js/ + top-level
// CSS in /static/css/.
func cacheControlFor(p string) string {
	cleaned := strings.TrimPrefix(p, "/")
	segments := strings.SplitN(cleaned, "/", 2) //nolint:mnd // 2 = split into first segment + rest; standard path-partition idiom.

	// Top-level vendored files: only canvas-confetti is shipped
	// at the top level. Any other top-level file (the app's
	// own JS in /static/js/...) is app-owned.
	if len(segments) == 2 { //nolint:mnd // 2 = "two-segment path"; standard idiom for "vendored subdir" detection.
		// Vendored subdirectories: htmx/, bulma/, fontawesome/.
		switch segments[0] {
		case "htmx", "bulma", "fontawesome":
			return "public, max-age=31536000, immutable"
		}
	}
	base := strings.ToLower(path.Base(p))
	if base == "canvas-confetti.browser.min.js" || base == "favicon.ico" || base == "apple-touch-icon.png" {
		// Vendored / baked-from-system-emoji assets. The favicons
		// are pinned to the project mascot (🎩 for MadHatter —
		// see scripts/gen_favicon.py); they don't change between
		// releases unless the mascot does, so the long cache is
		// safe. Browsers revalidate aggressively on hard-refresh,
		// so an updated mascot just needs a release.
		return "public, max-age=31536000, immutable"
	}
	switch strings.ToLower(path.Ext(p)) {
	case ".css", ".js":
		// App-owned. Short cache + must-revalidate so a release
		// that edits dashboard.js / fun-effects.js / team.js
		// lands on the next page load.
		return "public, max-age=300, must-revalidate"
	default:
		// Webfonts and other long-lived binary assets. Version-
		// pinned upstream.
		return "public, max-age=31536000, immutable"
	}
}

// staticContentTypes maps file extensions to their Content-Type.
// Go's mime.TypeByExtension doesn't know about woff/woff2/ttf/
// ico/png (the FontAwesome webfont extensions + favicon), so
// this helper supplies the right values. Extension is the key;
// matches are case-insensitive (the lookup normalizes to lower
// case before consulting the map).
var staticContentTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".js":    "application/javascript; charset=utf-8",
	".woff2": "font/woff2",
	".woff":  "font/woff",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".eot":   "application/vnd.ms-fontobject",
	".svg":   "image/svg+xml",
	".ico":   "image/x-icon",
	".png":   "image/png",
}

// staticContentType returns the Content-Type for a static asset
// path. Returns "" for extensions the default FileServer would
// already get right — the caller then leaves the header alone.
func staticContentType(p string) string {
	return staticContentTypes[strings.ToLower(path.Ext(p))]
}
