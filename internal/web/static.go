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
// discovered). The three upstream sources are:
//
//   - HTMX 1.9.10  (https://unpkg.com/htmx.org@1.9.10) — script
//   - Bulma 0.9.4   (https://cdn.jsdelivr.net/npm/bulma@0.9.4) — CSS
//   - FontAwesome 6.4.0 (https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.4.0)
//     — CSS + 7 webfonts (brands/regular/solid/v4compatibility × ttf/woff2)
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
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fs.ServeHTTP(w, r)
	}))
}

// staticContentType returns the Content-Type for a static asset
// path. Go's mime.TypeByExtension doesn't know about woff/woff2/ttf
// (the FontAwesome webfont extensions), so this helper supplies the
// right values. Returns "" for extensions the default FileServer
// would already get right — the caller then leaves the header alone.
func staticContentType(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	case ".eot":
		return "application/vnd.ms-fontobject"
	case ".svg":
		return "image/svg+xml"
	}
	return ""
}
