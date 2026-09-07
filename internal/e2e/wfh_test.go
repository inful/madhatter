//go:build e2e

package e2e

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/inful/madhatter/internal/database"
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

// TestSignalOnSiteToday_SingleDay verifies the dashboard
// "I'm actually coming in today" affordance:
//   - With a recurring WFH row seeded for today, the dashboard
//     surfaces the today-button.
//   - Submitting the form POSTs /wfh/today/on-site, the row flips
//     to withdrawn on disk, and the redirect lands on / with
//     wfh_signal_on_site=ok in the URL.
//
// This is the single-day case — the route /wfh/today/on-site has
// no date parameter and operates on "today UTC" by definition.
func TestSignalOnSiteToday_SingleDay(t *testing.T) {
	ctx, cancel := harness.browserContext(t)
	defer cancel()

	today := time.Now().UTC().Format("2006-01-02")

	// Seed: dev user has an approved recurring WFH row today so the
	// dashboard's CanSignalOnSiteToday gate opens.
	seedDB, closeSeed := harness.openDBForSeeding(t)
	defer closeSeed()
	harness.seedDevRecurringWFH(t, seedDB, today)

	harness.loginAsFakeAdmin(t, ctx)

	// Drive the today-button form. The dashboard renders the form
	// at form[action="/wfh/today/on-site"] — chromedp.Submit hits
	// the actual submit button, which is what we want here
	// (unlike the dev-login form which we drive via direct GET to
	// dodge a chromedp context-race).
	if err := chromedp.Run(ctx,
		chromedp.Navigate(harness.BaseURL+"/"),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Submit(`form[action="/wfh/today/on-site"]`, chromedp.ByQuery),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
	); err != nil {
		t.Fatalf("submit /wfh/today/on-site: %v", err)
	}

	// Verify the redirect URL carries the success flash.
	var currentURL string
	if err := chromedp.Run(ctx,
		chromedp.Location(&currentURL),
	); err != nil {
		t.Fatalf("read post-submit URL: %v", err)
	}
	if !strings.Contains(currentURL, "wfh_signal_on_site=ok") {
		t.Errorf("post-submit URL must carry wfh_signal_on_site=ok, got %q", currentURL)
	}

	// Verify the row on disk is withdrawn.
	verifyDB, closeVerify := harness.openDBForSeeding(t)
	defer closeVerify()
	row := harness.lookupDevWFHByDate(t, verifyDB, today)
	if row == nil {
		t.Fatalf("expected WFH row for %s after withdraw, found none", today)
	}
	if row.Status != database.WFHStatusWithdrawn {
		t.Errorf("row for %s must be withdrawn after signal-on-site, got status %q",
			today, row.Status)
	}
}

// TestSignalOnSiteFuture_MultiDay verifies the Phase 3 forward-dated
// "I'll be in on [date]" picker:
//   - With recurring WFH rows seeded for today, +3 business days,
//     and +7 business days, the dashboard surfaces:
//     * the today-button (today row)
//     * the picker with two future options (+3 and +7)
//   - Selecting +3 in the picker and submitting POSTs
//     /wfh/on-site?date=+3, the +3 row flips to withdrawn, and
//     the redirect carries wfh_signal_on_site_future=ok with the
//     target date echoed back.
//   - The today row and the +7 row remain approved — the picker
//     targets one date only.
//
// This is the multi-day case — verifies the picker is date-keyed
// and that withdrawing one row doesn't cascade.
func TestSignalOnSiteFuture_MultiDay(t *testing.T) {
	ctx, cancel := harness.browserContext(t)
	defer cancel()

	today := time.Now().UTC()
	todayStr := today.Format("2006-01-02")
	target := nextBusinessDayString(today, 3)
	other := nextBusinessDayString(today, 7)

	// Sanity: the three dates must be distinct. If nextBusinessDayString
	// lands on a weekend boundary the +3 / +7 math could collide —
	// guard against that before driving the browser so the
	// assertions below stay unambiguous.
	if target == todayStr || target == other || other == todayStr {
		t.Fatalf("test fixture collision: today=%s target=%s other=%s",
			todayStr, target, other)
	}

	// Seed: three recurring WFH rows for dev user across today,
	// +3, +7. The today-row seeds the today-button; +3 and +7
	// seed the picker.
	seedDB, closeSeed := harness.openDBForSeeding(t)
	defer closeSeed()
	harness.seedDevRecurringWFH(t, seedDB, todayStr)
	harness.seedDevRecurringWFH(t, seedDB, target)
	harness.seedDevRecurringWFH(t, seedDB, other)

	harness.loginAsFakeAdmin(t, ctx)

	// Verify the dashboard surfaces both the today-button and the
	// picker before driving any action. The picker is identified
	// by its select[name="date"] inside form[action="/wfh/on-site"].
	var body string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(harness.BaseURL+"/"),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Evaluate(`document.body.innerText`, &body),
	); err != nil {
		t.Fatalf("navigate + read dashboard: %v", err)
	}
	if !strings.Contains(body, "I'm actually coming in today") {
		t.Errorf("dashboard must surface the today-button; body fragment:\n%s",
			truncate(body, 600))
	}
	if !strings.Contains(body, "I'll be in on") {
		t.Errorf("dashboard must surface the forward-dated picker; body fragment:\n%s",
			truncate(body, 600))
	}

	// Drive the picker: select the +3 date and submit the form.
	// We set the value via JS (date inputs/selects in chromedp
	// ignore SendKeys reliably) then click the submit button.
	targetEscaped := url.QueryEscape(target)
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(
			`(()=>{const s=document.querySelector('form[action="/wfh/on-site"] select[name="date"]');`+
				`s.value="`+target+`";`+
				`s.dispatchEvent(new Event('change',{bubbles:true}));`+
				`return s.value;})()`,
			nil,
		),
		chromedp.Submit(`form[action="/wfh/on-site"]`, chromedp.ByQuery),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
	); err != nil {
		t.Fatalf("submit /wfh/on-site?date=%s: %v", targetEscaped, err)
	}

	// Verify the redirect URL carries the success flash with the
	// target date echoed back.
	var currentURL string
	if err := chromedp.Run(ctx,
		chromedp.Location(&currentURL),
	); err != nil {
		t.Fatalf("read post-submit URL: %v", err)
	}
	if !strings.Contains(currentURL, "wfh_signal_on_site_future=ok") {
		t.Errorf("post-submit URL must carry wfh_signal_on_site_future=ok, got %q",
			currentURL)
	}
	if !strings.Contains(currentURL, "date="+target) {
		t.Errorf("post-submit URL must echo the target date %s, got %q",
			target, currentURL)
	}

	// Verify the row state on disk: +3 is withdrawn, today and +7
	// are still approved.
	verifyDB, closeVerify := harness.openDBForSeeding(t)
	defer closeVerify()

	todayRow := harness.lookupDevWFHByDate(t, verifyDB, todayStr)
	if todayRow == nil {
		t.Errorf("today row %s must still exist", todayStr)
	} else if todayRow.Status != database.WFHStatusApproved {
		t.Errorf("today row %s must remain approved (picker targets one date only), got %q",
			todayStr, todayRow.Status)
	}

	targetRow := harness.lookupDevWFHByDate(t, verifyDB, target)
	if targetRow == nil {
		t.Fatalf("target row %s must exist after submit", target)
	}
	if targetRow.Status != database.WFHStatusWithdrawn {
		t.Errorf("target row %s must be withdrawn after picker submission, got %q",
			target, targetRow.Status)
	}

	otherRow := harness.lookupDevWFHByDate(t, verifyDB, other)
	if otherRow == nil {
		t.Errorf("other row %s must still exist", other)
	} else if otherRow.Status != database.WFHStatusApproved {
		t.Errorf("other row %s must remain approved (picker targets one date only), got %q",
			other, otherRow.Status)
	}
}
