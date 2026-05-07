package web

import (
	"context"
	"net/http"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database/sqlc"
)

// mustGetUser returns the authenticated user from the context.
// It panics if no user is present, which means a route was reached without
// going through safeRequireAuth middleware — a programming error.
func mustGetUser(ctx context.Context) *sqlc.GetSessionByTokenRow {
	user, ok := auth.GetUserFromContext(ctx)
	if !ok {
		panic("mustGetUser: no authenticated user in context — missing safeRequireAuth middleware on this route")
	}

	return user
}

// safeAuthMiddleware wraps middleware to handle nil auth components gracefully.
func (h *Handler) safeAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.authMiddleware == nil {
			// No authentication configured, proceed without auth.
			next.ServeHTTP(w, r)
			return
		}
		// Use the actual middleware.
		h.authMiddleware.OptionalAuth(next).ServeHTTP(w, r)
	})
}

// safeRequireAuth wraps middleware to handle nil auth components gracefully.
func (h *Handler) safeRequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.authMiddleware == nil {
			// No authentication configured - show error.
			http.Error(w, "Authentication required but not configured. Please set up OAuth provider environment variables.", http.StatusUnauthorized)
			return
		}
		// Use the actual middleware.
		h.authMiddleware.RequireAuth(next).ServeHTTP(w, r)
	})
}

// safeRequireAdmin wraps middleware to handle nil auth components gracefully.
func (h *Handler) safeRequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.authMiddleware == nil {
			// No authentication configured - show error.
			http.Error(w, "Admin access required but authentication not configured. Please set up OAuth provider environment variables.", http.StatusUnauthorized)
			return
		}
		// Use the actual middleware.
		h.authMiddleware.RequireAdmin(next).ServeHTTP(w, r)
	})
}
