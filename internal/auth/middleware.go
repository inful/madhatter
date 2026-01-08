package auth

import (
	"context"
	"net/http"

	"github.com/inful/madhatter/internal/database/sqlc"
)

// ContextKey is a custom type for context keys to avoid collisions.
type ContextKey string

const (
	UserContextKey ContextKey = "user"
)

// Middleware provides authentication middleware for HTTP handlers.
type Middleware struct {
	sessionManager *SessionManager
}

// NewMiddleware creates a new authentication middleware.
func NewMiddleware(sessionManager *SessionManager) *Middleware {
	return &Middleware{
		sessionManager: sessionManager,
	}
}

// RequireAuth is middleware that requires authentication.
// It validates the session and adds user info to the context.
func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get session token from cookie
		token, err := m.sessionManager.GetSessionCookie(r)
		if err != nil {
			// No session cookie, redirect to login
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Validate session
		session, err := m.sessionManager.ValidateSession(r.Context(), token)
		if err != nil {
			// Invalid or expired session
			m.sessionManager.ClearSessionCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Add user info to context
		ctx := context.WithValue(r.Context(), UserContextKey, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAdmin is middleware that requires admin privileges.
func (m *Middleware) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First check authentication
		token, err := m.sessionManager.GetSessionCookie(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		session, err := m.sessionManager.ValidateSession(r.Context(), token)
		if err != nil {
			m.sessionManager.ClearSessionCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Check admin status
		isAdmin := session.IsAdmin.Valid && session.IsAdmin.Int64 == 1
		if !isAdmin {
			http.Error(w, "Admin access required", http.StatusForbidden)
			return
		}

		// Add user info to context
		ctx := context.WithValue(r.Context(), UserContextKey, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// OptionalAuth is middleware that checks for authentication but doesn't require it.
// User info is added to context if available.
func (m *Middleware) OptionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := m.sessionManager.GetSessionCookie(r)
		if err == nil {
			session, err := m.sessionManager.ValidateSession(r.Context(), token)
			if err == nil {
				ctx := context.WithValue(r.Context(), UserContextKey, session)
				r = r.WithContext(ctx)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// GetUserFromContext extracts user information from the request context.
func GetUserFromContext(ctx context.Context) (*sqlc.GetSessionByTokenRow, bool) {
	user, ok := ctx.Value(UserContextKey).(*sqlc.GetSessionByTokenRow)
	return user, ok
}

// MustGetUserFromContext extracts user information from context, panics if not found.
func MustGetUserFromContext(ctx context.Context) *sqlc.GetSessionByTokenRow {
	if user, ok := GetUserFromContext(ctx); ok {
		return user
	}
	panic("user not found in context")
}
