package calendar

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"sync"
	texttemplate "text/template"
)

// defaultSupportAssignmentTextTemplate reproduces the current
// hard-coded text output: "Support duty" with optional cover /
// cover-for-leave suffix and the existing support-day link block.
var defaultSupportAssignmentTextTemplate = texttemplate.Must(texttemplate.New("supportText").Parse(
	`{{.BaseText}}{{if .Links}}

Links:
{{- range .Links}}
- {{.Text}}{{end}}{{end}}`,
))

// defaultSupportAssignmentHTMLTemplate reproduces the current
// hard-coded HTML output: a heading, a paragraph, and the support-day
// link block.
var defaultSupportAssignmentHTMLTemplate = template.Must(template.New("supportHTML").Funcs(supportHTMLFuncs).Parse(
	`{{htmlHeading .Summary}}{{htmlParagraph .BaseText}}{{if .Links}}<h4>Links</h4>{{range .Links}}{{if .HTML}}<p>{{.HTML}}</p>{{else}}<p><a href="{{.URL}}">{{.Label}}</a></p>{{end}}{{end}}{{end}}`,
))

// defaultLeaveTextTemplate reproduces "X leave for Y".
var defaultLeaveTextTemplate = texttemplate.Must(texttemplate.New("leaveText").Parse(`{{.BaseText}}`))

// defaultLeaveHTMLTemplate reproduces the heading + paragraph layout.
var defaultLeaveHTMLTemplate = template.Must(template.New("leaveHTML").Funcs(supportHTMLFuncs).Parse(
	`{{htmlHeading .Summary}}{{htmlParagraph .BaseText}}`,
))

// defaultHolidayTextTemplate reproduces the holiday text.
var defaultHolidayTextTemplate = texttemplate.Must(texttemplate.New("holidayText").Parse(`{{.BaseText}}`))

// defaultHolidayHTMLTemplate reproduces the holiday HTML.
var defaultHolidayHTMLTemplate = template.Must(template.New("holidayHTML").Funcs(supportHTMLFuncs).Parse(
	`{{htmlHeading .Summary}}{{htmlParagraph .BaseText}}`,
))

// per-event-kind on-disk template caches. Each cache is keyed by the
// operator-supplied file path; an empty path maps to the built-in
// default at lookup time.
var (
	supportTextCache sync.Map // map[string]*texttemplate.Template
	supportHTMLCache sync.Map // map[string]*template.Template
	leaveTextCache   sync.Map // map[string]*texttemplate.Template
	leaveHTMLCache   sync.Map // map[string]*template.Template
	holidayTextCache sync.Map // map[string]*texttemplate.Template
	holidayHTMLCache sync.Map // map[string]*template.Template
)

// supportHTMLFuncs is registered on every support HTML template so
// the default can keep using htmlHeading/htmlParagraph. Custom
// templates can call these helpers too.
var supportHTMLFuncs = template.FuncMap{
	"htmlHeading":   htmlHeading,
	"htmlParagraph": htmlParagraph,
}

// loadSupportAssignmentText returns the operator's template, or the
// built-in default if path is empty.
func loadSupportAssignmentText(path string) (*texttemplate.Template, error) {
	if path == "" {
		return defaultSupportAssignmentTextTemplate, nil
	}
	return loadTextFromFile(path, "supportAssignmentText", &supportTextCache)
}

// loadSupportAssignmentHTML returns the operator's HTML template, or
// the built-in default.
func loadSupportAssignmentHTML(path string) (*template.Template, error) {
	if path == "" {
		return defaultSupportAssignmentHTMLTemplate, nil
	}
	return loadHTMLFromFile(path, "supportAssignmentHTML", &supportHTMLCache, supportHTMLFuncs)
}

// loadLeaveText returns the operator's leave text template, or the
// built-in default.
func loadLeaveText(path string) (*texttemplate.Template, error) {
	if path == "" {
		return defaultLeaveTextTemplate, nil
	}
	return loadTextFromFile(path, "leaveText", &leaveTextCache)
}

// loadLeaveHTML returns the operator's leave HTML template.
func loadLeaveHTML(path string) (*template.Template, error) {
	if path == "" {
		return defaultLeaveHTMLTemplate, nil
	}
	return loadHTMLFromFile(path, "leaveHTML", &leaveHTMLCache, supportHTMLFuncs)
}

// loadHolidayText returns the operator's holiday text template.
func loadHolidayText(path string) (*texttemplate.Template, error) {
	if path == "" {
		return defaultHolidayTextTemplate, nil
	}
	return loadTextFromFile(path, "holidayText", &holidayTextCache)
}

// loadHolidayHTML returns the operator's holiday HTML template.
func loadHolidayHTML(path string) (*template.Template, error) {
	if path == "" {
		return defaultHolidayHTMLTemplate, nil
	}
	return loadHTMLFromFile(path, "holidayHTML", &holidayHTMLCache, supportHTMLFuncs)
}

// loadTextFromFile is the shared text-template loader. The cache is
// returned as-is when it already has a parsed template for path.
func loadTextFromFile(path, name string, cache *sync.Map) (*texttemplate.Template, error) {
	if cached, ok := cache.Load(path); ok {
		return cached.(*texttemplate.Template), nil
	}
	contents, err := os.ReadFile(path) //nolint:gosec // Template path is deployment-configured; reading it is intended.
	if err != nil {
		return nil, fmt.Errorf("read %s template: %w", name, err)
	}
	tmpl, err := texttemplate.New(name + "Override").Parse(string(contents))
	if err != nil {
		return nil, fmt.Errorf("parse %s template: %w", name, err)
	}
	cache.Store(path, tmpl)
	return tmpl, nil
}

// loadHTMLFromFile is the shared HTML-template loader.
func loadHTMLFromFile(path, name string, cache *sync.Map, funcs template.FuncMap) (*template.Template, error) {
	if cached, ok := cache.Load(path); ok {
		return cached.(*template.Template), nil
	}
	contents, err := os.ReadFile(path) //nolint:gosec // Template path is deployment-configured; reading it is intended.
	if err != nil {
		return nil, fmt.Errorf("read %s template: %w", name, err)
	}
	tmpl, err := template.New(name + "Override").Funcs(funcs).Parse(string(contents))
	if err != nil {
		return nil, fmt.Errorf("parse %s template: %w", name, err)
	}
	cache.Store(path, tmpl)
	return tmpl, nil
}

// supportAssignmentData is the data exposed to the support-assignment
// event templates. The Presence field is the per-day snapshot, embedded
// via a named field so its members are promoted into the template's
// data scope. The linter flags the field as unused because nothing
// reads it by name; the data is consumed by the template engine.
type supportAssignmentData struct {
	//nolint:unused // Field is read by template execution through field promotion.
	presenceSnapshot
	Summary         string
	BaseText        string
	IsCover         bool
	IsCoverForLeave bool
	Date            string
	Links           []meetingLinkTemplateData
}

// leaveData is the data exposed to the leave-event templates.
type leaveData struct {
	//nolint:unused // Read by template execution.
	presenceSnapshot
	Summary    string
	BaseText   string
	MemberName string
	LeaveType  string
	StartDate  string
	EndDate    string
}

// holidayData is the data exposed to the holiday-event templates.
type holidayData struct {
	//nolint:unused // Read by template execution.
	presenceSnapshot
	Summary  string
	BaseText string
	Name     string
	Date     string
}

// renderTemplate executes a text/template against data and returns the
// resulting string.
func renderTemplate(tmpl *texttemplate.Template, name string, data any) (string, error) {
	if tmpl == nil {
		return "", fmt.Errorf("nil %s template", name)
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// renderHTMLTemplate executes an html/template against data and
// returns the resulting string.
func renderHTMLTemplate(tmpl *template.Template, name string, data any) (string, error) {
	if tmpl == nil {
		return "", fmt.Errorf("nil %s template", name)
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}
