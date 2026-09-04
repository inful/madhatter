//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestLeave_ReportFlow walks the leave file/report round-trip:
// login, visit /leave/report, fill dates that include a future
// business day, submit, navigate to /leave/manage, verify the
// new row appears.
//
// Refactoring risk coverage:
//   - chi router mount of GET/POST /leave/report
//   - handleLeaveReport parsing and validation
//   - handleLeaveManagement rendering
func TestLeave_ReportFlow(t *testing.T) {
	ctx, cancel := harness.browserContext(t)
	defer cancel()

	harness.loginAsFakeAdmin(t, ctx)

	// Pick a single-day window 7 business days from now so the
	// report lands well inside the future date and the
	// picker/scheduler leaves it alone.
	start := nextBusinessDayString(time.Now(), 7)
	end := start

	if err := chromedp.Run(ctx,
		chromedp.Navigate(harness.BaseURL+"/leave/report"),
		chromedp.WaitVisible(`form[action="/leave/report"]`,
			chromedp.ByQuery),
		// The dev user is admin, so the form uses a <select
		// name="member_id"> instead of a hidden input. Pick the
		// first non-empty option (the seeded dev user) before
		// filling the dates.
		chromedp.Evaluate(
			`(()=>{`+
				`const sel=document.querySelector('select[name="member_id"]');`+
				`if(sel){const opts=[...sel.options].filter(o=>o.value);`+
				`if(opts.length){sel.value=opts[0].value;`+
				`sel.dispatchEvent(new Event('input',{bubbles:true}));}}`+
				`const s=document.querySelector('input[name="start_date"]');`+
				`s.value="`+start+`";s.dispatchEvent(new Event('input',{bubbles:true}));`+
				`const e=document.querySelector('input[name="end_date"]');`+
				`e.value="`+end+`";e.dispatchEvent(new Event('input',{bubbles:true}));`+
				`return null;})()`,
			nil,
		),
		chromedp.Submit(`form[action="/leave/report"]`, chromedp.ByQuery),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Sleep(1*time.Second),
	); err != nil {
		t.Fatalf("submit leave report: %v", err)
	}

	if err := chromedp.Run(ctx,
		chromedp.Navigate(harness.BaseURL+"/leave/manage"),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
	); err != nil {
		t.Fatalf("navigate to /leave/manage: %v", err)
	}

	var body string
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.body.innerText`, &body),
	); err != nil {
		t.Fatalf("read /leave/manage body: %v", err)
	}

	if !strings.Contains(body, start) {
		t.Errorf("/leave/manage does not show the reported date %q; "+
			"the leave did not persist.\nFirst 800 chars:\n%s",
			start, truncate(body, 800))
	}
}

// TestUnsubscribe_InvalidToken_RendersSafely verifies the public
// /unsubscribe endpoint accepts a malformed token without
// crashing. The endpoint is token-only auth (no session), and an
// invalid token should render the "no longer valid" page rather
// than 500. This pins the safety contract for the unsubscribe
// route that sits outside the auth middleware.
//
// We don't generate a valid HMAC token here because that would
// require the harness to know the SESSION_SECRET the running
// server is using for the unsubscribe HMAC key (currently
// `SetUnsubscribeSecret` reads it via the api/web_routes.go
// bootstrap from the env). The harness does set
// SESSION_SECRET=test-secret-...; using that here would make
// this test depend on internal/notify directly which is fine
// since it's a test-only import.
//
// For now we assert the public-entry route is robust to a
// malformed/expired/forged token, which is the regression we
// actually want to catch — if the endpoint 500s instead of
// rendering the same "invalid" page for any non-verifying
// token, the contract is broken.
func TestUnsubscribe_InvalidToken_RendersSafely(t *testing.T) {
	cases := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"truncated", "alice."},
		{"wrong-secret", "garbage-token-with-no-signature"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// /unsubscribe is public (no session). Hit it directly
			// via http; no browser needed.
			u := harness.BaseURL + "/unsubscribe"
			if tc.token != "" {
				u += "?token=" + tc.token
			}
			resp, err := http.Get(u)
			if err != nil {
				t.Fatalf("GET %s: %v", u, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("GET %s: expected 200, got %d", u, resp.StatusCode)
			}
			ct := resp.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "text/html") {
				t.Errorf("GET %s: expected text/html content-type, got %q",
					u, ct)
			}

			buf := make([]byte, 4096)
			n, _ := resp.Body.Read(buf)
			body := string(buf[:n])
			if !strings.Contains(strings.ToLower(body), "no longer valid") &&
				!strings.Contains(strings.ToLower(body), "couldn") {
				t.Errorf("expected 'Link is no longer valid' copy in body "+
					"for token=%q; first 400 chars:\n%s",
					tc.token, truncate(body, 400))
			}
		})
	}
}
