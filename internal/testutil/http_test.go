package testutil

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
)

// TestWithURLParam pins the contract that WithURLParam attaches a
// chi.RouteContext with the named parameter. The handler reads
// chi.URLParam("id") and echoes it; the test asserts the value
// reaches the handler.
func TestWithURLParam(t *testing.T) {
	echoID := func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(chi.URLParam(r, "id")))
	}

	rec := HandlerCall(t, echoID, http.MethodGet, "/things/abc-123",
		URLParam{Name: "id", Value: "abc-123"})

	assert.Equal(t, "abc-123", rec.Body.String())
}

// TestHandlerCall_NoParams covers the variadic-empty path. A handler
// that doesn't read URL params should still receive a usable request.
func TestHandlerCall_NoParams(t *testing.T) {
	hit := false
	ping := func(http.ResponseWriter, *http.Request) { hit = true }

	rec := HandlerCall(t, ping, http.MethodPost, "/ping")

	assert.True(t, hit, "handler must be invoked")
	_ = rec
}

// TestHandlerCall_PreservesMethod asserts the caller's method
// reaches the handler (some duplicated `_WrongMethod_Returns405`
// tests silently use the wrong verb; pin the contract here so a
// refactor can't regress).
func TestHandlerCall_PreservesMethod(t *testing.T) {
	got := ""
	capture := func(_ http.ResponseWriter, r *http.Request) { got = r.Method }

	HandlerCall(t, capture, http.MethodDelete, "/x")

	assert.Equal(t, "DELETE", got)
}
