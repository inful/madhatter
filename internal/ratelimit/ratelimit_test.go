package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// alwaysOKHandler is a trivial handler that returns 200; the rate
// limit middleware must not alter this behavior.
var alwaysOKHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// TestLimiter_AllowsUpToCapacity verifies a fresh limiter lets
// exactly `capacity` requests through before the first 429.
func TestLimiter_AllowsUpToCapacity(t *testing.T) {
	limiter := New(5, 0) // no refill — bucket empties after 5
	mw := Middleware(limiter, alwaysOKHandler)

	for i := range 5 {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "request %d should pass", i+1)
	}

	// 6th request — bucket is empty.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code,
		"6th request from the same IP must be rejected")
}

// TestLimiter_RetryAfterHeaderIsSet asserts the 429 response
// carries a numeric Retry-After (RFC 9110 §10.2.3).
func TestLimiter_RetryAfterHeaderIsSet(t *testing.T) {
	limiter := New(1, 0.1) // 1 token, refill 1 per 10s
	mw := Middleware(limiter, alwaysOKHandler)

	// First request consumes the initial token.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	mw.ServeHTTP(httptest.NewRecorder(), req)

	// Second request — bucket is empty.
	req = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	retryAfter := rec.Header().Get("Retry-After")
	assert.NotEmpty(t, retryAfter, "Retry-After header must be set")
	secs, err := strconv.Atoi(retryAfter)
	require.NoError(t, err, "Retry-After must be an integer second count")
	assert.GreaterOrEqual(t, secs, 1, "Retry-After must round up to at least 1s")
	assert.LessOrEqual(t, secs, 60, "Retry-After should be reasonable (1s-60s for slow refills)")
}

// TestLimiter_DifferentIPsAreIndependent asserts each IP has its
// own bucket; rate-limiting one client must not affect another.
func TestLimiter_DifferentIPsAreIndependent(t *testing.T) {
	limiter := New(1, 0)
	mw := Middleware(limiter, alwaysOKHandler)

	for _, ip := range []string{"10.0.0.1:1234", "10.0.0.2:1234", "10.0.0.3:1234"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "first request from %s must pass", ip)
	}
}

// TestLimiter_RefillRestoresCapacity asserts a slowly-refilling
// bucket lets through additional requests after the wait.
func TestLimiter_RefillRestoresCapacity(t *testing.T) {
	// 1 token, refill 1000 per second — effectively 1ms per token.
	limiter := New(1, 1000)
	mw := Middleware(limiter, alwaysOKHandler)

	// Consume the initial token.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	mw.ServeHTTP(httptest.NewRecorder(), req)

	// Second request rejected.
	req = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)

	// Wait long enough for the bucket to refill one token.
	time.Sleep(50 * time.Millisecond)

	// Third request passes.
	req = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, "bucket should have refilled")
}

// TestLimiter_PassesThroughHandler verifies the middleware
// forwards successfully to the wrapped handler.
func TestLimiter_PassesThroughHandler(t *testing.T) {
	limiter := New(10, 0)
	mw := Middleware(limiter, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Handler", "yes")
		w.WriteHeader(http.StatusTeapot)
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusTeapot, rec.Code)
	assert.Equal(t, "yes", rec.Header().Get("X-Handler"))
}

// TestLimiter_429ResponseBodyIsPlainText asserts the rejection
// payload is a short, machine-readable string rather than HTML.
func TestLimiter_429ResponseBodyIsPlainText(t *testing.T) {
	limiter := New(1, 0)
	mw := Middleware(limiter, alwaysOKHandler)

	// Consume the token.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	mw.ServeHTTP(httptest.NewRecorder(), req)

	// Second request — 429.
	req = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	ct := rec.Header().Get("Content-Type")
	assert.Contains(t, ct, "text/plain")
	assert.NotEmpty(t, rec.Body.String())
}

// TestTokenBucket_RefillIsContinuous covers the lazy-refill math:
// after `elapsed` seconds, the bucket holds (elapsed * refillRate)
// extra tokens, capped at capacity.
func TestTokenBucket_RefillIsContinuous(t *testing.T) {
	b := NewTokenBucket(10, 2) // 10 tokens, +2/s
	// Drain it.
	for range 10 {
		ok, _ := b.Allow()
		require.True(t, ok)
	}
	ok, _ := b.Allow()
	require.False(t, ok, "11th request must be denied")

	// Wait 1s — at 2 tokens/s refill the bucket is back to 2
	// (capped at 10).
	time.Sleep(1100 * time.Millisecond)
	ok, _ = b.Allow()
	require.True(t, ok, "1s of refill must restore at least one token")
	ok, _ = b.Allow()
	require.True(t, ok)
	ok, _ = b.Allow()
	require.False(t, ok, "after consuming 2 refilled tokens, the 3rd must be denied")
}

// TestClientKey_StripsPort asserts the bucket key uses the host
// portion of RemoteAddr only.
func TestClientKey_StripsPort(t *testing.T) {
	r1 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	r1.RemoteAddr = "192.0.2.1:55555"
	r2 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	r2.RemoteAddr = "192.0.2.1:99999"

	assert.Equal(t, ClientKey(r1), ClientKey(r2),
		"requests from the same IP on different ports must share a bucket")
}
