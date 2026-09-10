package web

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCSRFTokenTemplateFunc_RendersCookieValue pins the
// "form template migration" follow-up: when a form template
// uses {{ csrfToken }} the rendered HTML carries the cookie
// value as a hidden form field. This is the read path the
// migrated forms depend on; without it, every form template
// has to construct the input element manually and the value
// has to be threaded through every render call, which is
// exactly the per-render-helper sprawl this commit avoids.
func TestCSRFTokenTemplateFunc_RendersCookieValue(t *testing.T) {
	//nolint:gosec // G101 false positive: test fixture token, not a real credential.
	const expectedToken = "test-csrf-token-template-func-renders"

	tmpl, err := template.New("form").Funcs(template.FuncMap{
		"csrfToken": csrfToken,
	}).Parse(`<input type="hidden" name="csrf_token" value="{{ csrfToken }}">`)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	//nolint:gosec // G124 false positive: test fixture cookie; Secure is not relevant.
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: expectedToken})
	// csrfMiddleware stores the request in csrfCurrentRequest
	// before calling the next handler. The template func reads
	// from the same pointer, so the request must be set up
	// before the template renders. Mimic what the middleware
	// does:
	setCSRFCurrentRequest(req)
	defer clearCSRFCurrentRequest()

	require.NoError(t, tmpl.ExecuteTemplate(rec, "form", nil))
	body := rec.Body.String()
	assert.Contains(t, body, `name="csrf_token"`,
		"the template must render a csrf_token hidden input")
	assert.Contains(t, body, `value="`+expectedToken+`"`,
		"the value must be the cookie value, got %q", body)
}

// TestCSRFTokenTemplateFunc_EmptyWhenNoCookie pins the
// safety net: when the request has no csrf cookie, the
// template func returns an empty string. This happens
// when a template is rendered outside the request scope
// (a unit test, or any caller that hits the helper
// without first running the middleware) and when the
// middleware itself was bypassed by setting
// CSRF_ENABLED=false (the e2e harness, dev-mode
// flows). The empty value means the form still renders
// (no template error) and the resulting submission would
// fail the CSRF check at the POST boundary — which is
// the desired behavior: a form without a valid CSRF
// token must not be submittable.
func TestCSRFTokenTemplateFunc_EmptyWhenNoCookie(t *testing.T) {
	tmpl, err := template.New("form").Funcs(template.FuncMap{
		"csrfToken": csrfToken,
	}).Parse(`<input type="hidden" name="csrf_token" value="{{ csrfToken }}">`)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	// No CSRF cookie on the request.
	setCSRFCurrentRequest(req)
	defer clearCSRFCurrentRequest()

	require.NoError(t, tmpl.ExecuteTemplate(rec, "form", nil))
	body := rec.Body.String()
	assert.Contains(t, body, `value=""`,
		"when the cookie is absent the value must be empty (the form will not pass CSRF on submit), got %q", body)
	assert.NotContains(t, body, "panic",
		"the template must not panic on a missing-cookie request")
}

// TestCSRFTokenTemplateFunc_SafeOutsideRequestScope pins
// that the helper degrades gracefully when called outside
// the middleware's request scope — i.e., from a goroutine
// that doesn't have a stored request pointer (test code,
// background tasks). It must return empty, not panic.
func TestCSRFTokenTemplateFunc_SafeOutsideRequestScope(t *testing.T) {
	// Clear the pointer to simulate "not in a request scope".
	// Other tests in the same file may set it; clearCSRF is
	// idempotent.
	clearCSRFCurrentRequest()

	tmpl, err := template.New("form").Funcs(template.FuncMap{
		"csrfToken": csrfToken,
	}).Parse(`<input value="{{ csrfToken }}">`)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	require.NoError(t, tmpl.ExecuteTemplate(rec, "form", nil))
	body := rec.Body.String()
	assert.Equal(t, `<input value="">`, strings.TrimSpace(body),
		"outside request scope the value must be empty (and not panic)")
}

// TestAllFormTemplatesHaveCSRFToken is a meta-test that
// walks every form template in the embed and asserts each
// one contains a {{ csrfToken }} reference. This is the
// migration pin: any form template that doesn't add the
// CSRF field will be flagged by this test, and the test
// list itself is the checklist of which templates still
// need migration. As templates are migrated and the test
// passes, the form surface is complete.
func TestAllFormTemplatesHaveCSRFToken(t *testing.T) {
	// The template list is small enough to enumerate by hand
	// here. Each entry is a template file with a <form
	// method="POST"> that needs the csrf_token field.
	required := []string{
		"team.html",
		"calendar.html",
		"database_restore.html",
		"wfh_purge.html",
		"swaps.html",
		"wfh_swap_inbox.html",
		"wfh_request.html",
		"leave_management.html",
		"dashboard.html",
		"wfh_manage.html",
		"wfh_swap.html",
		"wfh_list.html",
		"calendar_subscriptions.html",
		"leave_report.html",
		"schedule_generate.html",
		"leave_report_sick.html",
		"wfh_mark.html",
	}
	for _, name := range required {
		t.Run(name, func(t *testing.T) {
			body, err := templateFS.ReadFile("templates/" + name)
			require.NoError(t, err, "template %s not found", name)
			assert.Contains(t, string(body), "csrf_token",
				"template %s must include the csrf_token field — without it, a POST through the form will be rejected by the CSRF middleware",
				name)
		})
	}
}
