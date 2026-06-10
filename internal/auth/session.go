package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
)

const (
	// SessionTokenLength is the length in bytes for generated session tokens.
	sessionTokenLength = 32
	// CleanupInterval is the interval between session cleanup runs.
	cleanupInterval = 15 * time.Minute
)

// SessionManager handles session creation, validation, and destruction.
type SessionManager struct {
	db              *sqlc.Queries
	sessionDuration time.Duration
	cookieName      string
	cleanupInterval time.Duration
	stopCleanup     chan struct{}
	stopOnce        sync.Once
}

// NewSessionManager creates a new session manager.
func NewSessionManager(db *sqlc.Queries, duration time.Duration) *SessionManager {
	return &SessionManager{
		db:              db,
		sessionDuration: duration,
		cookieName:      "session_token",
		cleanupInterval: cleanupInterval,
		stopCleanup:     make(chan struct{}),
	}
}

// CreateSession creates a new session for a user.
func (sm *SessionManager) CreateSession(ctx context.Context, userID string) (string, error) {
	// Generate secure session token
	token, err := generateSecureToken()
	if err != nil {
		return "", err
	}

	// Hash the token before storing in database
	tokenHash := hashToken(token)

	// Generate UUID for session ID
	sessionID := uuid.New().String()

	// Set expiration time
	expiresAt := time.Now().Add(sm.sessionDuration)

	// Store session in database with hashed token
	_, err = sm.db.CreateSession(ctx, sqlc.CreateSessionParams{
		ID:        sessionID,
		UserID:    userID,
		Token:     tokenHash,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	// Return the original unhashed token to the client
	return token, nil
}

// ValidateSession validates a session token and returns user info.
func (sm *SessionManager) ValidateSession(ctx context.Context, token string) (*sqlc.GetSessionByTokenRow, error) {
	// Hash the token before looking it up
	tokenHash := hashToken(token)

	// Get session using hashed token
	session, err := sm.db.GetSessionByToken(ctx, tokenHash)
	if err != nil {
		return nil, ErrInvalidSession
	}

	return &session, nil
}

// DestroySession removes a session.
func (sm *SessionManager) DestroySession(ctx context.Context, token string) error {
	// Hash the token before deletion
	tokenHash := hashToken(token)
	return sm.db.DeleteSession(ctx, tokenHash)
}

// DestroyUserSessions removes all sessions for a user.
func (sm *SessionManager) DestroyUserSessions(ctx context.Context, userID string) error {
	return sm.db.DeleteUserSessions(ctx, userID)
}

// SetSessionCookie sets the session cookie in the response.
func (sm *SessionManager) SetSessionCookie(w http.ResponseWriter, token string, secure bool) {
	cookie := &http.Cookie{ //nolint:gosec // G124 false positive: cookie has all required security attributes (HttpOnly, Secure, SameSite); gosec can't see them on multi-line struct literal
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
	cookie := &http.Cookie{ //nolint:gosec // G124 false positive: cookie has all required security attributes (HttpOnly, Secure, SameSite); gosec can't see them on multi-line struct literal
		Name:     sm.cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
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

// StartCleanup starts a background goroutine that periodically cleans up expired sessions.
func (sm *SessionManager) StartCleanup(ctx context.Context) {
	ticker := time.NewTicker(sm.cleanupInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// Clean up expired sessions using the parent context
				if err := sm.db.DeleteExpiredSessions(ctx); err != nil {
					log.Printf("Session cleanup error: %v\n", err)
				}
			case <-sm.stopCleanup:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// StopCleanup stops the background cleanup goroutine.
func (sm *SessionManager) StopCleanup() {
	sm.stopOnce.Do(func() {
		close(sm.stopCleanup)
	})
}

// generateSecureToken generates a cryptographically secure random token.
func generateSecureToken() (string, error) {
	bytes := make([]byte, sessionTokenLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

// hashToken hashes a session token using SHA-256.
func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
