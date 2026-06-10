package notify

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed templates/*.tmpl
var embeddedTemplates embed.FS

// renderer is the production text/template-based renderer. It maps an
// event to a (subject, body) pair using one template per event kind.
//
// Templates can be overridden per event via the NOTIFY_*_TXT_PATH env
// vars. When unset, the bundled template is used. The pattern mirrors
// the calendar event-template override documented in AGENTS.md.
type renderer struct {
	baseURL    string
	subjectTpl map[string]*template.Template
	bodyTpl    map[string]*template.Template
}

// NewRenderer parses the bundled templates and applies any
// env-var-supplied overrides. baseURL is substituted into the
// templates as {{.BaseURL}} — useful for "view in dashboard" links.
// Exposed so the api package can build a renderer with the runtime
// BaseURL from Config; tests use newRenderer directly via the
// internal helper.
func NewRenderer(baseURL string) (*renderer, error) {
	return newRenderer(baseURL)
}

// newRenderer is the package-private implementation. Kept named
// distinctly from the exported NewRenderer so the call sites read
// consistently and tests can use the internal helper without an
// indirection.
func newRenderer(baseURL string) (*renderer, error) {
	r := &renderer{
		baseURL:    baseURL,
		subjectTpl: make(map[string]*template.Template),
		bodyTpl:    make(map[string]*template.Template),
	}
	// One subject template and one body template per event kind. They
	// share the same data struct.
	for _, kind := range []string{
		EventSwapRequested,
		EventSwapAccepted,
		EventSwapRejected,
		EventSwapCancelled,
		EventWFHStateChange,
		EventCoverAssigned,
	} {
		subjectName := filepath.Base(kind) + ".subject.tmpl"
		bodyName := filepath.Base(kind) + ".body.tmpl"

		subject, err := r.loadTemplate(subjectName, kind, "subject")
		if err != nil {
			return nil, err
		}
		body, err := r.loadTemplate(bodyName, kind, "body")
		if err != nil {
			return nil, err
		}
		r.subjectTpl[kind] = subject
		r.bodyTpl[kind] = body
	}
	return r, nil
}

// loadTemplate loads a template, preferring the env-var override and
// falling back to the bundled file.
func (r *renderer) loadTemplate(name, kind, slot string) (*template.Template, error) {
	src, override, err := readTemplate(name, kind, slot)
	if err != nil {
		return nil, err
	}
	if override {
		// The file may have been hand-edited; surface a clear parse
		// error in the server log.
		fmt.Fprintf(os.Stderr, "notify: using override template for %s %s from %s\n", kind, slot, name)
	}
	t, err := template.New(name).Parse(src)
	if err != nil {
		return nil, fmt.Errorf("notify: parse %s template (%s %s): %w", name, kind, slot, err)
	}
	return t, nil
}

// readTemplate returns the template source and a flag indicating whether
// it came from an env-var override path. The override path is operator-
// controlled (set via env var at process start), not user input, so the
// potential file-inclusion risk is bounded.
func readTemplate(name, kind, slot string) (string, bool, error) {
	envKey := envKeyFor(kind, slot)
	if path := os.Getenv(envKey); path != "" {
		//nolint:gosec // G304: path is operator-controlled (env var)
		b, err := os.ReadFile(path)
		if err != nil {
			return "", false, fmt.Errorf("notify: read %s from %s: %w", envKey, path, err)
		}
		return string(b), true, nil
	}
	b, err := embeddedTemplates.ReadFile("templates/" + name)
	if err != nil {
		return "", false, fmt.Errorf("notify: embedded template %s missing: %w", name, err)
	}
	return string(b), false, nil
}

// envKeyFor returns the env var name that overrides a given template.
// Mirrors the calendar override convention: NOTIFY_<EVENT>_TXT_PATH
// for body, NOTIFY_<EVENT>_SUBJECT_TXT_PATH for subject.
func envKeyFor(kind, slot string) string {
	prefix := "NOTIFY_"
	switch kind {
	case EventSwapRequested:
		prefix += "SWAP_REQUESTED"
	case EventSwapAccepted:
		prefix += "SWAP_ACCEPTED"
	case EventSwapRejected:
		prefix += "SWAP_REJECTED"
	case EventSwapCancelled:
		prefix += "SWAP_CANCELLED"
	case EventWFHStateChange:
		prefix += "WFH_STATE_CHANGED"
	case EventCoverAssigned:
		prefix += "COVER_ASSIGNED"
	default:
		prefix += strings.ToUpper(strings.ReplaceAll(kind, ".", "_"))
	}
	if slot == "subject" {
		return prefix + "_SUBJECT_TXT_PATH"
	}
	return prefix + "_TXT_PATH"
}

// data is the union of fields any event template can reference. We
// keep it as a single struct (with optional fields) so all six
// templates share the same data signature and the renderer can fill
// in just the relevant ones per event.
type data struct {
	BaseURL string

	// Swap
	SwapID        string
	RequesterName string
	RequesterDate string
	TargetName    string
	TargetDate    string
	ActorName     string
	Reason        string

	// WFH
	Date      string
	OldStatus string
	NewStatus string

	// Cover
	LeaveMemberName string
	StartDate       string
	EndDate         string
	ResolvedBy      string
}

// render produces the (subject, body) pair for the given event.
func (r *renderer) render(eventKind string, event any) (subject, body string, err error) {
	d, err := toData(eventKind, event)
	if err != nil {
		return "", "", err
	}
	d.BaseURL = r.baseURL

	var sbuf, bbuf bytes.Buffer
	if t, ok := r.subjectTpl[eventKind]; ok {
		if err := t.Execute(&sbuf, d); err != nil {
			return "", "", fmt.Errorf("notify: execute subject template for %s: %w", eventKind, err)
		}
	} else {
		return "", "", fmt.Errorf("notify: no subject template registered for %s", eventKind)
	}
	if t, ok := r.bodyTpl[eventKind]; ok {
		if err := t.Execute(&bbuf, d); err != nil {
			return "", "", fmt.Errorf("notify: execute body template for %s: %w", eventKind, err)
		}
	} else {
		return "", "", fmt.Errorf("notify: no body template registered for %s", eventKind)
	}
	return strings.TrimRight(sbuf.String(), "\r\n"), bbuf.String(), nil
}

func toData(eventKind string, event any) (data, error) {
	switch e := event.(type) {
	case SwapEvent:
		return data{
			SwapID:        e.SwapID,
			RequesterName: e.RequesterName,
			RequesterDate: e.RequesterDate,
			TargetName:    e.TargetName,
			TargetDate:    e.TargetDate,
			ActorName:     e.ActorName,
			Reason:        e.Reason,
		}, nil
	case WFHEvent:
		return data{
			Date:      e.Date,
			OldStatus: e.OldStatus,
			NewStatus: e.NewStatus,
			ActorName: e.ActorName,
		}, nil
	case CoverEvent:
		return data{
			LeaveMemberName: e.LeaveMemberName,
			StartDate:       e.StartDate,
			EndDate:         e.EndDate,
			ResolvedBy:      e.ResolvedBy,
		}, nil
	default:
		return data{}, fmt.Errorf("notify: unknown event type %T for kind %s", event, eventKind)
	}
}
