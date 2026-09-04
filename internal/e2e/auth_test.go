//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestLogin_AndDashboardRender is the smallest useful end-to-end
// check: after the --development fake-login, the dashboard page
// loads and renders the HAT banner area. If this breaks, every
// authenticated test below breaks too, so it doubles as the
// harness's first smoke test.
//
// On failure: the dashboard is the entry point for every
// authenticated flow. A regression in the /login chain, the
// session cookie, or the chi router mount of / breaks here.
func TestLogin_AndDashboardRender(t *testing.T) {
	ctx, cancel := harness.browserContext(t)
	defer cancel()

	harness.loginAsFakeAdmin(t, ctx)

	var title string
	var currentURL string
	var bodyHasHATMarker bool
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
		chromedp.Title(&title),
		chromedp.Evaluate(`window.location.href`, &currentURL),
		chromedp.Evaluate(
			`document.body.innerText.indexOf('Today') !== -1 || `+
				`document.body.innerText.indexOf('No team members found') !== -1`,
			&bodyHasHATMarker,
		),
	); err != nil {
		t.Fatalf("dashboard post-login render failed (title=%q): %v", title, err)
	}

	// All currently-rendered dashboards include "Today" (the Today
	// card heading) or the no-team-members flash. Either is a
	// successful landing render.
	if !bodyHasHATMarker {
		var bodyText string
		_ = chromedp.Evaluate(`document.body.innerText`, &bodyText)
		t.Errorf("dashboard body did not contain the expected "+
			"'Today' / 'No team members' marker; url=%s title=%q "+
			"first-300-chars-of-body=%q",
			currentURL, title, truncate(bodyText, 300))
	}
}

// ----- context helpers used across the e2e suite -----

// browserCtx returns a fresh chromedp context with a per-test
// cancel. Use for tests that don't need login.
func browserCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return harness.browserContext(t)
}

// truncate keeps any *testing.T message bounded.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// containsFold is a case-insensitive substring check using
// strings.Contains — repeated here to keep the test file
// dependency on `strings` minimal.
func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// wait polls the page inner text until predicate returns true or
// the timeout elapses. Used in flow tests that depend on the
// dashboard rendering asynchronously after a server-side
// update (HTMX swaps, period boundaries, etc.).
func wait(t *testing.T, ctx context.Context, predicate string, timeout time.Duration) {
	t.Helper()
	if err := chromedp.Run(ctx, chromedp.Poll(
		predicate,
		nil,
		chromedp.WithPollingInterval(150*time.Millisecond),
		chromedp.WithPollingTimeout(timeout),
	)); err != nil {
		var landedAt string
		_ = chromedp.Evaluate(`window.location.href`, &landedAt)
		t.Fatalf("predicate %q never satisfied at %s: %v",
			truncate(predicate, 60), landedAt, err)
	}
}
