package calendar

import (
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
