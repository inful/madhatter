package web

import (
	"crypto/tls"
	"html/template"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetSubscriptionURLs_IncludesTeamCalendarLinks(t *testing.T) {
	req := &http.Request{Host: "example.com", Header: make(http.Header)}
	data := map[string]any{}

	setSubscriptionURLs(req, "test-token", data)

	assert.Equal(t, "http://example.com/calendar/test-token/team.ics", data["TeamCalendarURL"])
	assert.Equal(t, "webcal://example.com/calendar/test-token/team.ics", string(data["TeamCalendarWebcalURL"].(template.URL)))
	assert.Contains(t, string(data["TeamCalendarOutlookURL"].(template.URL)), "name=HAT+Days+%28Rest+of+Team%29")
	assert.Contains(t, string(data["TeamCalendarGoogleURL"].(template.URL)), "cid=webcal%3A%2F%2Fexample.com%2Fcalendar%2Ftest-token%2Fteam.ics")
}

func TestBaseURLFromRequest_TLS(t *testing.T) {
	req := &http.Request{Host: "example.com"}
	req.TLS = &tls.ConnectionState{}

	assert.Equal(t, "https://example.com", baseURLFromRequest(req))
}

func TestBaseURLFromRequest_XForwardedProtoAndHost(t *testing.T) {
	req := &http.Request{Host: "internal:8080", Header: make(http.Header)}
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "public.example.com")

	assert.Equal(t, "https://public.example.com", baseURLFromRequest(req))
}

func TestBaseURLFromRequest_Forwarded(t *testing.T) {
	req := &http.Request{Host: "internal:8080", Header: make(http.Header)}
	req.Header.Set("Forwarded", "for=1.2.3.4;proto=https;host=forwarded.example.com")

	assert.Equal(t, "https://forwarded.example.com", baseURLFromRequest(req))
}

func TestOutlookSubscriptionURL_UsesHTTPSAndName(t *testing.T) {
	req := &http.Request{Host: "example.com", Header: make(http.Header)}

	result := outlookSubscriptionURL(req, "/calendar/test-token/ics", "Support rota")
	parsed, err := url.Parse(string(result))
	require.NoError(t, err)
	assert.Equal(t, "https", parsed.Scheme)
	assert.Equal(t, "outlook.office.com", parsed.Host)
	assert.Equal(t, "/calendar/0/addfromweb", parsed.Path)

	query := parsed.Query()
	assert.Equal(t, "Support rota", query.Get("name"))
	assert.Equal(t, "https://example.com/calendar/test-token/ics", query.Get("url"))
	assert.Empty(t, query.Get("path"))
	assert.Empty(t, query.Get("rru"))
}

func TestGoogleSubscriptionURL_UsesCIDWithWebcalURL(t *testing.T) {
	req := &http.Request{Host: "example.com", Header: make(http.Header)}

	result := googleSubscriptionURL(req, "/calendar/test-token/ics")
	parsed, err := url.Parse(string(result))
	require.NoError(t, err)
	assert.Equal(t, "https", parsed.Scheme)
	assert.Equal(t, "calendar.google.com", parsed.Host)
	assert.Equal(t, "/calendar/render", parsed.Path)

	query := parsed.Query()
	assert.Equal(t, "webcal://example.com/calendar/test-token/ics", query.Get("cid"))
}
