//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestCalendar_SubscriptionFlow walks the calendar subscription
// end-to-end:
//  1. Login as the dev admin (the seeded member).
//  2. Visit /calendar. The handler auto-creates a CalendarSubscription
//     for the logged-in member, and the template renders the
//     token-bounded ICS URL.
//  3. Pull the token out of the rendered page (regex on the
//     `/calendar/<token>/ics` URL).
//  4. Hit `/calendar/<token>/ics` over plain HTTP and verify the
//     body looks like a real iCalendar feed (BEGIN:VCALENDAR +
//     at least one VEVENT / the per-team-member VEVENT block).
//
// Refactoring risk coverage:
//   - chi router mount of GET /calendar and GET /calendar/{token}/ics
//   - automatic subscription creation in loadUserSubscription
//   - the ICalGenerator writing BEGIN:VCALENDAR/END:VCALENDAR
//     and a valid VEVENT block
func TestCalendar_SubscriptionFlow(t *testing.T) {
	ctx, cancel := harness.browserContext(t)
	defer cancel()

	harness.loginAsFakeAdmin(t, ctx)

	// /calendar auto-creates a subscription and renders the URLs.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(harness.BaseURL+"/calendar"),
		// The page renders a code block containing the token-bounded
		// ICS URL. We don't pin the markup because that's the part
		// most likely to change under refactor; we only need the URL.
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
	); err != nil {
		t.Fatalf("navigate to /calendar: %v", err)
	}

	var icsURL string
	if err := chromedp.Run(ctx,
		// The token-bounded ICS URL is rendered into a data-url
		// attribute on the .url-box div (see templates/calendar.html);
		// it's not in innerText. Pull it directly from the DOM.
		chromedp.Evaluate(
			`document.getElementById('calendar-url-box').getAttribute('data-url') || ''`,
			&icsURL,
		),
	); err != nil {
		t.Fatalf("read data-url: %v", err)
	}
	if icsURL == "" {
		var body string
		_ = chromedp.Evaluate(`document.body.innerText`, &body)
		t.Fatalf("calendar-url-box has no data-url attribute; body:\n%s",
			truncate(body, 600))
	}

	// Resolve to an absolute URL — the attribute is already absolute
	// (set via baseURLFromRequest), so this is identity-checked.
	absolute, err := absoluteURL(icsURL, harness.BaseURL)
	if err != nil {
		t.Fatalf("URL %q is not absolute: %v", icsURL, err)
	}
	icsURL = absolute

	matches := regexp.MustCompile(`/calendar/([A-Za-z0-9_-]+)/ics`).FindStringSubmatch(icsURL)
	if len(matches) < 2 {
		t.Fatalf("ICS URL %q does not contain a token segment", icsURL)
	}
	_ = matches[1] // token is parsed but the URL is used in full below

	// Fetch the ICS feed. /calendar/{token}/ics is intentionally
	// outside the auth middleware (the token IS the credential), so
	// we can fetch it without the browser's cookie state.
	resp, err := http.Get(icsURL)
	if err != nil {
		t.Fatalf("GET %s: %v", icsURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		icsBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s: status %d, body:\n%s",
			icsURL, resp.StatusCode, truncate(string(icsBody), 600))
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/calendar") {
		t.Errorf("expected text/calendar content-type, got %q", contentType)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read ICS body: %v", err)
	}
	ics := string(bodyBytes)

	// VCALENDAR envelope + at least one VEVENT block.
	if !strings.Contains(ics, "BEGIN:VCALENDAR") {
		t.Errorf("ICS feed missing BEGIN:VCALENDAR; first 400 chars:\n%s",
			truncate(ics, 400))
	}
	if !strings.Contains(ics, "END:VCALENDAR") {
		t.Errorf("ICS feed missing END:VCALENDAR; first 400 chars:\n%s",
			truncate(ics, 400))
	}
	if !strings.Contains(ics, "BEGIN:VEVENT") {
		t.Errorf("ICS feed missing any VEVENT; first 400 chars:\n%s",
			truncate(ics, 400))
	}
}

// absoluteURL resolves a reference URL against a base URL. If the
// reference is already absolute it is returned unchanged; otherwise
// it is joined to the base.
func absoluteURL(refURL, baseURL string) (string, error) {
	if u, err := url.Parse(refURL); err == nil && u.IsAbs() {
		return refURL, nil
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(refURL)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}
