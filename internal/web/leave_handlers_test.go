package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleLeaveManagement_RegularUserSeesOnlyOwn asserts the
// /leave/manage page is accessible to a regular (non-admin) user
// and shows only that user's leave entries. Admins continue to see
// every team member's entries.
//
// Today the page is mounted under the admin group in routes.go so a
// non-admin gets a 303 redirect to /login (or 403). After the fix
// the route moves to the auth-only group and the handler filters
// by MemberID for non-admins.
func TestHandleLeaveManagement_RegularUserSeesOnlyOwn(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, aliceID, "2026-09-01", "2026-09-03")
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, bobID, "2026-09-10", "2026-09-12")
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, bobID, "2026-09-20", "2026-09-22")
	require.NoError(t, err)

	rec := httptest.NewRequestWithContext(ctx, http.MethodGet, "/leave/manage", nil)
	rec = withUser(rec, "alice@example.com", "Alice", false)
	rr := httptest.NewRecorder()
	h.handleLeaveManagement(rr, rec)

	require.Equal(t, http.StatusOK, rr.Code, "non-admin must reach /leave/manage, body=%s", rr.Body.String())
	body := rr.Body.String()

	assert.Contains(t, body, "2026-09-01", "Alice's own leave should appear")
	assert.Contains(t, body, "2026-09-03", "Alice's own leave should appear")
	for _, hidden := range []string{"2026-09-10", "2026-09-12", "2026-09-20", "2026-09-22"} {
		assert.NotContains(t, body, hidden, "Bob's leave %s must not appear in Alice's view", hidden)
	}
}

// TestHandleLeaveManagement_AdminSeesAll asserts an admin session
// continues to see every team member's leave entries. This is the
// "don't regress the existing behavior" half of the contract.
func TestHandleLeaveManagement_AdminSeesAll(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, aliceID, "2026-09-01", "2026-09-03")
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, bobID, "2026-09-10", "2026-09-12")
	require.NoError(t, err)

	rec := httptest.NewRequestWithContext(ctx, http.MethodGet, "/leave/manage", nil)
	rec = withUser(rec, "admin@example.com", "Admin", true)
	rr := httptest.NewRecorder()
	h.handleLeaveManagement(rr, rec)

	require.Equal(t, http.StatusOK, rr.Code)
	for _, d := range []string{"2026-09-01", "2026-09-10"} {
		assert.Contains(t, rr.Body.String(), d, "admin must see all dates including %s", d)
	}
}

// TestHandleLeaveEdit_NonAdminRejectsOthersLeave asserts a non-admin
// can edit their own leave but the handler rejects edits to leave
// rows owned by other members. The protection is the only thing
// standing between a curious user and a privilege escalation.
func TestHandleLeaveEdit_NonAdminRejectsOthersLeave(t *testing.T) {
	ctx := context.Background()
	db, _, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, bobID, "2026-09-10", "2026-09-12")
	require.NoError(t, err)
	rows, err := db.GetLeaveRecords(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	bobLeaveID := rows[0].ID

	h := &Handler{db: db, authMiddleware: &auth.Middleware{}}

	// Alice (non-admin) tries to edit Bob's leave. The handler must
	// refuse — protecting the leave row from tampering by anyone
	// other than its owner or an admin.
	form := "member_id=" + bobID + "&start_date=2099-01-01&end_date=2099-01-02"
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/leave/"+bobLeaveID+"/edit",
		strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, "alice@example.com", "Alice", false)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", bobLeaveID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))

	rr := httptest.NewRecorder()
	h.handleLeaveEdit(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code,
		"non-admin must not be able to edit another member's leave")

	post, err := db.GetLeaveByID(ctx, bobLeaveID)
	require.NoError(t, err)
	assert.Equal(t, "2026-09-10", post.StartDate.Format("2006-01-02"))
	assert.Equal(t, "2026-09-12", post.EndDate.Format("2006-01-02"))
}

// TestHandleLeaveDelete_NonAdminRejectsOthersLeave is the delete
// counterpart of the edit test. Same protection, same rationale.
func TestHandleLeaveDelete_NonAdminRejectsOthersLeave(t *testing.T) {
	ctx := context.Background()
	db, _, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, bobID, "2026-09-10", "2026-09-12")
	require.NoError(t, err)
	rows, err := db.GetLeaveRecords(ctx)
	require.NoError(t, err)
	bobLeaveID := rows[0].ID

	h := &Handler{db: db, authMiddleware: &auth.Middleware{}}

	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/leave/"+bobLeaveID+"/delete", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", bobLeaveID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	req = withUser(req, "alice@example.com", "Alice", false)
	rr := httptest.NewRecorder()
	h.handleLeaveDelete(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code,
		"non-admin must not be able to delete another member's leave")

	post, err := db.GetLeaveByID(ctx, bobLeaveID)
	require.NoError(t, err)
	require.NotNil(t, post, "Bob's leave must still exist after a forbidden delete")
	assert.Equal(t, bobID, post.MemberID, "MemberID must not have been touched")
}

// setupLeaveTestDB is the file-scoped test DB helper. Mirrors the
// pattern in swap_handlers_test.go and wfh_handlers_test.go — file-
// backed so the leave ID is stable across queries within a single
// test invocation. The returned Handler has a parsed template so
// the leave-management page can render to a body the test can
// grep.
func setupLeaveTestDB(t *testing.T) (*database.DB, *Handler, func()) {
	t.Helper()
	db, err := database.New(t.TempDir() + "/leave_test.db")
	require.NoError(t, err)
	tmpl, err := parseTemplates()
	require.NoError(t, err)
	h := &Handler{db: db, authMiddleware: &auth.Middleware{}, tmpl: tmpl}
	return db, h, func() { _ = db.Close() }
}
