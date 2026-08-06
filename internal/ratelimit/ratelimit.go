// Package ratelimit provides a small, in-memory, per-IP token-bucket
// rate limiter. It is sufficient for single-instance deployments; a
// multi-instance deployment should swap the storage backend for a
// shared counter (Redis etc.) but the API stays the same.
package ratelimit

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// TokenBucket is a tiny token-bucket limiter. Each bucket holds up
// to Capacity tokens and refills continuously at RefillRate tokens
// per second. The bucket drains by one token on every Allow.
type TokenBucket struct {
	Capacity   int
	RefillRate float64
	tokens     float64
	lastRefill time.Time
	mu         sync.Mutex
}

// NewTokenBucket constructs a bucket pre-filled to capacity.
func NewTokenBucket(capacity int, refillRate float64) *TokenBucket {
	return &TokenBucket{
		Capacity:   capacity,
		RefillRate: refillRate,
		tokens:     float64(capacity),
		lastRefill: time.Now(),
	}
}

// Allow consumes one token if available. It returns (true, 0) when
// the request may proceed, or (false, retryAfter) when the bucket is
// empty; the caller should respond 429 with Retry-After = retryAfter.
func (b *TokenBucket) Allow() (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * b.RefillRate
	if b.tokens > float64(b.Capacity) {
		b.tokens = float64(b.Capacity)
	}
	b.lastRefill = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}

	deficit := 1 - b.tokens
	wait := time.Duration(float64(time.Second) * deficit / b.RefillRate)
	return false, wait
}

// Limiter is a keyed token-bucket registry. The key is typically the
// client IP; the registry lazily creates a bucket the first time it
// sees a key. The per-bucket mutexes keep contention inside a single
// bucket (and thus a single client) from serializing unrelated
// clients.
type Limiter struct {
	Capacity   int
	RefillRate float64
	mu         sync.Mutex
	buckets    map[string]*TokenBucket
}

// New creates a limiter that admits `capacity` requests immediately
// per key and refills at `refillRate` tokens/second (continuous, not
// bursty).
func New(capacity int, refillRate float64) *Limiter {
	return &Limiter{
		Capacity:   capacity,
		RefillRate: refillRate,
		buckets:    make(map[string]*TokenBucket),
	}
}

// Allow returns (true, 0) if the request from key may proceed, or
// (false, retryAfter) if the bucket is empty.
func (r *Limiter) Allow(key string) (bool, time.Duration) {
	r.mu.Lock()
	b, ok := r.buckets[key]
	if !ok {
		b = NewTokenBucket(r.Capacity, r.RefillRate)
		r.buckets[key] = b
	}
	r.mu.Unlock()
	return b.Allow()
}

// Middleware enforces a per-key rate limit. When the bucket for the
// client key is empty, it short-circuits with 429 Too Many Requests
// and a Retry-After header set to the time until the next token
// becomes available.
func Middleware(limiter *Limiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := ClientKey(r)
		ok, retryAfter := limiter.Allow(key)
		if !ok {
			// Round up to whole seconds — Retry-After is in seconds
			// per RFC 9110 §10.2.3.
			secs := int(retryAfter.Seconds())
			if retryAfter > 0 && time.Duration(secs)*time.Second < retryAfter {
				secs++
			}
			if secs < 1 {
				secs = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("Too Many Requests\n"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// MiddlewareFactory is the chi-compatible middleware form: it takes
// the limiter at construction time and returns the
// (http.Handler) -> http.Handler shape chi.Use / chi.With expects.
func MiddlewareFactory(limiter *Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return Middleware(limiter, next)
	}
}

// ClientKeyFromAddr derives a bucket key from a raw RemoteAddr
// string. Prefers the host portion so a forwarded chain with
// mismatched proxy hops can't widen the bucket by varying
// X-Forwarded-For values. When the address doesn't have a port
// (Unix socket, test fixtures, etc.) the raw value is returned —
// every test request then shares one bucket, which is the correct
// shape for asserting rate-limit behavior.
func ClientKeyFromAddr(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// ClientKey derives the bucket key for a request. Wraps
// ClientKeyFromAddr. The existing tests assert this helper, so the
// indirection is here rather than replacing the call sites.
func ClientKey(r *http.Request) string {
	return ClientKeyFromAddr(r.RemoteAddr)
}
