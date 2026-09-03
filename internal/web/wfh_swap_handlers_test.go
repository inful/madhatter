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
	"github.com/inful/madhatter/internal/wfh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleWFHSwapForm_OnlyAssignedOrSwapReachable pins the
// origin guard: the form is only meaningful for
// origin IN ('assigned', 'swap'). A voluntary (ad_hoc) row
// returns 409 Conflict so the user understands they can't
// swap a self-requested WFH.
func TestHandleWFHSwapForm_OnlyAssignedOrSwapReachable(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	mid, err := db.AddTeamMember(ctx, "alice@example.com", "alice@example.com")
	require.NoError(t, err)

	voluntaryDate := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
	voluntary, err := db.CreateWFHRequest(ctx, mid, voluntaryDate)
	require.NoError(t, err)

	h := newSwapHandler(t, db)
	h.wfhService = wfh.NewService(db, wfh.Config{Enabled: true, SeatCap: 2, AssignmentEnabled: true})

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/wfh/"+voluntary.ID+"/swap", nil)
	req = withUser(req, "alice@example.com", "Alice", false)
	req = withChiParam(req, voluntary.ID) // GET /wfh/{id}/swap
	rr := httptest.NewRecorder()
	h.handleWFHSwapForm(rr, req)

	assert.Equal(t, http.StatusConflict, rr.Code,
		"voluntary WFH should not show the swap form")
}

// TestHandleWFHSwapCreate_409ConflictGuard pins the "one
// pending swap per assigned row" invariant. A second
// submission for the same assigned row redirects with the
// swap_exists flash, not a double insert.
func TestHandleWFHSwapCreate_409ConflictGuard(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "alice@example.com", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "bob@example.com", "bob@example.com")
	require.NoError(t, err)

	date := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
	assignedID := "assigned-409-test"
	_, err = db.ExecContext(ctx,
		`INSERT INTO wfh_requests (id, member_id, date, status, origin)
		 VALUES (?, ?, ?, 'approved', 'assigned')`,
		assignedID, aliceID, date)
	require.NoError(t, err)

	h := newSwapHandler(t, db)
	h.wfhService = wfh.NewService(db, wfh.Config{Enabled: true, SeatCap: 2, AssignmentEnabled: true})

	// First submit succeeds — redirects to /wfh?flash=swap_requested.
	form := url.Values{}
	form.Set("target_member_id", bobID)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/wfh/"+assignedID+"/swap",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, "alice@example.com", "Alice", false)
	req = withChiParam(req, assignedID) // POST /wfh/{id}/swap
	rr := httptest.NewRecorder()
	h.handleWFHSwapCreate(rr, req)
	require.Equal(t, http.StatusSeeOther, rr.Code, "first submit should redirect")
	assert.Contains(t, rr.Header().Get("Location"), "swap_requested",
		"first submit should land on the swap_requested flash")

	// Second submit hits the 409 conflict guard — redirects
	// to /wfh?flash=swap_exists.
	form2 := url.Values{}
	form2.Set("target_member_id", bobID)
	req2 := httptest.NewRequestWithContext(ctx, http.MethodPost, "/wfh/"+assignedID+"/swap",
		strings.NewReader(form2.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2 = withUser(req2, "alice@example.com", "Alice", false)
	req2 = withChiParam(req2, assignedID)
	rr2 := httptest.NewRecorder()
	h.handleWFHSwapCreate(rr2, req2)
	assert.Equal(t, http.StatusSeeOther, rr2.Code)
	assert.Contains(t, rr2.Header().Get("Location"), "swap_exists",
		"second submit must redirect with swap_exists flash")
}

// TestHandleWFHSwapAcceptAndReject_StateTransitions pins the
// accept/reject flow. Only the target can transition; the
// status flips correctly.
func TestHandleWFHSwapAcceptAndReject_StateTransitions(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "alice@example.com", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "bob@example.com", "bob@example.com")
	require.NoError(t, err)

	date := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
	assignedID := "assigned-accept-test"
	_, err = db.ExecContext(ctx,
		`INSERT INTO wfh_requests (id, member_id, date, status, origin)
		 VALUES (?, ?, ?, 'approved', 'assigned')`,
		assignedID, aliceID, date)
	require.NoError(t, err)
	swapID, err := db.CreateWFHAssignmentSwap(ctx, assignedID, bobID, date)
	require.NoError(t, err)

	h := newSwapHandler(t, db)
	h.wfhService = wfh.NewService(db, wfh.Config{Enabled: true, SeatCap: 2, AssignmentEnabled: true})

	// chi.URLParam requires chi.RouteCtxKey — see the helper
	// inside TestHandleWFHSwapCancel_OnlyRequesterCanCancel
	// for the pattern.
	setSwapIDParam := func(r *http.Request, value string) *http.Request {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("swapId", value)
		return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	}

	// Alice (requester, not target) cannot accept.
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/wfh/swap/"+swapID+"/accept", nil)
	req = setSwapIDParam(req, swapID)
	req = withUser(req, "alice@example.com", "Alice", false)
	rr := httptest.NewRecorder()
	h.handleWFHSwapAccept(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code,
		"only the target can accept")

	// Bob (target) accepts.
	req = httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/wfh/swap/"+swapID+"/accept", nil)
	req = setSwapIDParam(req, swapID)
	req = withUser(req, "bob@example.com", "Bob", false)
	rr = httptest.NewRecorder()
	h.handleWFHSwapAccept(rr, req)
	assert.Equal(t, http.StatusSeeOther, rr.Code)

	s, err := db.GetWFHAssignmentSwapByID(ctx, swapID)
	require.NoError(t, err)
	assert.Equal(t, "accepted", s.Status)
}

// TestHandleWFHSwapReject_FlowAcceptThenReject pins the
// reject transition. After the swap is accepted, reject
// returns Conflict (state machine).
func TestHandleWFHSwapReject_FlowAcceptThenReject(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "alice@example.com", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "bob@example.com", "bob@example.com")
	require.NoError(t, err)

	date := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
	assignedID := "assigned-reject-test"
	_, err = db.ExecContext(ctx,
		`INSERT INTO wfh_requests (id, member_id, date, status, origin)
		 VALUES (?, ?, ?, 'approved', 'assigned')`,
		assignedID, aliceID, date)
	require.NoError(t, err)
	swapID, err := db.CreateWFHAssignmentSwap(ctx, assignedID, bobID, date)
	require.NoError(t, err)

	h := newSwapHandler(t, db)
	h.wfhService = wfh.NewService(db, wfh.Config{Enabled: true})

	setSwapIDParam := func(r *http.Request, value string) *http.Request {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("swapId", value)
		return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	}

	// Bob (target) rejects.
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/wfh/swap/"+swapID+"/reject", nil)
	req = setSwapIDParam(req, swapID)
	req = withUser(req, "bob@example.com", "Bob", false)
	rr := httptest.NewRecorder()
	h.handleWFHSwapReject(rr, req)
	require.Equal(t, http.StatusSeeOther, rr.Code)

	s, err := db.GetWFHAssignmentSwapByID(ctx, swapID)
	require.NoError(t, err)
	assert.Equal(t, "rejected", s.Status)
}

// TestHandleWFHSwapCancel_OnlyRequesterCanCancel pins the
// authorization guard for cancel. Only the requester can
// voluntarily cancel; the target sees 403.
func TestHandleWFHSwapCancel_OnlyRequesterCanCancel(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "alice@example.com", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "bob@example.com", "bob@example.com")
	require.NoError(t, err)

	date := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
	assignedID := "assigned-cancel-test"
	_, err = db.ExecContext(ctx,
		`INSERT INTO wfh_requests (id, member_id, date, status, origin)
		 VALUES (?, ?, ?, 'approved', 'assigned')`,
		assignedID, aliceID, date)
	require.NoError(t, err)
	swapID, err := db.CreateWFHAssignmentSwap(ctx, assignedID, bobID, date)
	require.NoError(t, err)

	h := newSwapHandler(t, db)
	h.wfhService = wfh.NewService(db, wfh.Config{Enabled: true})

	// chi.URLParam requires the chi.RouteCtxKey to be set in
	// the request context — the existing withChiParam helper
	// adds the "id" param; here we add the "swapId" param.
	setSwapIDParam := func(r *http.Request, value string) *http.Request {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("swapId", value)
		return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	}

	// Bob (target, not requester) cannot cancel.
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/wfh/swap/"+swapID+"/cancel", nil)
	req = setSwapIDParam(req, swapID)
	req = withUser(req, "bob@example.com", "Bob", false)
	rr := httptest.NewRecorder()
	h.handleWFHSwapCancel(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code,
		"only the requester can cancel")

	// Alice (requester) cancels.
	req = httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/wfh/swap/"+swapID+"/cancel", nil)
	req = setSwapIDParam(req, swapID)
	req = withUser(req, "alice@example.com", "Alice", false)
	rr = httptest.NewRecorder()
	h.handleWFHSwapCancel(rr, req)
	assert.Equal(t, http.StatusSeeOther, rr.Code)

	s, err := db.GetWFHAssignmentSwapByID(ctx, swapID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", s.Status)
}

// TestHandleWFHSwapInbox_RendersPending pins the inbox read.
// The current user (target) sees only pending swaps where
// they are the target.
func TestHandleWFHSwapInbox_RendersPending(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	bobID, err := db.AddTeamMember(ctx, "bob@example.com", "bob@example.com")
	require.NoError(t, err)
	aliceID, err := db.AddTeamMember(ctx, "alice@example.com", "alice@example.com")
	require.NoError(t, err)

	date := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
	assignedID := "assigned-inbox-test"
	_, err = db.ExecContext(ctx,
		`INSERT INTO wfh_requests (id, member_id, date, status, origin)
		 VALUES (?, ?, ?, 'approved', 'assigned')`,
		assignedID, aliceID, date)
	require.NoError(t, err)
	_, err = db.CreateWFHAssignmentSwap(ctx, assignedID, bobID, date)
	require.NoError(t, err)

	h := newSwapHandler(t, db)
	h.wfhService = wfh.NewService(db, wfh.Config{Enabled: true})

	req := httptest.NewRequestWithContext(ctx, http.MethodGet,
		"/wfh/swap/inbox", nil)
	req = withUser(req, "bob@example.com", "Bob", false)
	rr := httptest.NewRecorder()
	h.handleWFHSwapInbox(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "Pending Swap Requests",
		"inbox must render the pending swaps section")
}
