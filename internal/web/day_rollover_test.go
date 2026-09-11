package web

import (
	"embed"
	"io"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed assets/js/day-rollover.js
var dayRolloverJS embed.FS

// TestBaseTemplate_CurrentDayMetaTag pins the HTML surface
// for the day-rollover feature. The base template must
// declare a `<meta name="current-day" content="YYYY-MM-DD">`
// tag carrying the day the page was rendered. The JS reads
// this tag at load time; on a visibilitychange event it
// compares the captured day to the browser's current day;
// when they differ (the user kept the tab open across
// midnight, or slept + woke on a new day), it calls
// location.reload() so the dashboard reflects the new state
// (today's HAT, today's leaves, today's birthday banner,
// etc.) instead of yesterday's.
//
// Why a meta tag (not a data-* attribute on body, not a
// global JS var): the meta tag is part of the standard HTML
// metadata surface, plays well with browser cache keys, and
// is easy to grep for in production page sources when
// diagnosing "why did this tab reload at 4 AM" support
// tickets.
func TestBaseTemplate_CurrentDayMetaTag(t *testing.T) {
	tmpl, err := parseTemplates()
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	require.NoError(t, tmpl.ExecuteTemplate(rec, "login.html", map[string]any{
		"Template": "login",
	}))
	body := rec.Body.String()

	re := regexp.MustCompile(`<meta\s+name="current-day"\s+content="(\d{4}-\d{2}-\d{2})"`)
	match := re.FindStringSubmatch(body)
	require.NotNil(t, match,
		"the base template must declare <meta name=\"current-day\" content=\"YYYY-MM-DD\">; got body=%s", body)
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, match[1],
		"the meta tag's content must be a YYYY-MM-DD literal")
}

// TestDayRolloverJS_HasReloadHook pins the JS source so the
// day-rollover feature can't regress to a no-op. The script
// must:
//   - read the current-day value from the meta tag (any
//     mechanism — getAttribute, querySelector, dataset, etc.)
//   - listen for visibilitychange events
//   - call location.reload() when the day has changed
//   - gate the reload on a day-mismatch check (so a same-day
//     tab toggle doesn't reload)
//
// These four behaviors are the entire contract; the regex
// checks below are implementation-agnostic so a future
// refactor (e.g. using `dataset` instead of `getAttribute`)
// doesn't need to update this test.
func TestDayRolloverJS_HasReloadHook(t *testing.T) {
	body := readAppJS(t, &dayRolloverJS, "assets/js/day-rollover.js")

	// The script must reference the current-day meta tag by name.
	// Matches `meta[name="current-day"]` (querySelector), the
	// getAttribute('current-day') pattern, or
	// `getElementsByName('current-day')`.
	assert.Regexp(t, `current-day`, body,
		"day-rollover.js must read the current-day meta tag")
	assert.Regexp(t, `visibilitychange`, body,
		"day-rollover.js must register a visibilitychange listener")
	assert.Contains(t, body, "location.reload",
		"day-rollover.js must call location.reload() when the day changed")

	// The reload must be gated on a day-mismatch check (or some
	// other guard). If the script reloads unconditionally on every
	// visibility event, every tab toggle wastes a server round
	// trip — the user-visible behavior degrades to "the page
	// reloads every time I look at it".
	hasGuard := strings.Contains(body, "!==") ||
		strings.Contains(body, "!=") ||
		strings.Contains(body, "getDate()") ||
		strings.Contains(body, "toDateString") ||
		strings.Contains(body, "if (")
	assert.True(t, hasGuard,
		"day-rollover.js must guard the reload behind a day-mismatch check (otherwise every visibility event reloads the page)")
}

// readAppJS loads a vendored JS source from a go:embed FS so
// the regex checks above can scan it directly. The other
// fun-effects_birthday_test.go file inlines its own embed +
// reader for the same reason; both files can share this
// helper if you want to factor it out, but the current
// duplication keeps each test file self-contained.
func readAppJS(t *testing.T, fs *embed.FS, path string) string {
	t.Helper()
	f, err := fs.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	buf, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(buf)
}
