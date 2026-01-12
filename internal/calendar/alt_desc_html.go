package calendar

import (
	"html"
	"strings"

	ics "github.com/arran4/golang-ical"
)

const htmlContentType = "text/html"

func setAltDescHTML(event *ics.VEvent, htmlBody string) {
	if event == nil {
		return
	}
	if strings.TrimSpace(htmlBody) == "" {
		return
	}

	// Keep this as a single logical line; the ics serializer will fold as needed.
	value := "<html><body>" + htmlBody + "</body></html>"
	event.AddProperty("X-ALT-DESC", value, ics.WithFmtType(htmlContentType))
}

func htmlHeading(text string) string {
	return "<h3>" + html.EscapeString(text) + "</h3>"
}

func htmlParagraph(text string) string {
	return "<p>" + html.EscapeString(text) + "</p>"
}
