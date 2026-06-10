package web

import (
	"html/template"
	"log"
	"net/http"
	"time"

	"github.com/inful/madhatter/internal/notify"
)

// SetUnsubscribeSecret installs the HMAC secret used to sign and
// verify the per-recipient one-click unsubscribe tokens that appear
// in email bodies and List-Unsubscribe headers. The same secret is
// also used to mint the per-recipient URLs in the renderer.
func (h *Handler) SetUnsubscribeSecret(secret string) {
	h.unsubscribeSecret = secret
	h.unsubscribeURLFn = notify.UnsubscribeURLFactory(h.publicBaseURL, secret)
}

// SetPublicBaseURL records the public origin (scheme + host) of the
// server so unsubscribe URLs can be absolute. Used by the
// renderer-time URL factory installed by SetUnsubscribeSecret.
func (h *Handler) SetPublicBaseURL(baseURL string) {
	h.publicBaseURL = baseURL
	if h.unsubscribeSecret != "" {
		h.unsubscribeURLFn = notify.UnsubscribeURLFactory(baseURL, h.unsubscribeSecret)
	}
}

// UnsubscribeURLFunc is exposed for tests and for the renderer to
// pull the per-recipient URL. Returns the empty string when the
// server hasn't been configured with a public base URL or secret.
func (h *Handler) UnsubscribeURLFunc() func(memberID string) string {
	return h.unsubscribeURLFn
}

// handleUnsubscribe is the public GET /unsubscribe endpoint. The
// token is the only auth — there is no session — and verifies that
// the request was issued by this server for the named member.
//
// On success the handler sets notification_preferences.email_enabled
// = 0 and renders a small confirmation page. The same page is used
// for invalid tokens, but with a "couldn't verify" message so the
// caller doesn't get a different error from a forged token.
func (h *Handler) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		h.renderUnsubscribePage(w, unsubscribeOutcome{
			Status: unsubscribeStatusInvalid,
		})
		return
	}
	memberID, err := notify.VerifyUnsubscribeToken(token, h.unsubscribeSecret)
	if err != nil || memberID == "" {
		h.renderUnsubscribePage(w, unsubscribeOutcome{
			Status: unsubscribeStatusInvalid,
		})
		return
	}

	// Confirm the member still exists. A member deleted between
	// email send and click is a normal-but-rare case; we treat it
	// as a successful unsubscribe by virtue of the FK CASCADE on
	// notification_preferences — the row goes away with the
	// member, and any future email would be blocked by the
	// member-not-found branch in the resolver.
	now := time.Now().UTC()
	if err := h.db.SetNotificationEmailEnabled(r.Context(), memberID, false, &now); err != nil {
		log.Printf("unsubscribe: failed to persist preference: %v", err)
		// A FK violation means the member was deleted. Show the
		// same "link no longer valid" page as a forged token,
		// since the token signed for a real member whose row is
		// gone is functionally indistinguishable.
		h.renderUnsubscribePage(w, unsubscribeOutcome{
			Status: unsubscribeStatusInvalid,
		})
		return
	}

	h.renderUnsubscribePage(w, unsubscribeOutcome{
		Status:   unsubscribeStatusDisabled,
		MemberID: memberID,
	})
}

// handleUnsubscribeResume is the POST counterpart that re-enables
// notifications for a member. Same token-based auth, but the
// action is "resume" instead of "stop".
func (h *Handler) handleUnsubscribeResume(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	token := r.FormValue("token")
	memberID, err := notify.VerifyUnsubscribeToken(token, h.unsubscribeSecret)
	if err != nil || memberID == "" {
		h.renderUnsubscribePage(w, unsubscribeOutcome{
			Status: unsubscribeStatusInvalid,
		})
		return
	}
	if err := h.db.SetNotificationEmailEnabled(r.Context(), memberID, true, nil); err != nil {
		log.Printf("unsubscribe: failed to resume: %v", err)
		// FK violation = member gone. Treat as "link no longer
		// valid" so we don't leak that the member existed at one
		// point.
		h.renderUnsubscribePage(w, unsubscribeOutcome{
			Status: unsubscribeStatusInvalid,
		})
		return
	}
	h.renderUnsubscribePage(w, unsubscribeOutcome{
		Status:   unsubscribeStatusResumed,
		MemberID: memberID,
	})
}

type unsubscribeStatus string

const (
	unsubscribeStatusInvalid  unsubscribeStatus = "invalid"
	unsubscribeStatusError    unsubscribeStatus = "error"
	unsubscribeStatusDisabled unsubscribeStatus = "disabled"
	unsubscribeStatusResumed  unsubscribeStatus = "resumed"
)

type unsubscribeOutcome struct {
	Status   unsubscribeStatus
	MemberID string
}

var unsubscribePageTmpl = template.Must(template.New("unsubscribe").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Email notifications — MadHatter Rota</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
  body { font-family: system-ui, -apple-system, "Segoe UI", sans-serif;
         max-width: 32rem; margin: 4rem auto; padding: 0 1rem; color: #1f2937; }
  h1 { font-size: 1.5rem; margin-bottom: 1rem; }
  p  { line-height: 1.5; }
  .ok    { color: #166534; }
  .err   { color: #991b1b; }
  .info  { color: #1e40af; }
  form   { margin-top: 1.5rem; }
  button { font: inherit; padding: 0.5rem 1rem; border: 1px solid #1f2937;
           background: #fff; cursor: pointer; border-radius: 4px; }
</style>
</head>
<body>
{{- if eq .Status "disabled" }}
  <h1 class="ok">You've been unsubscribed.</h1>
  <p>You won't receive any more email notifications from MadHatter Rota.</p>
  <form method="post" action="/unsubscribe/resume">
    <input type="hidden" name="token" value="">
    <p>Changed your mind?
      <button type="submit">Resume notifications</button>
    </p>
  </form>
{{- else if eq .Status "resumed" }}
  <h1 class="ok">Notifications resumed.</h1>
  <p>You will start receiving email notifications again the next time a relevant event happens.</p>
{{- else if eq .Status "error" }}
  <h1 class="err">Something went wrong.</h1>
  <p>We couldn't update your notification preferences. Please try again later or contact an admin.</p>
{{- else }}
  <h1 class="err">Link is no longer valid.</h1>
  <p class="info">This unsubscribe link has expired or was tampered with. You can still change your preferences from the dashboard.</p>
{{- end }}
</body>
</html>
`))

// renderUnsubscribePage writes the small confirmation page used
// by handleUnsubscribe and handleUnsubscribeResume.
func (h *Handler) renderUnsubscribePage(w http.ResponseWriter, o unsubscribeOutcome) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = unsubscribePageTmpl.Execute(w, o)
}
