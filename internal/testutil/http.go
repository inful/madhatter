package testutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// URLParam is a named URL parameter for HandlerCall. The order of
// values in the HandlerCall variadic sets the order they're added
// to chi.RouteContext, which matters when the production handler
// reads parameters in sequence.
type URLParam struct {
	Name  string
	Value string
}

// WithURLParam returns a copy of r with one URL parameter attached
// to the request context so chi.URLParam can read it. Use this in
// tests that invoke a handler method directly (outside the chi
// router) to simulate the parameter chi would have injected at
// the matched route.
//
// The chi RouteCtxKey is a public context-key symbol: tests are
// the canonical example of consumers who need to fabricate a
// RouteContext.
func WithURLParam(r *http.Request, name, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(name, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// HandlerCall invokes fn against a freshly-built *http.Request and
// returns the response recorder. The method and path are required;
// additional chi URL parameters can be passed via params. This is
// the small kit that turns the
//
//	req := httptest.NewRequest(...)
//	req = withChiParam(req, "id", "x")
//	rec := httptest.NewRecorder()
//	fn(rec, req)
//	assert.Equal(t, StatusMethodNotAllowed, rec.Code)
//
// boilerplate (which appears in ~30 handler-test files) into
//
//	rec := testutil.HandlerCall(t, fn, "GET", "/path",
//	    testutil.URLParam{Name: "id", Value: "x"})
//	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
//
// Behavior is identical: HandlerCall calls fn directly without
// the chi router, so middleware (auth, security headers) is
// bypassed. Tests that need to exercise middleware should mount
// the handler on a real chi.Mux via the NewHandler constructor.
func HandlerCall(t *testing.T, fn http.HandlerFunc, method, path string, params ...URLParam) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, path, nil)
	for _, p := range params {
		req = WithURLParam(req, p.Name, p.Value)
	}
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec
}
