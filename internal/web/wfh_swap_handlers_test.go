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
	"github.com/inful/madhatter/internal/database"
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

// ---------------------------------------------------------------------------
// Step 20 (plans/assigned-wfh-plan.md) — WFH swap notification wiring.
//
// The four swap event kinds (SwapRequested / SwapAccepted /
// SwapRejected / SwapCancelled) were already wired to the
// HAT-swap path. Phase 4 wires them to the WFH-swap path too.
// A future regression test must fail if any of these
// dispatches silently disappears — the target would otherwise
// never learn a swap is pending on their day.
// ---------------------------------------------------------------------------

// setupWFHSwapWithPendingSwap seeds an assigned WFH row for
// alice and a pending swap from alice to bob. Returns swapID
// for further mutations.
func setupWFHSwapWithPendingSwap(t *testing.T, ctx context.Context, db *database.DB) (swapID, aliceID, bobID string) {
	t.Helper()
	aliceID, err := db.AddTeamMember(ctx, "alice@example.com", "alice@example.com")
	require.NoError(t, err)
	bobID, err = db.AddTeamMember(ctx, "bob@example.com", "bob@example.com")
	require.NoError(t, err)

	date := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
	assignedID := "assigned-" + t.Name()
	_, err = db.ExecContext(ctx,
		`INSERT INTO wfh_requests (id, member_id, date, status, origin)
		 VALUES (?, ?, ?, 'approved', 'assigned')`,
		assignedID, aliceID, date)
	require.NoError(t, err)

	swapID, err = db.CreateWFHAssignmentSwap(ctx, assignedID, bobID, date)
	require.NoError(t, err)
	return swapID, aliceID, bobID
}

// TestHandleWFHSwapCreate_FiresSwapRequested is the Step 20
// wiring regression for the WFH-swap create path. A valid submit
// must dispatch exactly one SwapRequested with the right
// requester / target / dates so the target learns the swap is
// pending.
func TestHandleWFHSwapCreate_FiresSwapRequested(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, _ := db.AddTeamMember(ctx, "alice@example.com", "alice@example.com")
	bobID, err := db.AddTeamMember(ctx, "bob@example.com", "bob@example.com")
	require.NoError(t, err)

	date := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
	assignedID := "assigned-create-notify"
	_, err = db.ExecContext(ctx,
		`INSERT INTO wfh_requests (id, member_id, date, status, origin)
		 VALUES (?, ?, ?, 'approved', 'assigned')`,
		assignedID, aliceID, date)
	require.NoError(t, err)

	h := newSwapHandler(t, db)
	h.wfhService = wfh.NewService(db, wfh.Config{Enabled: true, SeatCap: 2, AssignmentEnabled: true})
	notifier := &fakeNotifier{}
	h.notifier = notifier

	form := url.Values{}
	form.Set("target_member_id", bobID)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/wfh/"+assignedID+"/swap", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, "alice@example.com", "Alice", false)
	req = withChiParam(req, assignedID)
	rr := httptest.NewRecorder()
	h.handleWFHSwapCreate(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code,
		"valid submit must redirect with the success flash")
	assert.Equal(t, 1, notifier.swapRequested,
		"valid submit must dispatch exactly one SwapRequested")
	assert.Equal(t, 0, notifier.swapAccepted,
		"no accept event should fire from the create path")
	assert.Equal(t, aliceID, notifier.lastSwapEvent.RequesterMemberID,
		"SwapEvent.requester must be the assigned member (alice), not the target")
	assert.Equal(t, bobID, notifier.lastSwapEvent.TargetMemberID,
		"SwapEvent.target must be the picked teammate (bob)")
	assert.Equal(t, date, notifier.lastSwapEvent.RequesterDate,
		"SwapEvent.requesterDate must surface the WFH row's date")
	assert.Equal(t, date, notifier.lastSwapEvent.TargetDate,
		"SwapEvent.targetDate should mirror the swap date")
}

// TestHandleWFHSwapAccept_FiresSwapAccepted pins the accept
// path: the requester gets a "your swap was accepted" email.
func TestHandleWFHSwapAccept_FiresSwapAccepted(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	swapID, aliceID, bobID := setupWFHSwapWithPendingSwap(t, ctx, db)

	h := newSwapHandler(t, db)
	h.wfhService = wfh.NewService(db, wfh.Config{Enabled: true})
	notifier := &fakeNotifier{}
	h.notifier = notifier

	// setSwapIDParam mirrors the local helpers in the
	// pre-existing tests above — duplicated here because Go
	// doesn't share a closure across test functions in this
	// file. Sets the chi URL parameter "swapId" so the
	// handler can locate the swap row.
	setSwapIDParam := func(r *http.Request, value string) *http.Request {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("swapId", value)
		return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/wfh/swap/"+swapID+"/accept", nil)
	req = setSwapIDParam(req, swapID)
	req = withUser(req, "bob@example.com", "Bob", false)
	rr := httptest.NewRecorder()
	h.transitionWFHSwap(rr, req, "accepted", "/wfh/swap/inbox?flash=swap_accepted")

	require.Equal(t, http.StatusSeeOther, rr.Code)
	assert.Equal(t, 1, notifier.swapAccepted,
		"accept must dispatch exactly one SwapAccepted")
	assert.Equal(t, 0, notifier.swapRejected,
		"accept must not dispatch SwapRejected")
	assert.Equal(t, 0, notifier.swapCancelled,
		"accept must not dispatch SwapCancelled")
	assert.Equal(t, aliceID, notifier.lastSwapEvent.RequesterMemberID,
		"SwapAccepted must surface the requester (alice)")
	assert.Equal(t, bobID, notifier.lastSwapEvent.TargetMemberID,
		"SwapAccepted must surface the target (bob)")
}

// TestHandleWFHSwapReject_FiresSwapRejected pins the reject
// path: the requester gets a "your swap was rejected" email.
func TestHandleWFHSwapReject_FiresSwapRejected(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	swapID, aliceID, bobID := setupWFHSwapWithPendingSwap(t, ctx, db)

	h := newSwapHandler(t, db)
	h.wfhService = wfh.NewService(db, wfh.Config{Enabled: true})
	notifier := &fakeNotifier{}
	h.notifier = notifier

	setSwapIDParam := func(r *http.Request, value string) *http.Request {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("swapId", value)
		return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/wfh/swap/"+swapID+"/reject", nil)
	req = setSwapIDParam(req, swapID)
	req = withUser(req, "bob@example.com", "Bob", false)
	rr := httptest.NewRecorder()
	h.transitionWFHSwap(rr, req, "rejected", "/wfh/swap/inbox?flash=swap_rejected")

	require.Equal(t, http.StatusSeeOther, rr.Code)
	assert.Equal(t, 1, notifier.swapRejected,
		"reject must dispatch exactly one SwapRejected")
	assert.Equal(t, 0, notifier.swapAccepted,
		"reject must not dispatch SwapAccepted")
	assert.Equal(t, aliceID, notifier.lastSwapEvent.RequesterMemberID)
	assert.Equal(t, bobID, notifier.lastSwapEvent.TargetMemberID)
}

// TestHandleWFHSwapCancel_FiresSwapCancelled pins the cancel
// path: the target gets a "swap cancelled, no action needed"
// email when the requester voluntarily cancels.
func TestHandleWFHSwapCancel_FiresSwapCancelled(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	swapID, aliceID, bobID := setupWFHSwapWithPendingSwap(t, ctx, db)

	h := newSwapHandler(t, db)
	h.wfhService = wfh.NewService(db, wfh.Config{Enabled: true})
	notifier := &fakeNotifier{}
	h.notifier = notifier

	setSwapIDParam := func(r *http.Request, value string) *http.Request {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("swapId", value)
		return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	}

	// The cancel handler enforces that the requester (alice)
	// is the caller.
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/wfh/swap/"+swapID+"/cancel", nil)
	req = setSwapIDParam(req, swapID)
	req = withUser(req, "alice@example.com", "Alice", false)
	rr := httptest.NewRecorder()
	h.handleWFHSwapCancel(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	assert.Equal(t, 1, notifier.swapCancelled,
		"cancel must dispatch exactly one SwapCancelled")
	assert.Equal(t, 0, notifier.swapRequested,
		"cancel must not dispatch SwapRequested")
	assert.Equal(t, aliceID, notifier.lastSwapEvent.RequesterMemberID)
	assert.Equal(t, bobID, notifier.lastSwapEvent.TargetMemberID)
}
