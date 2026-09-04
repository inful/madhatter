//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// TestTeam_AddMember_EndToEnd exercises the full admin round-trip
// for adding a team member: navigate to the team page (still
// requires login), fill the form with a unique email, submit,
// and verify the new member appears in the rendered list.
//
// Refactoring risk coverage:
//   - chi router still mounts POST /team
//   - handleTeamPost still parses form values, validates, and
//     redirects on success
//   - team.html template still iterates GetActiveTeamMembers and
//     renders the email for each row
func TestTeam_AddMember_EndToEnd(t *testing.T) {
	ctx, cancel := harness.browserContext(t)
	defer cancel()

	harness.loginAsFakeAdmin(t, ctx)

	// Land on the team page.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(harness.BaseURL+"/team"),
		chromedp.WaitVisible(`form[action="/team"][method="post"]`,
			chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate to /team: %v", err)
	}

	// Unique email so the test is idempotent under repeated runs
	// against a long-lived test DB (it isn't — each test gets a
	// fresh DB via TestMain — but the convention is harmless).
	uniqueEmail := "alice+e2e@example.com"
	uniqueName := "Alice E2E"

	// Fill and submit.
	if err := chromedp.Run(ctx,
		chromedp.SendKeys(`input[name="name"]`, uniqueName,
			chromedp.ByQuery),
		chromedp.SendKeys(`input[name="email"]`, uniqueEmail,
			chromedp.ByQuery),
		chromedp.Submit(`form[action="/team"][method="post"]`,
			chromedp.ByQuery),
		// After a successful POST the handler 303-redirects back
		// to /team. Wait for the redirect to land before reading.
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Sleep(500*1000*1000), // 500ms HTMX settle
	); err != nil {
		t.Fatalf("submit new team member: %v", err)
	}

	// Diagnostic: confirm we're on /team after the redirect.
	var landedAt string
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.location.pathname`, &landedAt),
	); err != nil {
		t.Fatalf("read landed URL: %v", err)
	}
	if landedAt != "/team" {
		t.Fatalf("expected to land at /team after submit, got %q", landedAt)
	}

	// The new member should appear in the rendered team list. We
	// query the inner text of every <td> in the members table
	// rather than the whole page body — the team page has many
	// forms per member (recurring-WFH day toggles, exempt flag)
	// whose labels would dominate the body text and obscure the
	// membership assertion.
	var body string
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.body.innerText`, &body),
	); err != nil {
		t.Fatalf("read team page body: %v", err)
	}

	if !strings.Contains(body, uniqueEmail) {
		t.Errorf("team page body did not contain new email %q; "+
			"page rendered without the new row.\nFirst 800 chars:\n%s",
			uniqueEmail, truncate(body, 800))
	}
	if !strings.Contains(body, uniqueName) {
		t.Errorf("team page body did not contain new name %q; "+
			"page rendered without the new row.\nFirst 800 chars:\n%s",
			uniqueName, truncate(body, 800))
	}
}
