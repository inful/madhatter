package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
)

const (
	// SessionTokenLength is the length in bytes for generated session tokens.
	sessionTokenLength = 32
)

// SessionManager handles session creation, validation, and destruction.
type SessionManager struct {
	db              *sqlc.Queries
	sessionDuration time.Duration
	cookieName      string
}

// NewSessionManager creates a new session manager.
func NewSessionManager(db *sqlc.Queries, duration time.Duration) *SessionManager {
	return &SessionManager{
		db:              db,
		sessionDuration: duration,
		cookieName:      "session_token",
	}
}

// CreateSession creates a new session for a user.
func (sm *SessionManager) CreateSession(ctx context.Context, userID string) (string, error) {
	// Generate secure session token
	token, err := generateSecureToken(sessionTokenLength)
	if err != nil {
		return "", err
	}

	// Generate UUID for session ID
	sessionID := uuid.New().String()

	// Set expiration time
	expiresAt := time.Now().Add(sm.sessionDuration)

	// Store session in database
	_, err = sm.db.CreateSession(ctx, sqlc.CreateSessionParams{
		ID:        sessionID,
		UserID:    userID,
		Token:     token,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	return token, nil
}

// ValidateSession validates a session token and returns user info.
func (sm *SessionManager) ValidateSession(ctx context.Context, token string) (*sqlc.GetSessionByTokenRow, error) {
	// Clean up expired sessions first
	_ = sm.db.DeleteExpiredSessions(ctx)

	// Get session
	session, err := sm.db.GetSessionByToken(ctx, token)
	if err != nil {
		return nil, ErrInvalidSession
	}

	return &session, nil
}

// DestroySession removes a session.
func (sm *SessionManager) DestroySession(ctx context.Context, token string) error {
	return sm.db.DeleteSession(ctx, token)
}

// DestroyUserSessions removes all sessions for a user.
func (sm *SessionManager) DestroyUserSessions(ctx context.Context, userID string) error {
	return sm.db.DeleteUserSessions(ctx, userID)
}

// SetSessionCookie sets the session cookie in the response.
func (sm *SessionManager) SetSessionCookie(w http.ResponseWriter, token string, secure bool) {
	cookie := &http.Cookie{
		Name:     sm.cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sm.sessionDuration.Seconds()),
	}
	http.SetCookie(w, cookie)
}

// ClearSessionCookie removes the session cookie.
func (sm *SessionManager) ClearSessionCookie(w http.ResponseWriter) {
	cookie := &http.Cookie{
		Name:     sm.cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	}
	http.SetCookie(w, cookie)
}

// GetSessionCookie retrieves the session token from cookies.
func (sm *SessionManager) GetSessionCookie(r *http.Request) (string, error) {
	cookie, err := r.Cookie(sm.cookieName)
	if err != nil {
		return "", err
	}
	return cookie.Value, nil
}

// generateSecureToken generates a cryptographically secure random token.
func generateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}
