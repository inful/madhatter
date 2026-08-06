package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/inful/madhatter/internal/ratelimit"
)

// tokenRateLimitMiddleware is a huma middleware that enforces the
// server's per-IP rate limit on the token-mint and token-revoke
// endpoints. Requests over the limit short-circuit with
// 429 Too Many Requests; other requests fall through to the
// next middleware in the chain.
//
// Huma middleware are called with a huma.Context which exposes
// RemoteAddr() — the per-request lookup string — without needing
// the full *http.Request. ClientKeyFromAddr derives the bucket key
// the same way the HTTP middleware does.
func (s *Server) tokenRateLimitMiddleware(ctx huma.Context, next func(huma.Context)) {
	if s.tokenRateLimit == nil {
		next(ctx)
		return
	}
	key := ratelimit.ClientKeyFromAddr(ctx.RemoteAddr())
	ok, _ := s.tokenRateLimit.Allow(key)
	if !ok {
		ctx.SetHeader("Retry-After", "60")
		ctx.SetStatus(http.StatusTooManyRequests)
		_, _ = ctx.BodyWriter().Write([]byte(`{"error":"rate limited"}`))
		return
	}
	next(ctx)
}
