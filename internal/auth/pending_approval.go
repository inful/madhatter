package auth

import (
	"embed"
	"html/template"
	"strings"
)

// pendingApprovalData is the payload rendered by the "your account
// is awaiting approval" page. Field names match the template so
// callers can swap in their own template without changing the
// callback.
type pendingApprovalData struct {
	Email        string
	Provider     string
	BaseURL      string
	EmailSubject string
}

//go:embed pending_approval.html
var pendingApprovalFS embed.FS

// defaultPendingApprovalTemplate is the fallback used when the
// AuthManager has no template wired in. It is parsed once at
// package init; a small CSS block keeps the page self-contained
// without depending on the web bundle.
var defaultPendingApprovalTemplate = func() *template.Template {
	t, err := template.New("pending").ParseFS(pendingApprovalFS, "pending_approval.html")
	if err != nil {
		panic("auth: embedded pending_approval.html missing: " + err.Error())
	}
	return t
}()

// pendingApprovalEmailSubject is shown in the page's <title> and the
// mailto link. Kept in sync with the template body.
const pendingApprovalEmailSubject = "Your MadHatter account is awaiting approval"

// renderPendingApproval executes the configured template (or the
// default) and writes the result to w. provider is the OAuth provider
// name (Google, GitLab, fake, …); email is the just-created user's
// email; baseURL is used to construct the "what to do" hint. The
// email is shown in the page body so the user has a clear reference
// when they email an admin, but it is intentionally not pre-filled
// into the mailto link (we don't know the admin addresses here).
func renderPendingApproval(
	t *template.Template,
	w pendingApprovalResponseWriter,
	email, provider, baseURL string,
) error {
	tmpl := t
	if tmpl == nil {
		tmpl = defaultPendingApprovalTemplate
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, pendingApprovalData{
		Email:        email,
		Provider:     provider,
		BaseURL:      baseURL,
		EmailSubject: pendingApprovalEmailSubject,
	}); err != nil {
		return err
	}
	_, err := w.Write([]byte(buf.String()))
	return err
}

// pendingApprovalResponseWriter is the subset of http.ResponseWriter
// the renderer needs. Pulled out so tests can use a buffer without
// pulling in net/http.
type pendingApprovalResponseWriter interface {
	Write([]byte) (int, error)
}
