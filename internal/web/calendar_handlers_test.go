package web

import (
	"crypto/tls"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
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
