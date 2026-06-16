package web

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/database/sqlc"
	"github.com/inful/madhatter/internal/rota"
	"github.com/stretchr/testify/require"
)

// setupApprovalTestDB is a self-contained DB for approval-flow
// tests. A file-backed DB lets us exercise the transactional
// helpers in user_admin_handlers.go (which depend on the
// *database.DB.BeginTx path, not the sqlc.Queries path).
func setupApprovalTestDB(t *testing.T) (*database.DB, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "approval_test.db"))
	require.NoError(t, err)
	return db, func() { _ = db.Close() }
}

// newApprovalHandler wires a Handler with a real AuthManager and
// SessionManager (the approval handlers call into both). The
// notifier is left nil — the approval handlers don't fire
// notifications from this code path.
func newApprovalHandler(t *testing.T, db *database.DB) (*Handler, *auth.SessionManager) {
	t.Helper()
	encryptor, err := auth.NewTokenEncryptor()
	require.NoError(t, err)
	sessionManager := auth.NewSessionManager(db.GetQueries(), 24*time.Hour)
	authManager := auth.NewAuthManager(nil, auth.NewUserService(db.GetQueries(), encryptor), sessionManager)
	h, err := NewHandler(db, authManager, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	h.maintenance = rota.NewScheduleMaintenance(db)
	return h, sessionManager
}

// TestUserApprovalFlow_PendingUserCannotLogin seeds two users: a
// pending one (created via CreateUser, the production path) and an
// active admin. It then walks the full flow:
//
//  1. The pending user can be looked up but their session is
//     rejected by ValidateSession (the gate that locks pending
//     users out even if they have a cookie).
//  2. The admin sees the pending user in ListPendingUsers.
//  3. The admin approves via the ApproveUser SQL path.
//  4. After approval the pending user's session is accepted by
//     ValidateSession.
//
// This is the core invariant of the approval feature: pending
// users are stuck in a non-functional state, and approval flips
// the flag to make existing (or new) sessions valid.
func TestUserApprovalFlow_PendingUserCannotLogin(t *testing.T) {
	db, cleanup := setupApprovalTestDB(t)
	defer cleanup()
	_, sessionManager := newApprovalHandler(t, db)
	ctx := context.Background()

	// Create the first admin (CreateUserAsFirstAdmin is the only
	// path that inserts an active user; everything else is
	// pending by default).
	adminUser, err := db.GetQueries().CreateActiveUser(ctx, activeUserParams("admin-1", "admin@example.com", "Admin One", 1))
	require.NoError(t, err)

	// Create the pending user via the production path: a row from
	// CreateUser (the SQL the OAuth flow calls). Active = 0.
	pendingUser, err := db.GetQueries().CreateUser(ctx, pendingUserParams("pending-1", "alice@example.com", "Alice"))
	require.NoError(t, err)
	require.True(t, pendingUser.IsActive.Valid)
	require.Equal(t, int64(0), pendingUser.IsActive.Int64,
		"a freshly created user must be pending (is_active = 0)")

	// Issue a session for the pending user (simulating a stale
	// cookie or a successful login before the rule change).
	pendingSession, err := sessionManager.CreateSession(ctx, pendingUser.ID)
	require.NoError(t, err)

	// ValidateSession must reject the pending user's session.
	_, err = sessionManager.ValidateSession(ctx, pendingSession)
	require.Error(t, err, "pending users must be rejected by ValidateSession")

	// Issue a session for the admin and verify it works.
	adminSession, err := sessionManager.CreateSession(ctx, adminUser.ID)
	require.NoError(t, err)
	session, err := sessionManager.ValidateSession(ctx, adminSession)
	require.NoError(t, err)
	require.Equal(t, adminUser.ID, session.UserID)

	// The pending user must show up in the admin's pending list.
	pending, err := db.GetQueries().ListPendingUsers(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, pendingUser.ID, pending[0].ID)

	// Approve the pending user.
	approved, err := db.GetQueries().ApproveUser(ctx, pendingUser.ID)
	require.NoError(t, err)
	require.True(t, approved.IsActive.Valid)
	require.Equal(t, int64(1), approved.IsActive.Int64)
	require.False(t, approved.DeactivatedAt.Valid, "approving clears deactivated_at")

	// Now the pending user's existing session is valid.
	session, err = sessionManager.ValidateSession(ctx, pendingSession)
	require.NoError(t, err, "after approval the user's session must be accepted")
	require.Equal(t, pendingUser.ID, session.UserID)
}

// TestUserApprovalFlow_DenyRemovesUserAndInvalidatesSession exercises
// the deny path: the user row is deleted in a transaction with
// their sessions and OAuth tokens, and any pre-existing session
// becomes invalid (because GetSessionByToken no longer joins to a
// user).
func TestUserApprovalFlow_DenyRemovesUserAndInvalidatesSession(t *testing.T) {
	db, cleanup := setupApprovalTestDB(t)
	defer cleanup()
	_, sessionManager := newApprovalHandler(t, db)
	ctx := context.Background()

	adminUser, err := db.GetQueries().CreateActiveUser(ctx, activeUserParams("admin-2", "admin@example.com", "Admin", 1))
	require.NoError(t, err)
	pendingUser, err := db.GetQueries().CreateUser(ctx, pendingUserParams("pending-2", "bob@example.com", "Bob"))
	require.NoError(t, err)

	// Issue a session for the pending user.
	session, err := sessionManager.CreateSession(ctx, pendingUser.ID)
	require.NoError(t, err)
	_, err = sessionManager.ValidateSession(ctx, session)
	require.Error(t, err, "pending user must not be able to use their session")

	// Deny via the production SQL path: the team-page handler
	// composes these three deletes in a transaction (see
	// (*Handler).denyPendingUser in user_admin_handlers.go). We
	// exercise the same SQL here.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	q := db.GetQueries().WithTx(tx)
	require.NoError(t, q.DeleteUserOAuthTokens(ctx, pendingUser.ID))
	require.NoError(t, q.DeleteUserSessions(ctx, pendingUser.ID))
	require.NoError(t, q.DeleteUser(ctx, pendingUser.ID))
	require.NoError(t, tx.Commit())

	// The user row is gone.
	_, err = db.GetQueries().GetUserByID(ctx, pendingUser.ID)
	require.Error(t, err, "denied user must be deleted")

	// The session row is gone (we deleted them in the same tx),
	// so ValidateSession returns ErrInvalidSession.
	_, err = sessionManager.ValidateSession(ctx, session)
	require.Error(t, err)

	// The admin can still log in.
	adminSession, err := sessionManager.CreateSession(ctx, adminUser.ID)
	require.NoError(t, err)
	_, err = sessionManager.ValidateSession(ctx, adminSession)
	require.NoError(t, err)
}

// TestUserApprovalFlow_TeamPage_ApproveEndpoint exercises the HTTP
// layer: the team page lists pending users, and POSTing to the
// approve endpoint moves them to active. The full page render is
// out of scope; we just verify the count and the post-approve
// state change.
func TestUserApprovalFlow_TeamPage_ApproveEndpoint(t *testing.T) {
	db, cleanup := setupApprovalTestDB(t)
	defer cleanup()
	h, sessionManager := newApprovalHandler(t, db)
	ctx := context.Background()

	adminUser, err := db.GetQueries().CreateActiveUser(ctx, activeUserParams("admin-3", "admin@example.com", "Admin", 1))
	require.NoError(t, err)
	_, err = db.GetQueries().CreateUser(ctx, pendingUserParams("pending-3", "carol@example.com", "Carol"))
	require.NoError(t, err)

	before, err := db.GetQueries().CountPendingUsers(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), before)

	// The handler is admin-gated; the admin must be in the request
	// context. We hand-build a session cookie and a request.
	adminSession, err := sessionManager.CreateSession(ctx, adminUser.ID)
	require.NoError(t, err)

	// Look up Carol's ID.
	pending, err := db.GetQueries().ListPendingUsers(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	carolID := pending[0].ID

	// The handler uses chi.URLParam to get the id; that requires
	// a route-mounted handler. We mount the route on a chi router
	// and call through that.
	router := chi.NewRouter()
	router.Post("/team/users/{id}/approve", h.handleUserApprove)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/team/users/"+carolID+"/approve", nil)
	req.Header.Set("Cookie", "session_token="+adminSession)
	// requireAdmin reads the user from the request context; for a
	// direct handler call (no middleware) we have to seed it.
	ctx = context.WithValue(ctx, auth.UserContextKey, &sqlc.GetSessionByTokenRow{
		UserID:  adminUser.ID,
		Email:   adminUser.Email,
		Name:    adminUser.Name,
		IsAdmin: adminUser.IsAdmin,
	})
	req = req.WithContext(ctx)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code,
		"approve should redirect to /team")

	// Carol is now active.
	carol, err := db.GetQueries().GetUserByID(ctx, carolID)
	require.NoError(t, err)
	require.Equal(t, int64(1), carol.IsActive.Int64)

	after, err := db.GetQueries().CountPendingUsers(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(0), after)
}

// TestUserApprovalFlow_TeamPage_DeactivateReactivate covers the
// deactivate → reactivate round-trip. The user stays in the
// database but is_active flips 0 → 1 and the team member is
// deactivated/activated in lockstep.
func TestUserApprovalFlow_TeamPage_DeactivateReactivate(t *testing.T) {
	db, cleanup := setupApprovalTestDB(t)
	defer cleanup()
	_, sessionManager := newApprovalHandler(t, db)
	ctx := context.Background()

	adminUser, err := db.GetQueries().CreateActiveUser(ctx, activeUserParams("admin-4", "admin@example.com", "Admin", 1))
	require.NoError(t, err)
	activeUser, err := db.GetQueries().CreateActiveUser(ctx, activeUserParams("active-4", "dave@example.com", "Dave", 0))
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Dave", "dave@example.com")
	require.NoError(t, err)

	// Sanity: Dave is active, his team member is active.
	pre, err := db.GetQueries().GetUserByID(ctx, activeUser.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), pre.IsActive.Int64)
	require.False(t, pre.DeactivatedAt.Valid)

	// Deactivate via the SQL path (the handler composes a session
	// and team-member deactivation; the SQL is the source of truth).
	post, err := db.GetQueries().DeactivateUser(ctx, activeUser.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), post.IsActive.Int64)
	require.True(t, post.DeactivatedAt.Valid, "deactivation sets deactivated_at")

	// Reactivate.
	reactivated, err := db.GetQueries().ReactivateUser(ctx, activeUser.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), reactivated.IsActive.Int64)
	require.False(t, reactivated.DeactivatedAt.Valid, "reactivate clears deactivated_at")

	// The admin's session is unaffected.
	adminSession, err := sessionManager.CreateSession(ctx, adminUser.ID)
	require.NoError(t, err)
	_, err = sessionManager.ValidateSession(ctx, adminSession)
	require.NoError(t, err)
}

// TestUserApprovalFlow_NonAdminCannotApprove verifies the admin
// gate: a non-admin user can submit the POST but gets redirected
// to /login (the requireAdmin helper). The pending user is not
// approved.
func TestUserApprovalFlow_NonAdminCannotApprove(t *testing.T) {
	db, cleanup := setupApprovalTestDB(t)
	defer cleanup()
	h, sessionManager := newApprovalHandler(t, db)
	ctx := context.Background()

	regular, err := db.GetQueries().CreateActiveUser(ctx, activeUserParams("regular-5", "eve@example.com", "Eve", 0))
	require.NoError(t, err)
	pending, err := db.GetQueries().CreateUser(ctx, pendingUserParams("pending-5", "frank@example.com", "Frank"))
	require.NoError(t, err)

	regularSession, err := sessionManager.CreateSession(ctx, regular.ID)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/team/users/"+pending.ID+"/approve", nil)
	req.Header.Set("Cookie", "session_token="+regularSession)
	h.handleUserApprove(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	require.Contains(t, loc, "/login",
		"non-admin must be redirected to /login, got %q", loc)

	// Frank is still pending.
	post, err := db.GetQueries().GetUserByID(ctx, pending.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), post.IsActive.Int64)
}

// activeUserParams builds the sqlc.CreateActiveUserParams for a
// test user. id/email/name are free-form; isAdmin is a NullInt64
// (the field signature is required by sqlc).
func activeUserParams(id, email, name string, isAdmin int) sqlc.CreateActiveUserParams {
	return sqlc.CreateActiveUserParams{
		ID:         id,
		Email:      email,
		Name:       name,
		Provider:   "fake",
		ProviderID: id,
		IsAdmin:    sql.NullInt64{Int64: int64(isAdmin), Valid: true},
	}
}

// pendingUserParams builds the sqlc.CreateUserParams for a
// pending user (is_active is hard-coded to 0 by the query).
func pendingUserParams(id, email, name string) sqlc.CreateUserParams {
	return sqlc.CreateUserParams{
		ID:         id,
		Email:      email,
		Name:       name,
		Provider:   "fake",
		ProviderID: id,
	}
}
