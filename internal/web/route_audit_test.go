package web

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/database/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupRouteAuditDB + newRouteAuditHandler mirror the helpers
// in user_approval_test.go but are scoped to this file so a
// future rename of those helpers doesn't break the route-audit
// tests.
func setupRouteAuditDB(t *testing.T) (*database.DB, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "route_audit.db"))
	require.NoError(t, err)
	return db, func() { _ = db.Close() }
}

func newRouteAuditHandler(t *testing.T, db *database.DB) (*Handler, *auth.SessionManager) {
	t.Helper()
	encryptor, err := auth.NewTokenEncryptor(false)
	require.NoError(t, err)
	sessionManager := auth.NewSessionManager(db.GetQueries(), 24*time.Hour)
	authManager := auth.NewAuthManager(nil, auth.NewUserService(db.GetQueries(), encryptor), sessionManager)
	// Wire the middleware with the same sessionManager the
	// authManager uses so the auth chain doesn't crash on a
	// nil dereference when it tries to read the session
	// cookie. The previous version passed &auth.Middleware{}
	// (empty), which worked for tests that bypassed the
	// router via a fresh chi.Mux, but panics on the full
	// route table because safeRequireAuth calls into the
	// sessionManager.
	authMiddleware := auth.NewMiddleware(sessionManager)
	h, err := NewHandler(db, authManager, authMiddleware, false, nil)
	require.NoError(t, err)
	return h, sessionManager
}

// seedAdminAndPendingUser creates one admin user and one
// pending user, returning the admin session token and the
// pending user's id. The admin session is what's used to
// authenticate the mutating requests; the pending user is
// the target whose state we observe to detect whether a
// mutation accidentally happened.
func seedAdminAndPendingUser(t *testing.T, db *database.DB, sessionManager *auth.SessionManager) (adminSession string, pendingID string) {
	t.Helper()
	ctx := context.Background()
	adminID := "admin-id-route-audit"
	_, err := db.GetQueries().CreateActiveUser(ctx, sqlc.CreateActiveUserParams{
		ID: adminID, Email: "admin-route-audit@example.com", Name: "Admin RA",
		Provider: "fake", ProviderID: adminID,
		IsAdmin: sql.NullInt64{Int64: 1, Valid: true},
	})
	require.NoError(t, err)
	pendingID = "pending-id-route-audit"
	_, err = db.GetQueries().CreateUser(ctx, sqlc.CreateUserParams{
		ID: pendingID, Email: "pending-route-audit@example.com", Name: "Pending RA",
		Provider: "fake", ProviderID: pendingID,
	})
	require.NoError(t, err)
	adminSession, err = sessionManager.CreateSession(ctx, adminID)
	require.NoError(t, err)
	return adminSession, pendingID
}

// TestUserApproveRoute_GETRejected pins the security review
// follow-up: an admin route that mutates state must be
// registered as r.Post, not r.HandleFunc. A GET against the
// /team/users/{id}/approve route must return 405 (Method
// Not Allowed), NOT 200 with a state change. Before the
// refactor, the route was registered with r.HandleFunc and
// the handler mutated on any method, so a cross-site link
// like `<img src="/team/users/<id>/approve">` from a
// logged-in admin's browser would silently approve a pending
// user.
//
// The test goes through the full handler.Router() (the
// production route table from registerRoutes) rather than
// mounting the handler on a fresh chi.Mux with r.Post —
// that would be tautological. The pin is on the production
// route registration, not on chi's r.Post behavior (which
// is well-tested by chi itself).
//
// The test request carries a valid session cookie so the
// auth middleware passes; we want to verify the route
// rejects GET AFTER auth, not the auth redirect itself.
func TestUserApproveRoute_GETRejected(t *testing.T) {
	db, cleanup := setupRouteAuditDB(t)
	defer cleanup()
	h, sessionManager := newRouteAuditHandler(t, db)
	adminSession, pendingID := seedAdminAndPendingUser(t, db, sessionManager)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet, "/team/users/"+pendingID+"/approve", nil,
	)
	//nolint:gosec // G124 false positive: test fixture cookie; Secure is not relevant
	req.AddCookie(&http.Cookie{Name: "session_token", Value: adminSession})
	h.Router().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
		"GET against the approve route must be rejected with 405, got %d", rec.Code)

	// The pending user must still be pending. A non-405
	// response from the old handler would have flipped
	// is_active to 1; the refactored route must not have
	// touched the row.
	pending, err := db.GetQueries().GetUserByID(req.Context(), pendingID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), pending.IsActive.Int64,
		"GET must not approve the user; is_active must remain 0")
}

// TestUserDenyRoute_GETRejected is the parallel test for the
// deny route.
func TestUserDenyRoute_GETRejected(t *testing.T) {
	db, cleanup := setupRouteAuditDB(t)
	defer cleanup()
	h, sessionManager := newRouteAuditHandler(t, db)
	adminSession, pendingID := seedAdminAndPendingUser(t, db, sessionManager)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet, "/team/users/"+pendingID+"/deny", nil,
	)
	//nolint:gosec // G124 false positive: test fixture cookie; Secure is not relevant
	req.AddCookie(&http.Cookie{Name: "session_token", Value: adminSession})
	h.Router().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
		"GET against the deny route must be rejected with 405, got %d", rec.Code)

	// The pending user must still exist. With the route
	// restricted to POST, a GET returns 405 from chi before
	// the handler is called, so the user's row is

	pending, err := db.GetQueries().GetUserByID(req.Context(), pendingID)
	require.NoError(t, err,
		"the pending user must still exist; a 405 response from the route must not have reached the handler")
	assert.Equal(t, "Pending RA", pending.Name,
		"the pending user's row must be unchanged")
}

// TestUserDeactivateRoute_GETRejected pins the same property
// for deactivation.
func TestUserDeactivateRoute_GETRejected(t *testing.T) {
	db, cleanup := setupRouteAuditDB(t)
	defer cleanup()
	h, sessionManager := newRouteAuditHandler(t, db)
	adminSession, activeID := seedAdminAndActiveUser(t, db, sessionManager)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet, "/team/users/"+activeID+"/deactivate", nil,
	)
	//nolint:gosec // G124 false positive: test fixture cookie; Secure is not relevant
	req.AddCookie(&http.Cookie{Name: "session_token", Value: adminSession})
	h.Router().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
		"GET against the deactivate route must be rejected with 405, got %d", rec.Code)

	// The active user must still be active.
	active, err := db.GetQueries().GetUserByID(req.Context(), activeID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), active.IsActive.Int64,
		"GET must not deactivate the user; is_active must remain 1")
}

// TestScheduleGenerateRoute_GETRendersForm pins that the
// /schedule/generate GET path renders the form, not a
// mutation. Unlike the user-management routes above, this
// one has a legitimate GET (form render) AND a POST
// (mutation). Both are registered separately so a future
// code change can't accidentally collapse them. The test
// confirms the GET path returns 200 and renders an HTML
// page containing the form, not a 302/500 from a mutation
// side-effect.
func TestScheduleGenerateRoute_GETRendersForm(t *testing.T) {
	db, cleanup := setupRouteAuditDB(t)
	defer cleanup()
	h, sessionManager := newRouteAuditHandler(t, db)
	adminSession, _ := seedAdminAndActiveUser(t, db, sessionManager)
	// The schedule-generate handler refuses to render
	// when there are no team members, so seed one. The
	// name/email don't need to match anything; the test
	// only checks that the route reaches the render path,
	// not the no-team-members 400 branch.
	_, err := db.AddTeamMember(t.Context(), "Schedule Member", "schedule-member@example.com")
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet, "/schedule/generate", nil,
	)
	//nolint:gosec // G124 false positive: test fixture cookie; Secure is not relevant
	req.AddCookie(&http.Cookie{Name: "session_token", Value: adminSession})
	h.Router().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code,
		"GET against the schedule generate route must render the form (200), got %d", rec.Code)
	assert.Contains(t, rec.Body.String(), "<form",
		"the GET response must contain a <form> element; if the handler is dispatching to the POST branch the form tag is missing")
}

// seedAdminAndActiveUser is the parallel to
// seedAdminAndPendingUser for tests that deactivate
// (the target must be active, not pending). It returns
// the admin session and the active user's id.

// seedAdminAndActiveUser is the parallel to
// seedAdminAndPendingUser for tests that deactivate
// (the target must be active, not pending). It returns
// the admin session and the active user's id.
func seedAdminAndActiveUser(t *testing.T, db *database.DB, sessionManager *auth.SessionManager) (adminSession string, activeID string) {
	t.Helper()
	ctx := context.Background()
	adminID := "admin-id-active-ra"
	_, err := db.GetQueries().CreateActiveUser(ctx, sqlc.CreateActiveUserParams{
		ID: adminID, Email: "admin-active-ra@example.com", Name: "Admin Active RA",
		Provider: "fake", ProviderID: adminID,
		IsAdmin: sql.NullInt64{Int64: 1, Valid: true},
	})
	require.NoError(t, err)
	activeID = "active-id-route-audit"
	_, err = db.GetQueries().CreateActiveUser(ctx, sqlc.CreateActiveUserParams{
		ID: activeID, Email: "active-route-audit@example.com", Name: "Active RA",
		Provider: "fake", ProviderID: activeID,
	})
	require.NoError(t, err)
	adminSession, err = sessionManager.CreateSession(ctx, adminID)
	require.NoError(t, err)
	return adminSession, activeID
}
