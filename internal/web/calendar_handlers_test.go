package web

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	assert.Equal(t, "/calendar/0/deeplink/compose", parsed.Path)

	query := parsed.Query()
	assert.Equal(t, "/calendar/action/compose", query.Get("path"))
	assert.Equal(t, "addsubscription", query.Get("rru"))
	assert.Equal(t, "Support rota", query.Get("name"))
	assert.Equal(t, "https://example.com/calendar/test-token/ics", query.Get("url"))
}
