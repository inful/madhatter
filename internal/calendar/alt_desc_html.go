package calendar

import (
	"fmt"
	"html"
	"net/url"
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

func htmlLink(href string, label string) string {
	if strings.TrimSpace(href) == "" {
		return ""
	}
	if _, err := url.ParseRequestURI(href); err != nil {
		return ""
	}
	if strings.TrimSpace(label) == "" {
		label = href
	}
	return fmt.Sprintf(`<p><a href="%s">%s</a></p>`, html.EscapeString(href), html.EscapeString(label))
}

func htmlList(title string, items []string) string {
	var b strings.Builder
	b.WriteString("<h4>")
	b.WriteString(html.EscapeString(title))
	b.WriteString("</h4>")

	b.WriteString("<ul>")
	if len(items) == 0 {
		b.WriteString("<li>(none)</li>")
	} else {
		for _, item := range items {
			b.WriteString("<li>")
			b.WriteString(html.EscapeString(item))
			b.WriteString("</li>")
		}
	}
	b.WriteString("</ul>")

	return b.String()
}
