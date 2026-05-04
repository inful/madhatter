package web

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/database/sqlc"
	"github.com/inful/madhatter/internal/rota"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func setupSwapTestDB(t *testing.T) (*database.DB, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "swap_test.db")

	db, err := database.New(dbPath)
	require.NoError(t, err)

	return db, func() { _ = db.Close() }
}

func newSwapHandler(t *testing.T, db *database.DB) *Handler {
	t.Helper()

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	h.maintenance = rota.NewScheduleMaintenance(db)
	return h
}

// withUser injects a fake session row into the request context, simulating a
// logged-in user as the web auth middleware would do.
func withUser(r *http.Request, email, name string, isAdmin bool) *http.Request {
	admin := int64(0)
	if isAdmin {
		admin = 1
	}

	row := &sqlc.GetSessionByTokenRow{
		Email:   email,
		Name:    name,
		IsAdmin: sql.NullInt64{Int64: admin, Valid: true},
	}

	ctx := context.WithValue(r.Context(), auth.UserContextKey, row)
	return r.WithContext(ctx)
}

// withChiParam sets the chi URL parameter "id" on the request context.
func withChiParam(r *http.Request, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// seedSchedule generates a 14-day schedule starting from tomorrow so swap
// tests have future assignments to work with.
func seedSchedule(t *testing.T, db *database.DB) {
	t.Helper()

	eng := rota.NewEngine(db)
	start := time.Now().AddDate(0, 0, 1)
	end := start.AddDate(0, 0, 13)
	require.NoError(t, eng.GenerateSchedule(context.Background(), start, end))
}

// ---------------------------------------------------------------------------
// swapValidationErrorMessage — error mapping tests
// ---------------------------------------------------------------------------

func TestSwapValidationErrorMessage_KnownErrors(t *testing.T) {
	cases := []struct {
		err      error
		contains string
	}{
		{database.ErrSwapSameAssignment, "itself"},
		{database.ErrRequesterAssignmentNotFound, "not found"},
		{database.ErrTargetAssignmentNotFound, "not found"},
		{database.ErrSwapNotOwner, "your own"},
		{database.ErrSwapTargetSelf, "another member"},
		{database.ErrSwapRequesterDatePassed, "HAT day"},
		{database.ErrSwapTargetDatePassed, "target HAT day"},
	}

	for _, tc := range cases {
		msg := swapValidationErrorMessage(tc.err)
		assert.Contains(t, msg, tc.contains, "error: %v", tc.err)
	}
}

// ---------------------------------------------------------------------------
// handleSwaps GET
// ---------------------------------------------------------------------------

func TestHandleSwaps_NoAuth_Panics(t *testing.T) {
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	h := newSwapHandler(t, db)

	req := httptest.NewRequest(http.MethodGet, "/swaps", nil)
	w := httptest.NewRecorder()

	// safeRequireAuth middleware guarantees user is in context before the handler
	// runs. Calling the handler without it is a programming error and must panic.
	assert.Panics(t, func() {
		h.handleSwaps(w, req)
	})
}

func TestHandleSwaps_NotATeamMember_ShowsError(t *testing.T) {
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	h := newSwapHandler(t, db)

	req := httptest.NewRequest(http.MethodGet, "/swaps", nil)
	req = withUser(req, "nobody@example.com", "Nobody", false)
	w := httptest.NewRecorder()

	h.handleSwaps(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "not registered as a team member")
}

func TestHandleSwaps_TeamMember_RendersPage(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	h := newSwapHandler(t, db)

	req := httptest.NewRequest(http.MethodGet, "/swaps", nil)
	req = withUser(req, "alice@example.com", "Alice", false)
	w := httptest.NewRecorder()

	h.handleSwaps(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ---------------------------------------------------------------------------
// handleSwaps POST (create swap request)
// ---------------------------------------------------------------------------

func TestHandleSwaps_Post_MissingIDs_ShowsError(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	h := newSwapHandler(t, db)

	form := url.Values{}
	req := httptest.NewRequest(http.MethodPost, "/swaps", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, "alice@example.com", "Alice", false)
	w := httptest.NewRecorder()

	h.handleSwaps(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Please select both assignments")
}

func TestHandleSwaps_Post_SameIDs_ShowsError(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	seedSchedule(t, db)

	assignments, err := db.GetFutureAssignmentsForMember(ctx, aliceID)
	require.NoError(t, err)
	require.NotEmpty(t, assignments, "Alice must have at least one future assignment")

	aid := assignments[0].ID
	form := url.Values{
		"requester_assignment_id": {aid},
		"target_assignment_id":    {aid},
	}
	req := httptest.NewRequest(http.MethodPost, "/swaps", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, "alice@example.com", "Alice", false)
	w := httptest.NewRecorder()

	h := newSwapHandler(t, db)
	h.handleSwaps(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Cannot swap an assignment with itself")
}

func TestHandleSwaps_Post_NotOwner_ShowsError(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	seedSchedule(t, db)

	// Alice tries to use Bob's assignment as her own requester assignment.
	bobAssignments, err := db.GetFutureAssignmentsForMember(ctx, bobID)
	require.NoError(t, err)
	require.NotEmpty(t, bobAssignments)

	aliceAssignments, err := db.GetFutureAssignmentsForMember(ctx, aliceID)
	require.NoError(t, err)
	require.NotEmpty(t, aliceAssignments)

	form := url.Values{
		"requester_assignment_id": {bobAssignments[0].ID},
		"target_assignment_id":    {aliceAssignments[0].ID},
	}
	req := httptest.NewRequest(http.MethodPost, "/swaps", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, "alice@example.com", "Alice", false)
	w := httptest.NewRecorder()

	h := newSwapHandler(t, db)
	h.handleSwaps(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "You can only swap your own assignments")
}

func TestHandleSwaps_Post_ValidSwap_Redirects(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	seedSchedule(t, db)

	aliceAssignments, err := db.GetFutureAssignmentsForMember(ctx, aliceID)
	require.NoError(t, err)
	require.NotEmpty(t, aliceAssignments)

	bobAssignments, err := db.GetFutureAssignmentsForMember(ctx, bobID)
	require.NoError(t, err)
	require.NotEmpty(t, bobAssignments)

	form := url.Values{
		"requester_assignment_id": {aliceAssignments[0].ID},
		"target_assignment_id":    {bobAssignments[0].ID},
	}
	req := httptest.NewRequest(http.MethodPost, "/swaps", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, "alice@example.com", "Alice", false)
	w := httptest.NewRecorder()

	h := newSwapHandler(t, db)
	h.handleSwaps(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/swaps", w.Header().Get("Location"))
}

func TestHandleSwaps_Post_DuplicateSwap_ShowsError(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	seedSchedule(t, db)

	aliceAssignments, err := db.GetFutureAssignmentsForMember(ctx, aliceID)
	require.NoError(t, err)
	require.NotEmpty(t, aliceAssignments)

	bobAssignments, err := db.GetFutureAssignmentsForMember(ctx, bobID)
	require.NoError(t, err)
	require.NotEmpty(t, bobAssignments)

	aliceAID := aliceAssignments[0].ID
	bobAID := bobAssignments[0].ID

	// First swap — should succeed.
	_, err = db.CreateHatSwap(ctx, aliceAID, bobAID, aliceID, bobID)
	require.NoError(t, err)

	// Second swap with the same assignments — should fail.
	form := url.Values{
		"requester_assignment_id": {aliceAID},
		"target_assignment_id":    {bobAID},
	}
	req := httptest.NewRequest(http.MethodPost, "/swaps", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, "alice@example.com", "Alice", false)
	w := httptest.NewRecorder()

	h := newSwapHandler(t, db)
	h.handleSwaps(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "already has an open swap request")
}

func TestHandleSwaps_GetPendingOutgoingSwap_ShowsCancelButton(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	seedSchedule(t, db)

	aliceAssignments, err := db.GetFutureAssignmentsForMember(ctx, aliceID)
	require.NoError(t, err)
	require.NotEmpty(t, aliceAssignments)
	bobAssignments, err := db.GetFutureAssignmentsForMember(ctx, bobID)
	require.NoError(t, err)
	require.NotEmpty(t, bobAssignments)

	_, err = db.CreateHatSwap(ctx, aliceAssignments[0].ID, bobAssignments[0].ID, aliceID, bobID)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/swaps", nil)
	req = withUser(req, "alice@example.com", "Alice", false)
	w := httptest.NewRecorder()

	h := newSwapHandler(t, db)
	h.handleSwaps(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "/cancel")
}

// ---------------------------------------------------------------------------
// handleSwapCancel
// ---------------------------------------------------------------------------

func TestHandleSwapCancel_NoAuth_Panics(t *testing.T) {
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	h := newSwapHandler(t, db)

	req := httptest.NewRequest(http.MethodPost, "/swaps/some-id/cancel", nil)
	req = withChiParam(req, "some-id")
	w := httptest.NewRecorder()

	assert.Panics(t, func() {
		h.handleSwapCancel(w, req)
	})
}

func TestHandleSwapCancel_SwapNotFound_404(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	h := newSwapHandler(t, db)

	req := httptest.NewRequest(http.MethodPost, "/swaps/nonexistent/cancel", nil)
	req = withUser(req, "alice@example.com", "Alice", false)
	req = withChiParam(req, "nonexistent")
	w := httptest.NewRecorder()

	h.handleSwapCancel(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleSwapCancel_NotRequester_403(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	seedSchedule(t, db)

	aliceAssignments, err := db.GetFutureAssignmentsForMember(ctx, aliceID)
	require.NoError(t, err)
	require.NotEmpty(t, aliceAssignments)

	bobAssignments, err := db.GetFutureAssignmentsForMember(ctx, bobID)
	require.NoError(t, err)
	require.NotEmpty(t, bobAssignments)

	swapID, err := db.CreateHatSwap(ctx, aliceAssignments[0].ID, bobAssignments[0].ID, aliceID, bobID)
	require.NoError(t, err)

	// Bob (target) tries to cancel Alice's (requester) request.
	h := newSwapHandler(t, db)

	req := httptest.NewRequest(http.MethodPost, "/swaps/"+swapID+"/cancel", nil)
	req = withUser(req, "bob@example.com", "Bob", false)
	req = withChiParam(req, swapID)
	w := httptest.NewRecorder()

	h.handleSwapCancel(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandleSwapCancel_AlreadyAccepted_400(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	seedSchedule(t, db)

	aliceAssignments, err := db.GetFutureAssignmentsForMember(ctx, aliceID)
	require.NoError(t, err)
	require.NotEmpty(t, aliceAssignments)

	bobAssignments, err := db.GetFutureAssignmentsForMember(ctx, bobID)
	require.NoError(t, err)
	require.NotEmpty(t, bobAssignments)

	swapID, err := db.CreateHatSwap(ctx, aliceAssignments[0].ID, bobAssignments[0].ID, aliceID, bobID)
	require.NoError(t, err)

	require.NoError(t, db.UpdateHatSwapStatus(ctx, swapID, "accepted"))

	h := newSwapHandler(t, db)

	req := httptest.NewRequest(http.MethodPost, "/swaps/"+swapID+"/cancel", nil)
	req = withUser(req, "alice@example.com", "Alice", false)
	req = withChiParam(req, swapID)
	w := httptest.NewRecorder()

	h.handleSwapCancel(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleSwapCancel_Valid_Redirects(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	seedSchedule(t, db)

	aliceAssignments, err := db.GetFutureAssignmentsForMember(ctx, aliceID)
	require.NoError(t, err)
	require.NotEmpty(t, aliceAssignments)

	bobAssignments, err := db.GetFutureAssignmentsForMember(ctx, bobID)
	require.NoError(t, err)
	require.NotEmpty(t, bobAssignments)

	swapID, err := db.CreateHatSwap(ctx, aliceAssignments[0].ID, bobAssignments[0].ID, aliceID, bobID)
	require.NoError(t, err)

	h := newSwapHandler(t, db)

	req := httptest.NewRequest(http.MethodPost, "/swaps/"+swapID+"/cancel", nil)
	req = withUser(req, "alice@example.com", "Alice", false)
	req = withChiParam(req, swapID)
	w := httptest.NewRecorder()

	h.handleSwapCancel(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/swaps", w.Header().Get("Location"))

	swap, err := db.GetHatSwapByID(ctx, swapID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", swap.Status)
}

// ---------------------------------------------------------------------------
// handleSwapAccept
// ---------------------------------------------------------------------------

func TestHandleSwapAccept_NoAuth_Panics(t *testing.T) {
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	h := newSwapHandler(t, db)

	req := httptest.NewRequest(http.MethodPost, "/swaps/some-id/accept", nil)
	req = withChiParam(req, "some-id")
	w := httptest.NewRecorder()

	assert.Panics(t, func() {
		h.handleSwapAccept(w, req)
	})
}

func TestHandleSwapAccept_NotTarget_403(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	seedSchedule(t, db)

	aliceAssignments, err := db.GetFutureAssignmentsForMember(ctx, aliceID)
	require.NoError(t, err)
	require.NotEmpty(t, aliceAssignments)

	bobAssignments, err := db.GetFutureAssignmentsForMember(ctx, bobID)
	require.NoError(t, err)
	require.NotEmpty(t, bobAssignments)

	swapID, err := db.CreateHatSwap(ctx, aliceAssignments[0].ID, bobAssignments[0].ID, aliceID, bobID)
	require.NoError(t, err)

	// Alice (requester) tries to accept her own request.
	h := newSwapHandler(t, db)

	req := httptest.NewRequest(http.MethodPost, "/swaps/"+swapID+"/accept", nil)
	req = withUser(req, "alice@example.com", "Alice", false)
	req = withChiParam(req, swapID)
	w := httptest.NewRecorder()

	h.handleSwapAccept(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandleSwapAccept_Valid_ExecutesSwapAndRedirects(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	seedSchedule(t, db)

	aliceAssignments, err := db.GetFutureAssignmentsForMember(ctx, aliceID)
	require.NoError(t, err)
	require.NotEmpty(t, aliceAssignments)

	bobAssignments, err := db.GetFutureAssignmentsForMember(ctx, bobID)
	require.NoError(t, err)
	require.NotEmpty(t, bobAssignments)

	aliceAID := aliceAssignments[0].ID
	bobAID := bobAssignments[0].ID

	swapID, err := db.CreateHatSwap(ctx, aliceAID, bobAID, aliceID, bobID)
	require.NoError(t, err)

	h := newSwapHandler(t, db)

	req := httptest.NewRequest(http.MethodPost, "/swaps/"+swapID+"/accept", nil)
	req = withUser(req, "bob@example.com", "Bob", false)
	req = withChiParam(req, swapID)
	w := httptest.NewRecorder()

	h.handleSwapAccept(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/swaps", w.Header().Get("Location"))

	// The assignments should now be swapped.
	updatedAlice, err := db.GetAssignmentByID(ctx, aliceAID)
	require.NoError(t, err)
	assert.Equal(t, bobID, updatedAlice.MemberID, "Alice's slot should now belong to Bob")

	updatedBob, err := db.GetAssignmentByID(ctx, bobAID)
	require.NoError(t, err)
	assert.Equal(t, aliceID, updatedBob.MemberID, "Bob's slot should now belong to Alice")
}

func TestHandleSwapAccept_PastAssignments_ReturnsBadRequest(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	baseDate := time.Now().AddDate(0, 0, -3)
	aliceAID, err := db.CreateRotaAssignment(ctx, baseDate.Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)
	bobAID, err := db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 1).Format("2006-01-02"), bobID, false, nil)
	require.NoError(t, err)

	swapID, err := db.CreateHatSwap(ctx, aliceAID, bobAID, aliceID, bobID)
	require.NoError(t, err)

	h := newSwapHandler(t, db)
	req := httptest.NewRequest(http.MethodPost, "/swaps/"+swapID+"/accept", nil)
	req = withUser(req, "bob@example.com", "Bob", false)
	req = withChiParam(req, swapID)
	w := httptest.NewRecorder()

	h.handleSwapAccept(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "passed")

	updatedAlice, err := db.GetAssignmentByID(ctx, aliceAID)
	require.NoError(t, err)
	assert.Equal(t, aliceID, updatedAlice.MemberID)
}

// ---------------------------------------------------------------------------
// handleSwapReject
// ---------------------------------------------------------------------------

func TestHandleSwapReject_NotTarget_403(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	seedSchedule(t, db)

	aliceAssignments, err := db.GetFutureAssignmentsForMember(ctx, aliceID)
	require.NoError(t, err)
	require.NotEmpty(t, aliceAssignments)

	bobAssignments, err := db.GetFutureAssignmentsForMember(ctx, bobID)
	require.NoError(t, err)
	require.NotEmpty(t, bobAssignments)

	swapID, err := db.CreateHatSwap(ctx, aliceAssignments[0].ID, bobAssignments[0].ID, aliceID, bobID)
	require.NoError(t, err)

	// Alice (requester) tries to reject.
	h := newSwapHandler(t, db)

	req := httptest.NewRequest(http.MethodPost, "/swaps/"+swapID+"/reject", nil)
	req = withUser(req, "alice@example.com", "Alice", false)
	req = withChiParam(req, swapID)
	w := httptest.NewRecorder()

	h.handleSwapReject(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandleSwapReject_Valid_SetsStatusAndRedirects(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	seedSchedule(t, db)

	aliceAssignments, err := db.GetFutureAssignmentsForMember(ctx, aliceID)
	require.NoError(t, err)
	require.NotEmpty(t, aliceAssignments)

	bobAssignments, err := db.GetFutureAssignmentsForMember(ctx, bobID)
	require.NoError(t, err)
	require.NotEmpty(t, bobAssignments)

	swapID, err := db.CreateHatSwap(ctx, aliceAssignments[0].ID, bobAssignments[0].ID, aliceID, bobID)
	require.NoError(t, err)

	h := newSwapHandler(t, db)

	req := httptest.NewRequest(http.MethodPost, "/swaps/"+swapID+"/reject", nil)
	req = withUser(req, "bob@example.com", "Bob", false)
	req = withChiParam(req, swapID)
	w := httptest.NewRecorder()

	h.handleSwapReject(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/swaps", w.Header().Get("Location"))

	swap, err := db.GetHatSwapByID(ctx, swapID)
	require.NoError(t, err)
	assert.Equal(t, "rejected", swap.Status)
}

// ---------------------------------------------------------------------------
// handleSwapAdminDelete
// ---------------------------------------------------------------------------

func TestHandleSwapAdminDelete_NoAuth_Panics(t *testing.T) {
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	h := newSwapHandler(t, db)

	req := httptest.NewRequest(http.MethodPost, "/swaps/some-id/delete", nil)
	req = withChiParam(req, "some-id")
	w := httptest.NewRecorder()

	assert.Panics(t, func() {
		h.handleSwapAdminDelete(w, req)
	})
}

func TestHandleSwapAdminDelete_NonAdmin_403(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	h := newSwapHandler(t, db)

	req := httptest.NewRequest(http.MethodPost, "/swaps/some-id/delete", nil)
	req = withUser(req, "alice@example.com", "Alice", false)
	req = withChiParam(req, "some-id")
	w := httptest.NewRecorder()

	h.handleSwapAdminDelete(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandleSwapAdminDelete_Valid_Redirects(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	seedSchedule(t, db)

	aliceAssignments, err := db.GetFutureAssignmentsForMember(ctx, aliceID)
	require.NoError(t, err)
	require.NotEmpty(t, aliceAssignments)

	bobAssignments, err := db.GetFutureAssignmentsForMember(ctx, bobID)
	require.NoError(t, err)
	require.NotEmpty(t, bobAssignments)

	swapID, err := db.CreateHatSwap(ctx, aliceAssignments[0].ID, bobAssignments[0].ID, aliceID, bobID)
	require.NoError(t, err)

	h := newSwapHandler(t, db)

	req := httptest.NewRequest(http.MethodPost, "/swaps/"+swapID+"/delete", nil)
	req = withUser(req, "admin@example.com", "Admin", true)
	req = withChiParam(req, swapID)
	w := httptest.NewRecorder()

	h.handleSwapAdminDelete(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/swaps", w.Header().Get("Location"))

	// Verify it was actually deleted — GetHatSwapByID returns an error when not found.
	swap, err := db.GetHatSwapByID(ctx, swapID)
	require.Error(t, err, "deleted swap should not be found")
	assert.Nil(t, swap)
}
