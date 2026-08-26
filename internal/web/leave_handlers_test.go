package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/rota"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleLeaveManagement_RegularUserSeesOnlyOwn asserts the
// /leave/manage page is accessible to a regular (non-admin) user
// and shows only that user's leave entries. Admins continue to see
// every team member's entries.
//
// The route is mounted under the protected auth-only group in
// routes.go; the handler narrows the result set to the session
// member's own rows when the caller is not an admin.
func TestHandleLeaveManagement_RegularUserSeesOnlyOwn(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, aliceID, "2026-09-01", "2026-09-03", database.LeaveTypeLeave)
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, bobID, "2026-09-10", "2026-09-12", database.LeaveTypeLeave)
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, bobID, "2026-09-20", "2026-09-22", database.LeaveTypeLeave)
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
	_, err = db.CreateLeaveRecord(ctx, aliceID, "2026-09-01", "2026-09-03", database.LeaveTypeLeave)
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, bobID, "2026-09-10", "2026-09-12", database.LeaveTypeLeave)
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

// TestHandleLeaveReport_RawHTTPRejectsEscalation is the defense-
// in-depth safety net: it constructs a raw HTTP POST (no UI
// involved) and asserts the backend rejects a non-admin attempt
// to file a leave for someone else. The other tests in this file
// also exercise the protection but go through the user form; this
// test demonstrates the protection holds when a non-admin
// attacker writes a curl-equivalent request directly. If the
// handler ever loosens the rule, this test fails.
func TestHandleLeaveReport_RawHTTPRejectsEscalation(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	// Raw HTTP body Alice would send via curl with cookies:
	//   curl -X POST http://host/leave/report \
	//        -d 'member_id=<bob>&start_date=2026-10-01&end_date=2026-10-03' \
	//        --cookie 'session_token=...'
	form := "member_id=" + bobID + "&start_date=2026-10-01&end_date=2026-10-03"
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/leave/report", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, "alice@example.com", "Alice", false)
	rr := httptest.NewRecorder()
	h.handleLeaveReportPost(rr, req, map[string]any{"IsAdmin": false})

	require.Equal(t, http.StatusSeeOther, rr.Code,
		"POST should succeed (with coerced member_id), not 4xx")

	// Defense-in-depth: even if the response is 303, the only row
	// written MUST be Alice's, not Bob's. A bug that lets the form
	// value through would put a row under Bob — assert that didn't
	// happen.
	rows, err := db.GetLeaveRecords(ctx)
	require.NoError(t, err)
	for _, l := range rows {
		assert.NotEqual(t, bobID, l.MemberID,
			"raw HTTP form value must NOT be honored for non-admin")
		require.Equal(t, aliceID, l.MemberID,
			"only the session member's row may be created")
	}
}

// TestHandleLeaveReport_NonAdminForcesSelfMemberID asserts that a
// non-admin POSTing /leave/report with someone else's member_id is
// silently re-routed to their own member_id. The protection is
// silent (no 403) so a casual user who picks the wrong member from
// the form doesn't see an error — but the DB write is locked to
// the session's own member.
func TestHandleLeaveReport_NonAdminForcesSelfMemberID(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	// Alice (non-admin) submits the form with Bob's member_id.
	// The handler must force this to Alice's own id, leaving
	// Bob's row count unchanged.
	form := "member_id=" + bobID + "&start_date=2026-10-01&end_date=2026-10-03"
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/leave/report", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, "alice@example.com", "Alice", false)
	rr := httptest.NewRecorder()
	h.handleLeaveReportPost(rr, req, map[string]any{"IsAdmin": false})

	require.Equal(t, http.StatusSeeOther, rr.Code,
		"non-admin POST must succeed (with member_id coerced to self)")

	// Bob must still have zero leave rows — Alice's tampered form
	// value did not create a row on Bob's behalf.
	bobs, err := db.GetLeaveRecords(ctx)
	require.NoError(t, err)
	for _, l := range bobs {
		assert.NotEqual(t, bobID, l.MemberID,
			"non-admin must not be able to create a leave for Bob")
	}

	// Alice must have exactly one new row.
	alices, err := db.GetLeaveRecords(ctx)
	require.NoError(t, err)
	require.Len(t, alices, 1, "Alice should have one leave row after the POST")
	assert.Equal(t, aliceID, alices[0].MemberID,
		"member_id on the created row must be Alice's, not Bob's")
}

// TestHandleLeaveReport_AdminMemberIDRespected asserts the
// admin path: an admin POSTing with Bob's member_id creates a row
// for Bob (the form value is honored). Pairs with the non-admin
// test above as a positive control.
func TestHandleLeaveReport_AdminMemberIDRespected(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	form := "member_id=" + bobID + "&start_date=2026-10-01&end_date=2026-10-03"
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/leave/report", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, "admin@example.com", "Admin", true)
	rr := httptest.NewRecorder()
	h.handleLeaveReportPost(rr, req, map[string]any{"IsAdmin": true})

	require.Equal(t, http.StatusSeeOther, rr.Code)

	rows, err := db.GetLeaveRecords(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1, "admin must create one row")
	assert.Equal(t, bobID, rows[0].MemberID,
		"admin POST with Bob's id must create a row for Bob")
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
	_, err = db.CreateLeaveRecord(ctx, bobID, "2026-09-10", "2026-09-12", database.LeaveTypeLeave)
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
	_, err = db.CreateLeaveRecord(ctx, bobID, "2026-09-10", "2026-09-12", database.LeaveTypeLeave)
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
// grep, and a real ScheduleMaintenance so the report flow can run
// end-to-end without panicking on a nil maintenance.
func setupLeaveTestDB(t *testing.T) (*database.DB, *Handler, func()) {
	t.Helper()
	db, err := database.New(t.TempDir() + "/leave_test.db")
	require.NoError(t, err)
	tmpl, err := parseTemplates()
	require.NoError(t, err)
	h := &Handler{
		db:             db,
		authMiddleware: &auth.Middleware{},
		tmpl:           tmpl,
		maintenance:    rota.NewScheduleMaintenance(db),
	}
	return db, h, func() { _ = db.Close() }
}

// TestHandleLeaveEdit_RawHTTPRejectsEscalation is the
// defense-in-depth safety net for the edit path. It constructs a
// raw POST (httptest equivalent of curl) and asserts that the
// backend blocks a non-admin attempt to mutate another member's
// leave row. The handler's auth check happens BEFORE form
// parsing, so a tampered form value is never even read. If the
// canMutateLeave check is ever loosened, this test fails.
func TestHandleLeaveEdit_RawHTTPRejectsEscalation(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, bobID, "2026-09-10", "2026-09-12", database.LeaveTypeLeave)
	require.NoError(t, err)
	rows, err := db.GetLeaveRecords(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	bobLeaveID := rows[0].ID

	// The raw HTTP body Alice (non-admin) would send via curl. Note
	// the `member_id=Bob's-id` field — the attacker expects the
	// handler to honor it, which would re-route the row to Bob.
	body := "member_id=" + bobID + "&start_date=2099-01-01&end_date=2099-01-02"
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/leave/"+bobLeaveID+"/edit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, "alice@example.com", "Alice", false)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", bobLeaveID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))

	rr := httptest.NewRecorder()
	h.handleLeaveEdit(rr, req)

	// Defense-in-depth: 403, AND the row is unchanged on disk.
	assert.Equal(t, http.StatusForbidden, rr.Code,
		"raw HTTP edit of another member's leave must be rejected")
	post, err := db.GetLeaveByID(ctx, bobLeaveID)
	require.NoError(t, err)
	assert.Equal(t, "2026-09-10", post.StartDate.Format("2006-01-02"),
		"raw HTTP edit must not have changed Bob's start_date")
	assert.Equal(t, "2026-09-12", post.EndDate.Format("2006-01-02"),
		"raw HTTP edit must not have changed Bob's end_date")
	assert.Equal(t, bobID, post.MemberID,
		"raw HTTP edit must not have changed Bob's member_id")
}

// TestHandleLeaveDelete_RawHTTPRejectsEscalation is the
// defense-in-depth safety net for the delete path. Pairs with the
// edit raw-HTTP test above; covers the surface the attacker would
// probe first since DELETE is the highest-impact mutation.
func TestHandleLeaveDelete_RawHTTPRejectsEscalation(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, bobID, "2026-09-10", "2026-09-12", database.LeaveTypeLeave)
	require.NoError(t, err)
	rows, err := db.GetLeaveRecords(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	bobLeaveID := rows[0].ID

	// Raw DELETE attempt from Alice against Bob's row. The handler's
	// auth check happens before any DB write.
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/leave/"+bobID+"/delete", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", bobLeaveID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	req = withUser(req, "alice@example.com", "Alice", false)
	rr := httptest.NewRecorder()
	h.handleLeaveDelete(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code,
		"raw HTTP delete of another member's leave must be rejected")

	// Defense-in-depth: the row must still be on disk.
	post, err := db.GetLeaveByID(ctx, bobLeaveID)
	require.NoError(t, err)
	require.NotNil(t, post, "raw HTTP delete must not have removed Bob's row")
	assert.Equal(t, bobID, post.MemberID,
		"raw HTTP delete must not have changed Bob's member_id")
}

// TestHandleLeaveManagement_NonAdminSeesEditDeleteForOwnLeave is
// the positive counterpart to TestHandleLeaveManagement_RegularUserSeesOnlyOwn:
// once a non-admin can reach the page, they must also be able to
// act on their own rows. The backend already authorizes edits and
// deletes for the row owner via canMutateLeave (see
// TestHandleLeaveEdit_NonAdminRejectsOthersLeave), but the
// template was gating the per-row edit/delete buttons behind
// IsAdmin only — so a non-admin saw their leave records but no
// way to manage them. This test renders the page as a non-admin
// and asserts the row-level action buttons are present.
//
// Because the handler narrows the result set to the session
// member's rows for non-admins, every rendered row is by
// definition theirs, so the row-level guard in the template
// (`or $.IsAdmin (eq .Leave.MemberID $.SelfMemberID)`) always
// succeeds for them. The test still exercises the path so a
// future change that re-gates the buttons under IsAdmin would
// fail loudly.
func TestHandleLeaveManagement_NonAdminSeesEditDeleteForOwnLeave(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, aliceID, "2026-09-01", "2026-09-03", database.LeaveTypeLeave)
	require.NoError(t, err)

	rec := httptest.NewRequestWithContext(ctx, http.MethodGet, "/leave/manage", nil)
	rec = withUser(rec, "alice@example.com", "Alice", false)
	rr := httptest.NewRecorder()
	h.handleLeaveManagement(rr, rec)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	assert.Contains(t, body, `aria-label="Edit leave record"`,
		"non-admin must see an Edit button on their own leave row")
	assert.Contains(t, body, `aria-label="Delete leave record"`,
		"non-admin must see a Delete button on their own leave row")
}

// TestHandleLeaveManagement_NonAdminOmitsTeamPicker asserts that
// the edit modal renders the hidden-input member id for a
// non-admin rather than the admin-only team-member picker. The
// team picker would let a non-admin re-target a leave to another
// member — a privilege escalation vector that the backend
// already blocks via parseLeaveEditForm forcing member_id to
// self. Removing the picker from the non-admin view is the
// defense-in-depth half of that contract: an attacker shouldn't
// even see the UI affordance for the action that's about to be
// refused.
func TestHandleLeaveManagement_NonAdminOmitsTeamPicker(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, aliceID, "2026-09-01", "2026-09-03", database.LeaveTypeLeave)
	require.NoError(t, err)

	rec := httptest.NewRequestWithContext(ctx, http.MethodGet, "/leave/manage", nil)
	rec = withUser(rec, "alice@example.com", "Alice", false)
	rr := httptest.NewRecorder()
	h.handleLeaveManagement(rr, rec)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	assert.Contains(t, body, `id="editMemberID"`,
		"edit modal must still include the member_id input (hidden for non-admin)")
	assert.Contains(t, body, `name="member_id"`,
		"edit form must still submit a member_id field")
	// The admin-only path uses <select id="editMemberID" name="member_id">
	// — the non-admin path uses <input type="hidden" id="editMemberID" ...>.
	// Asserting against the literal `<select` substring catches a
	// regression that re-adds the picker for non-admins.
	assert.NotContains(t, body, `<select id="editMemberID"`,
		"non-admin must not see the team-member picker in the edit modal")
}

// TestHandleLeaveEdit_NonAdminEditsOwnLeave is the positive
// counterpart to TestHandleLeaveEdit_RawHTTPRejectsEscalation.
// The defense-in-depth test asserts the backend refuses a
// non-admin who tampers with the member_id field to escalate;
// this test asserts the non-admin happy path — editing their own
// leave via raw HTTP — actually succeeds. Without it, a future
// refactor that locks the edit handler down to admins-only
// would still pass the rejection test but break the user-visible
// feature the dashboard button now advertises.
func TestHandleLeaveEdit_NonAdminEditsOwnLeave(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, aliceID, "2026-09-01", "2026-09-03", database.LeaveTypeLeave)
	require.NoError(t, err)
	rows, err := db.GetLeaveRecords(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	aliceLeaveID := rows[0].ID

	// Alice (non-admin) edits her own row to a new range. The
	// form body sends her own member_id (the only valid value
	// for a non-admin) and the new dates. The handler should
	// accept it, redirect back to /leave/manage, and persist the
	// change to the row on disk.
	form := "member_id=" + aliceID + "&start_date=2026-10-05&end_date=2026-10-07"
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/leave/"+aliceLeaveID+"/edit", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, "alice@example.com", "Alice", false)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", aliceLeaveID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))

	rr := httptest.NewRecorder()
	h.handleLeaveEdit(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code,
		"non-admin editing their own leave must redirect (303), body=%s", rr.Body.String())

	post, err := db.GetLeaveByID(ctx, aliceLeaveID)
	require.NoError(t, err)
	require.NotNil(t, post)
	assert.Equal(t, aliceID, post.MemberID,
		"member_id must remain Alice's (non-admin cannot re-route)")
	assert.Equal(t, "2026-10-05", post.StartDate.Format("2006-01-02"),
		"start_date must reflect the new value")
	assert.Equal(t, "2026-10-07", post.EndDate.Format("2006-01-02"),
		"end_date must reflect the new value")
}

// postSickLeaveForm drives handleLeaveReportSickPost with a real
// httptest.NewRequest form body. Mirrors how production code calls
// the handler: it computes today once and passes it in, simulates
// the auth context, and passes through a fully-formed data map so
// the re-render path can produce a valid form body. Keeping this
// helper local to the file means the tests stay readable without
// each one rebuilding the call shape.
func postSickLeaveForm(t *testing.T, h *Handler, form url.Values, userEmail string, isAdmin bool) *httptest.ResponseRecorder {
	t.Helper()
	today := time.Now().Format("2006-01-02")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/leave/report-sick", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, userEmail, "Test User", isAdmin)
	rr := httptest.NewRecorder()
	data := map[string]any{
		"Template": "leave_report_sick",
		"IsAdmin":  isAdmin,
	}
	h.handleLeaveReportSickPost(rr, req, data, today)
	return rr
}

// TestHandleLeaveReportSick_NonAdminRegistersForOther is the happy
// path: a non-admin user registers a leave row for a *different*
// team member, pinned to today's date. This is the user-visible
// feature the route was added for; without it the relax-the-create-
// path change is unproven.
func TestHandleLeaveReportSick_NonAdminRegistersForOther(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	today := time.Now().Format("2006-01-02")
	form := url.Values{}
	form.Set("member_id", bobID)
	form.Set("start_date", today)
	form.Set("end_date", today)

	rr := postSickLeaveForm(t, h, form, "alice@example.com", false)

	require.Equal(t, http.StatusSeeOther, rr.Code,
		"non-admin must be able to register a same-day leave for another member, body=%s", rr.Body.String())

	rows, err := db.GetLeaveByDate(ctx, today)
	require.NoError(t, err)
	require.Len(t, rows, 1, "exactly one leave row should land for today")
	assert.Equal(t, bobID, rows[0].MemberID,
		"the row must be filed under Bob, not Alice (the session member)")
	assert.Equal(t, today, rows[0].StartDate.Format("2006-01-02"))
	assert.Equal(t, today, rows[0].EndDate.Format("2006-01-02"),
		"start_date and end_date must both equal today")
	assert.Equal(t, database.LeaveTypeLeave, rows[0].LeaveType,
		"sick-leave rows must always be plain leave, not conference")
}

// TestHandleLeaveReportSick_AdminCanAlsoUse is the positive
// counterpart confirming admins can use the same route. There is no
// admin-only check on the path, so this is a smoke test that the
// GET form (visible to all) is reachable for an admin and the POST
// path creates a row under the picked member.
func TestHandleLeaveReportSick_AdminCanAlsoUse(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	today := time.Now().Format("2006-01-02")
	form := url.Values{}
	form.Set("member_id", bobID)
	form.Set("start_date", today)
	form.Set("end_date", today)
	form.Set("leave_type", database.LeaveTypeLeave)

	rr := postSickLeaveForm(t, h, form, "admin@example.com", true)

	require.Equal(t, http.StatusSeeOther, rr.Code,
		"admin POST must redirect with 303, body=%s", rr.Body.String())
	rows, err := db.GetLeaveByDate(ctx, today)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, bobID, rows[0].MemberID,
		"admin's POST must create a row under the picked member")
}

// TestHandleLeaveReportSick_FutureDateRejected pins the date lock:
// the whole point of this route is that a non-admin can only file a
// leave for *today*. A tampered form value for tomorrow must be
// refused and the DB must remain untouched.
func TestHandleLeaveReportSick_FutureDateRejected(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	today := time.Now().Format("2006-01-02")
	form := url.Values{}
	form.Set("member_id", bobID)
	form.Set("start_date", tomorrow)
	form.Set("end_date", tomorrow)
	form.Set("leave_type", database.LeaveTypeLeave)

	rr := postSickLeaveForm(t, h, form, "alice@example.com", false)

	// The handler re-renders the form on validation failure, so the
	// response is 200 with the form body — not a raw 4xx.
	assert.Equal(t, http.StatusOK, rr.Code,
		"future-date POST must re-render the form, body=%s", rr.Body.String())
	assert.Contains(t, rr.Body.String(), "must be today",
		"re-rendered form must surface the date-lock error")
	assert.NotContains(t, rr.Body.String(), "successfully",
		"re-rendered form must not signal success")

	// Defense in depth: nothing landed in the DB.
	rows, err := db.GetLeaveByDate(ctx, today)
	require.NoError(t, err)
	assert.Empty(t, rows, "rejected future-date POST must not create a leave row")
	rows, err = db.GetLeaveByDate(ctx, tomorrow)
	require.NoError(t, err)
	assert.Empty(t, rows, "rejected future-date POST must not create a leave row")
}

// TestHandleLeaveReportSick_PastDateRejected is the same lock test
// for the past: the form pre-populates today's date, so a non-admin
// who tampers with the hidden inputs to yesterday must also be
// refused.
func TestHandleLeaveReportSick_PastDateRejected(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	today := time.Now().Format("2006-01-02")
	form := url.Values{}
	form.Set("member_id", bobID)
	form.Set("start_date", yesterday)
	form.Set("end_date", yesterday)
	form.Set("leave_type", database.LeaveTypeLeave)

	rr := postSickLeaveForm(t, h, form, "alice@example.com", false)

	assert.Equal(t, http.StatusOK, rr.Code,
		"past-date POST must re-render the form, body=%s", rr.Body.String())
	rows, err := db.GetLeaveByDate(ctx, today)
	require.NoError(t, err)
	assert.Empty(t, rows, "rejected past-date POST must not create a leave row")
	rows, err = db.GetLeaveByDate(ctx, yesterday)
	require.NoError(t, err)
	assert.Empty(t, rows, "rejected past-date POST must not create a leave row")
}

// TestHandleLeaveReportSick_RangeRejected enforces the single-day
// constraint: a non-admin cannot file a multi-day leave row through
// this path. Admin backend (/leave/report) accepts ranges; this
// endpoint does not.
func TestHandleLeaveReportSick_RangeRejected(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	today := time.Now().Format("2006-01-02")
	form := url.Values{}
	form.Set("member_id", bobID)
	// start_date == end_date == today, but a tampered "end_date"
	// outside today would slip past the duplicate guard. The lock
	// test below uses start_date == today, end_date == today+1.
	form.Set("start_date", today)
	form.Set("end_date", time.Now().AddDate(0, 0, 2).Format("2006-01-02"))
	form.Set("leave_type", database.LeaveTypeLeave)

	rr := postSickLeaveForm(t, h, form, "alice@example.com", false)

	assert.Equal(t, http.StatusOK, rr.Code,
		"range POST must re-render the form, body=%s", rr.Body.String())
	assert.Contains(t, rr.Body.String(), "must be today",
		"re-rendered form must surface the date-lock error")
	rows, err := db.GetLeaveByDate(ctx, today)
	require.NoError(t, err)
	assert.Empty(t, rows, "rejected range POST must not create a leave row")
}

// TestHandleLeaveReportSick_DuplicateRejected ensures the
// duplicate-row guard: a second submission for (member, today) is
// refused so a non-admin can't accidentally stack leave rows by
// hitting submit twice.
func TestHandleLeaveReportSick_DuplicateRejected(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	today := time.Now().Format("2006-01-02")
	// Pre-seed a leave row for Bob/today.
	_, err = db.CreateLeaveRecord(ctx, bobID, today, today, database.LeaveTypeLeave)
	require.NoError(t, err)

	form := url.Values{}
	form.Set("member_id", bobID)
	form.Set("start_date", today)
	form.Set("end_date", today)

	rr := postSickLeaveForm(t, h, form, "alice@example.com", false)

	assert.Equal(t, http.StatusOK, rr.Code,
		"duplicate POST must re-render the form, body=%s", rr.Body.String())
	assert.Contains(t, rr.Body.String(), "already has a leave record",
		"re-rendered form must surface the duplicate-leave error")

	rows, err := db.GetLeaveByDate(ctx, today)
	require.NoError(t, err)
	require.Len(t, rows, 1,
		"duplicate POST must not stack a second leave row")
	assert.Equal(t, bobID, rows[0].MemberID)
}

// TestHandleLeaveReportSick_EmptyMemberRejected asserts the bare
// input-validation path: an empty member_id is rejected, no row is
// created. Without the guard, the downstream GetMemberByID would
// fail with a confusing message and leave orphan error handling
// elsewhere.
func TestHandleLeaveReportSick_EmptyMemberRejected(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	today := time.Now().Format("2006-01-02")
	form := url.Values{}
	form.Set("member_id", "")
	form.Set("start_date", today)
	form.Set("end_date", today)

	rr := postSickLeaveForm(t, h, form, "alice@example.com", false)

	assert.Equal(t, http.StatusOK, rr.Code,
		"empty-member POST must re-render the form, body=%s", rr.Body.String())
	assert.Contains(t, rr.Body.String(), "team member is required",
		"re-rendered form must surface the missing-member error")
	rows, err := db.GetLeaveByDate(ctx, today)
	require.NoError(t, err)
	assert.Empty(t, rows, "empty-member POST must not create a leave row")
}

// TestHandleLeaveReportSick_UnknownMemberRejected asserts the
// member-exists guard: a member_id that doesn't match a real
// team-member row is rejected with a friendly form error and the
// DB must not be touched.
func TestHandleLeaveReportSick_UnknownMemberRejected(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	today := time.Now().Format("2006-01-02")
	form := url.Values{}
	form.Set("member_id", "not-a-real-member-id")
	form.Set("start_date", today)
	form.Set("end_date", today)

	rr := postSickLeaveForm(t, h, form, "alice@example.com", false)

	assert.Equal(t, http.StatusOK, rr.Code,
		"unknown-member POST must re-render the form, body=%s", rr.Body.String())
	assert.Contains(t, rr.Body.String(), "not found",
		"re-rendered form must surface the unknown-member error")
	rows, err := db.GetLeaveByDate(ctx, today)
	require.NoError(t, err)
	assert.Empty(t, rows, "unknown-member POST must not create a leave row")
}

// TestHandleLeaveReportSick_RawHTTPRejectsMissingSession is the
// defense-in-depth safety net for the route's create-on-behalf-of
// path. Per AGENTS.md, a missing session is a hard 401 — never let
// the form path run without one. Even though the safeRequireAuth
// middleware guarantees this in production, a future refactor
// could move the route out of that group; this test pins the
// handler-level check.
func TestHandleLeaveReportSick_RawHTTPRejectsMissingSession(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	today := time.Now().Format("2006-01-02")
	form := url.Values{}
	form.Set("member_id", bobID)
	form.Set("start_date", today)
	form.Set("end_date", today)

	// Build the raw HTTP request WITHOUT calling withUser — the
	// session context is intentionally empty, simulating a request
	// that bypassed the middleware (e.g. a misconfigured route
	// move, a future test fixture, or a defense-bypassing curl).
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/leave/report-sick", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rr := httptest.NewRecorder()
	h.handleLeaveReportSickPost(rr, req, map[string]any{"Template": "leave_report_sick"}, today)

	require.Equal(t, http.StatusUnauthorized, rr.Code,
		"raw HTTP without a session must be rejected with 401, body=%s", rr.Body.String())

	// Defense in depth: no row was created on disk.
	rows, err := db.GetLeaveByDate(ctx, today)
	require.NoError(t, err)
	assert.Empty(t, rows, "401 path must not create a leave row")
}

// TestHandleLeaveReportSick_RawHTTPLeavesNoRowOnBadDate is the
// twin of TestHandleLeaveReportSick_RawHTTPRejectsMissingSession:
// even when the session is present, the date lock is a real
// guarantee — a tampered future-date form value must not create a
// row on disk. The test constructs the raw HTTP request the way an
// attacker would (curl-equivalent) and verifies both the response
// shape and the DB-after state.
func TestHandleLeaveReportSick_RawHTTPLeavesNoRowOnBadDate(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	today := time.Now().Format("2006-01-02")
	body := "member_id=" + bobID +
		"&start_date=" + tomorrow +
		"&end_date=" + tomorrow
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/leave/report-sick", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, "alice@example.com", "Alice", false)

	rr := httptest.NewRecorder()
	h.handleLeaveReportSickPost(rr, req, map[string]any{"Template": "leave_report_sick"}, today)

	// Form re-render for validation failures, not a 4xx.
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "must be today",
		"re-rendered form must show the date-lock error")

	// No row should exist for today OR for tomorrow — the lock
	// must block the write on either side of the date boundary.
	rows, err := db.GetLeaveByDate(ctx, today)
	require.NoError(t, err)
	assert.Empty(t, rows, "date-lock must leave today empty")
	rows, err = db.GetLeaveByDate(ctx, tomorrow)
	require.NoError(t, err)
	assert.Empty(t, rows, "date-lock must leave tomorrow empty")
}

// TestHandleLeaveReportSick_GetFormIsAuthenticatedOnly is the
// route-mounting safety net. The route sits inside the protected
// auth-only middleware group in routes.go — this test asserts the
// form renders an empty page via the GET dispatcher when there is
// a user in the context, that today's date is pre-populated
// server-side (not left for the user to pick), and that the form
// does NOT expose a leave_type selector: this path always files
// plain leave so a future drift that re-adds the dropdown — or
// re-introduces display of the member's email in the picker —
// fails loudly here.
func TestHandleLeaveReportSick_GetFormRendersWithTodayLocked(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	rec := httptest.NewRequestWithContext(ctx, http.MethodGet, "/leave/report-sick", nil)
	rec = withUser(rec, "alice@example.com", "Alice", false)
	rr := httptest.NewRecorder()
	h.handleLeaveReportSick(rr, rec)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	today := time.Now().Format("2006-01-02")
	assert.Contains(t, body, today,
		"GET form must render today's date so the user can see the lock")
	assert.Contains(t, body, `name="member_id"`,
		"GET form must include the member picker for non-admins")
	assert.NotContains(t, body, `name="leave_type"`,
		"GET form must not expose a leave_type selector — this path always files plain leave")
	assert.Contains(t, body, ">Bob<",
		"member options must render the display name")
	assert.NotContains(t, body, "bob@example.com",
		"member options must not include the email address on this form")
}

// TestHandleLeaveReportSick_ConferenceOverride is the safety net
// for the "always plain leave" rule. Even if a tampered client
// POSTs leave_type=conference (or =anything), the row must land as
// plain leave — this route files absences for colleagues, not
// conference attendance.
func TestHandleLeaveReportSick_ConferenceOverride(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	today := time.Now().Format("2006-01-02")
	form := url.Values{}
	form.Set("member_id", bobID)
	form.Set("start_date", today)
	form.Set("end_date", today)
	form.Set("leave_type", database.LeaveTypeConference)

	rr := postSickLeaveForm(t, h, form, "alice@example.com", false)

	require.Equal(t, http.StatusSeeOther, rr.Code,
		"the row should land (form is otherwise valid); the row's leave_type is the override test, not the response code")
	rows, err := db.GetLeaveByDate(ctx, today)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, bobID, rows[0].MemberID)
	assert.Equal(t, database.LeaveTypeLeave, rows[0].LeaveType,
		"this route must always record plain leave, even when the POSTed leave_type is conference")
}
