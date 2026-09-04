//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestWFH_RequestFlow exercises the WFH request round-trip:
// login, visit /wfh/request, fill in a future business day, submit,
// verify the request appears on the user's own /wfh list.
//
// Refactoring risk coverage:
//   - chi router mount of GET /wfh/request and POST /wfh/request
//   - handleWFHRequest still parses + validates the date
//   - handleWFHList still renders pending WFH rows
func TestWFH_RequestFlow(t *testing.T) {
	ctx, cancel := harness.browserContext(t)
	defer cancel()

	harness.loginAsFakeAdmin(t, ctx)

	// Form has a single required date input. Pick a future business
	// day so the request is within the horizon (cap is 28 days by
	// default but business-day selection is robust here).
	future := nextBusinessDayString(time.Now(), 7)

	if err := chromedp.Run(ctx,
		chromedp.Navigate(harness.BaseURL+"/wfh/request"),
		chromedp.WaitVisible(`input[name="date"]`, chromedp.ByQuery),
		// Set the value via JS rather than SendKeys. Some browsers
		// ignore simulated keypresses on <input type="date">
		// because the native date picker captures them; a direct
		// value assignment + dispatchEvent('input') is the
		// reliable path.
		chromedp.Evaluate(
			`(()=>{const i=document.querySelector('input[name="date"]');`+
				`i.value="`+future+`";`+
				`i.dispatchEvent(new Event('input',{bubbles:true}));`+
				`return i.value;})()`,
			nil,
		),
		chromedp.Submit(`form[action="/wfh/request"][method="post"]`,
			chromedp.ByQuery),
		// Settlement may run inline; the handler returns 200 with
		// either a success banner or a denial banner. Either is the
		// post-submit state. Wait for the body to settle.
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
	); err != nil {
		t.Fatalf("submit WFH request: %v", err)
	}

	// Land on /wfh directly so we have a known page; the WFH list
	// page shows every WFH row for the logged-in member.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(harness.BaseURL+"/wfh"),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
	); err != nil {
		t.Fatalf("navigate to /wfh: %v", err)
	}

	var body string
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.body.innerText`, &body),
	); err != nil {
		t.Fatalf("read /wfh body: %v", err)
	}

	if !strings.Contains(body, future) {
		t.Errorf("/wfh list does not show the requested date %q; "+
			"the request did not persist.\nFirst 800 chars:\n%s",
			future, truncate(body, 800))
	}
}

// nextBusinessDayString returns the next business day after
// from + offsetDays, formatted as YYYY-MM-DD. Mirrors the helper
// in internal/testutil without taking on that import dep.
func nextBusinessDayString(from time.Time, offsetDays int) string {
	date := from.AddDate(0, 0, offsetDays)
	for date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
		date = date.AddDate(0, 0, 1)
	}
	return date.Format("2006-01-02")
}
