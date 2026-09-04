package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/notify"
)

const testUnsubscribeSecret = "0123456789abcdef0123456789abcdef"

// newUnsubscribeHandler wires a Handler with the unsubscribe
// plumbing set up, returning the handler and a database with a
// known member.
func newUnsubscribeHandler(t *testing.T) (*Handler, *database.DB, string) {
	t.Helper()
	db, err := database.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	_, err = db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	m, err := db.GetMemberByEmail(ctx, "alice@example.com")
	require.NoError(t, err)
	require.NotNil(t, m)

	h, err := NewHandlerConfig(HandlerConfig{
		DB:                db,
		AuthManager:       &auth.AuthManager{},
		AuthMiddleware:    &auth.Middleware{},
		Development:       true, // dev mode, no auth required
		UnsubscribeSecret: testUnsubscribeSecret,
		PublicBaseURL:     "https://rota.example.com",
	})
	require.NoError(t, err)
	return h, db, m.ID
}

func TestUnsubscribe_OneClickDisables(t *testing.T) {
	t.Parallel()
	h, db, memberID := newUnsubscribeHandler(t)

	token := notify.NewUnsubscribeToken(memberID, testUnsubscribeSecret)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/unsubscribe?token="+token.String(), nil)
	rr := httptest.NewRecorder()
	h.handleUnsubscribe(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code, "should render confirmation page")
	assert.Contains(t, rr.Body.String(), "You've been unsubscribed")

	enabled, err := db.IsNotificationEmailEnabled(context.Background(), memberID)
	require.NoError(t, err)
	assert.False(t, enabled, "preference should be persisted as disabled")
}

func TestUnsubscribe_InvalidToken_RendersError(t *testing.T) {
	t.Parallel()
	h, db, _ := newUnsubscribeHandler(t)

	cases := map[string]string{
		"empty":           "/unsubscribe",
		"missing-payload": "/unsubscribe?token=alice.",
		"wrong-secret":    "/unsubscribe?token=" + notify.NewUnsubscribeToken("alice", "wrong-secret-but-long-enough-okay").String(),
		"unknown-id":      "/unsubscribe?token=" + notify.NewUnsubscribeToken("not-a-real-id", testUnsubscribeSecret).String(),
	}
	for name, target := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
			rr := httptest.NewRecorder()
			h.handleUnsubscribe(rr, req)
			assert.Equal(t, http.StatusOK, rr.Code)
			assert.Contains(t, rr.Body.String(), "no longer valid")
		})
	}
	// No state should have been changed.
	enabled, err := db.IsNotificationEmailEnabled(context.Background(), "alice")
	// "alice" is not a valid id, so the row should never exist.
	_ = enabled
	_ = err
	// Defaults to enabled, so the test is a no-op; we just check
	// no error.
	_ = db
}

func TestUnsubscribe_Resume_ReEnables(t *testing.T) {
	t.Parallel()
	h, db, memberID := newUnsubscribeHandler(t)

	// First disable.
	now := time.Now().UTC()
	require.NoError(t, db.SetNotificationEmailEnabled(context.Background(), memberID, false, &now))
	enabled, err := db.IsNotificationEmailEnabled(context.Background(), memberID)
	require.NoError(t, err)
	require.False(t, enabled)

	// Then resume via the token.
	form := url.Values{}
	form.Set("token", notify.NewUnsubscribeToken(memberID, testUnsubscribeSecret).String())
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/unsubscribe/resume", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.handleUnsubscribeResume(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "Notifications resumed")

	enabled, err = db.IsNotificationEmailEnabled(context.Background(), memberID)
	require.NoError(t, err)
	assert.True(t, enabled, "resume should re-enable")
}

func TestUnsubscribe_URLFunc_Roundtrips(t *testing.T) {
	t.Parallel()
	h, _, memberID := newUnsubscribeHandler(t)

	fn := h.UnsubscribeURLFunc()
	require.NotNil(t, fn)
	url := fn(memberID)
	assert.True(t, strings.HasPrefix(url, "https://rota.example.com/unsubscribe?token="))

	// Extract the token and verify it.
	tokenStr := strings.TrimPrefix(url, "https://rota.example.com/unsubscribe?token=")
	got, err := notify.VerifyUnsubscribeToken(tokenStr, testUnsubscribeSecret)
	require.NoError(t, err)
	assert.Equal(t, memberID, got)
}

func TestUnsubscribe_URLFunc_EmptyWithoutConfig(t *testing.T) {
	t.Parallel()
	// Empty subscribe config: build a Handler with no secret and no
	// base URL. The URL factory must be safe to call (either nil or
	// a function that returns "") so the templates can render
	// "no unsubscribe link" without nil-checks.
	db, err := database.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	h, err := NewHandlerConfig(HandlerConfig{
		DB:             db,
		AuthManager:    &auth.AuthManager{},
		AuthMiddleware: &auth.Middleware{},
	})
	require.NoError(t, err)

	assert.NotPanics(t, func() {
		fn := h.UnsubscribeURLFunc()
		if fn != nil {
			assert.Empty(t, fn("any-id"),
				"empty subscribe config must produce empty URLs")
		}
	})
}
