package web

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/database/sqlc"
	"github.com/inful/madhatter/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleUserAdminUpdatePromotesUser(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	handler, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	adminID := createActiveTestUser(t, db, true)
	targetID := createActiveTestUser(t, db, false)
	req := buildAdminUpdateRequest(targetID, "1")
	req = withAdminContext(req, adminID)

	rec := httptest.NewRecorder()
	handler.handleUserAdminUpdate(rec, req)

	assert.Equal(t, http.StatusSeeOther, rec.Code)

	target, err := db.GetQueries().GetUserByID(context.Background(), targetID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), target.IsAdmin.Int64)
}

func TestHandleUserAdminUpdatePreventsRemovingLastAdmin(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	handler, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	adminID := createActiveTestUser(t, db, true)
	req := buildAdminUpdateRequest(adminID, "0")
	req = withAdminContext(req, adminID)

	rec := httptest.NewRecorder()
	handler.handleUserAdminUpdate(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "cannot remove the last admin")

	admin, err := db.GetQueries().GetUserByID(context.Background(), adminID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), admin.IsAdmin.Int64)
}

func TestHandleUserAdminUpdateDemotesWhenOtherAdminExists(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	handler, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	firstAdminID := createActiveTestUser(t, db, true)
	secondAdminID := createActiveTestUser(t, db, true)
	req := buildAdminUpdateRequest(secondAdminID, "0")
	req = withAdminContext(req, firstAdminID)

	rec := httptest.NewRecorder()
	handler.handleUserAdminUpdate(rec, req)

	assert.Equal(t, http.StatusSeeOther, rec.Code)

	adminCount, err := db.GetQueries().CountAdmins(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), adminCount)
}

func createActiveTestUser(t *testing.T, db *database.DB, isAdmin bool) string {
	t.Helper()

	userID := uuid.NewString()
	adminValue := int64(0)
	if isAdmin {
		adminValue = 1
	}

	_, err := db.GetQueries().CreateActiveUser(context.Background(), sqlc.CreateActiveUserParams{
		ID:         userID,
		Email:      userID + "@example.com",
		Name:       "User " + userID,
		Provider:   "fake",
		ProviderID: userID,
		IsAdmin:    sql.NullInt64{Int64: adminValue, Valid: true},
	})
	require.NoError(t, err)

	return userID
}

func buildAdminUpdateRequest(userID, isAdminFormValue string) *http.Request {
	form := url.Values{}
	form.Set("is_admin", isAdminFormValue)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/team/users/"+userID+"/admin", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", userID)

	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}

func withAdminContext(req *http.Request, userID string) *http.Request {
	session := &sqlc.GetSessionByTokenRow{
		UserID:  userID,
		IsAdmin: sql.NullInt64{Int64: 1, Valid: true},
	}

	return req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, session))
}

// ---------------------------------------------------------------------------
// validateTeamMemberInput
// ---------------------------------------------------------------------------

func TestValidateTeamMemberInput_RejectsEmptyName(t *testing.T) {
	err := validateTeamMemberInput("", "alice@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name cannot be empty")
}

func TestValidateTeamMemberInput_RejectsEmptyEmail(t *testing.T) {
	err := validateTeamMemberInput("Alice", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "email cannot be empty")
}

func TestValidateTeamMemberInput_RejectsLongName(t *testing.T) {
	err := validateTeamMemberInput(strings.Repeat("x", 256), "alice@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is too long")
}

func TestValidateTeamMemberInput_RejectsLongEmail(t *testing.T) {
	err := validateTeamMemberInput("Alice", strings.Repeat("x", 256)+"@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "email is too long")
}

func TestValidateTeamMemberInput_RejectsInvalidEmailFormat(t *testing.T) {
	err := validateTeamMemberInput("Alice", "not-an-email")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid email format")
}

func TestValidateTeamMemberInput_AcceptsValidInput(t *testing.T) {
	err := validateTeamMemberInput("Alice", "alice@example.com")
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// handleTeamPost
// ---------------------------------------------------------------------------

func TestHandleTeamPost_ValidMember_RedirectsToTeam(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	form := url.Values{}
	form.Set("name", "Alice")
	form.Set("email", "alice@example.com")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/team", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.handleTeamPost(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/team", w.Header().Get("Location"))

	members, err := db.GetActiveTeamMembers(context.Background())
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, "Alice", members[0].Name)
}

func TestHandleTeamPost_DuplicateEmail_Returns500(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = db.AddTeamMember(context.Background(), "Existing", "dup@example.com")
	require.NoError(t, err)

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	form := url.Values{}
	form.Set("name", "New Person")
	form.Set("email", "dup@example.com")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/team", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.handleTeamPost(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ---------------------------------------------------------------------------
// handleTeam (GET + POST routing)
// ---------------------------------------------------------------------------

func TestHandleTeam_Get_Returns200(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/team", nil)
	w := httptest.NewRecorder()

	h.handleTeam(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleTeam_Get_DevelopmentModeSyncsTeamMembersToApplicationUsers(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	_, err = db.AddTeamMember(ctx, "Dev Member", "dev-member@example.com")
	require.NoError(t, err)

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, true, nil)
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/team", nil)
	w := httptest.NewRecorder()

	h.handleTeam(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	users, err := db.GetQueries().ListActiveUsers(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "dev-member@example.com", users[0].Email)
	assert.Equal(t, "Dev Member", users[0].Name)
	assert.Equal(t, "fake", users[0].Provider)
	assert.False(t, auth.IsAdmin(users[0].IsAdmin))
}

func TestHandleTeam_Post_DelegatesToHandleTeamPost(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	form := url.Values{}
	form.Set("name", "Bob")
	form.Set("email", "bob@example.com")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/team", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.handleTeam(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/team", w.Header().Get("Location"))
}

// ---------------------------------------------------------------------------
// handleTeamMemberEdit
// ---------------------------------------------------------------------------

func TestHandleTeamMemberEdit_WrongMethod_Returns405(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	rec := testutil.HandlerCall(t, h.handleTeamMemberEdit, http.MethodGet, "/team/members/x/edit",
		testutil.URLParam{Name: "id", Value: "x"})

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestHandleTeamMemberEdit_InvalidInput_Returns400(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	form := url.Values{}
	form.Set("name", "")
	form.Set("email", "alice@example.com")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/team/members/x/edit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withChiParam(req, "x")
	w := httptest.NewRecorder()

	h.handleTeamMemberEdit(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleTeamMemberEdit_ValidPost_RedirectsToTeam(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Old Name", "old@example.com")
	require.NoError(t, err)

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	form := url.Values{}
	form.Set("name", "New Name")
	form.Set("email", "new@example.com")

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/team/members/"+memberID+"/edit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withChiParam(req, memberID)
	w := httptest.NewRecorder()

	h.handleTeamMemberEdit(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/team", w.Header().Get("Location"))
}

// ---------------------------------------------------------------------------
// handleTeamMemberDelete
// ---------------------------------------------------------------------------

func TestHandleTeamMemberDelete_WrongMethod_Returns405(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	rec := testutil.HandlerCall(t, h.handleTeamMemberDelete, http.MethodGet, "/team/members/x/delete",
		testutil.URLParam{Name: "id", Value: "x"})

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestHandleTeamMemberDelete_ValidPost_RedirectsToTeam(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "To Delete", "todelete@example.com")
	require.NoError(t, err)

	// A second member must remain so HandleTeamChange can still build the schedule.
	_, err = db.AddTeamMember(ctx, "Remaining", "remaining@example.com")
	require.NoError(t, err)

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/team/members/"+memberID+"/delete", nil)
	req = withChiParam(req, memberID)
	w := httptest.NewRecorder()

	h.handleTeamMemberDelete(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/team", w.Header().Get("Location"))
}

// ---------------------------------------------------------------------------
// handleTeamMemberPermanentWFHUpdate
// ---------------------------------------------------------------------------

func TestHandleTeamMemberPermanentWFHUpdate_WrongMethod_Returns405(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	rec := testutil.HandlerCall(t, h.handleTeamMemberPermanentWFHUpdate, http.MethodGet, "/team/members/x/permanent-wfh",
		testutil.URLParam{Name: "id", Value: "x"})

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestHandleTeamMemberPermanentWFHUpdate_NotFound_Returns404(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	form := url.Values{}
	form.Set("recurring_wfh_monday", "1")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/team/members/x/permanent-wfh", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withChiParam(req, uuid.NewString())
	w := httptest.NewRecorder()

	h.handleTeamMemberPermanentWFHUpdate(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleTeamMemberPermanentWFHUpdate_ValidPost_UpdatesRecurringDays(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	setForm := url.Values{}
	setForm.Set("recurring_wfh_monday", "1")
	setForm.Set("recurring_wfh_thursday", "1")
	setReq := httptest.NewRequestWithContext(ctx, http.MethodPost, "/team/members/"+memberID+"/permanent-wfh", strings.NewReader(setForm.Encode()))
	setReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setReq = withChiParam(setReq, memberID)
	setResp := httptest.NewRecorder()

	h.handleTeamMemberPermanentWFHUpdate(setResp, setReq)

	assert.Equal(t, http.StatusSeeOther, setResp.Code)
	assert.Equal(t, "/team", setResp.Header().Get("Location"))

	member, err := db.GetMemberByID(ctx, memberID)
	require.NoError(t, err)
	assert.True(t, member.RecurringWFHMonday)
	assert.False(t, member.RecurringWFHTuesday)
	assert.False(t, member.RecurringWFHWednesday)
	assert.True(t, member.RecurringWFHThursday)
	assert.False(t, member.RecurringWFHFriday)
	assert.False(t, member.IsPermanentWFH)

	unsetForm := url.Values{}
	unsetReq := httptest.NewRequestWithContext(ctx, http.MethodPost, "/team/members/"+memberID+"/permanent-wfh", strings.NewReader(unsetForm.Encode()))
	unsetReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	unsetReq = withChiParam(unsetReq, memberID)
	unsetResp := httptest.NewRecorder()

	h.handleTeamMemberPermanentWFHUpdate(unsetResp, unsetReq)

	assert.Equal(t, http.StatusSeeOther, unsetResp.Code)
	assert.Equal(t, "/team", unsetResp.Header().Get("Location"))

	member, err = db.GetMemberByID(ctx, memberID)
	require.NoError(t, err)
	assert.False(t, member.RecurringWFHMonday)
	assert.False(t, member.RecurringWFHTuesday)
	assert.False(t, member.RecurringWFHWednesday)
	assert.False(t, member.RecurringWFHThursday)
	assert.False(t, member.RecurringWFHFriday)
	assert.False(t, member.IsPermanentWFH)
}

// ---------------------------------------------------------------------------
// handleTeamMemberExemptUpdate — Step 17 of Phase 4
// ---------------------------------------------------------------------------

// TestHandleTeamMemberExemptUpdate_WrongMethod_Returns405 pins the
// route's method gate: a stray GET must 405, not 200, so a stale
// tab linking to the route doesn't accidentally toggle state.
func TestHandleTeamMemberExemptUpdate_WrongMethod_Returns405(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	rec := testutil.HandlerCall(t, h.handleTeamMemberExemptUpdate, http.MethodGet, "/team/members/x/exempt",
		testutil.URLParam{Name: "id", Value: "x"})

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// TestHandleTeamMemberExemptUpdate_NotFound_Returns404 covers the
// member-existence guard. Without it, an admin could flip the
// flag on a phantom row and the DB UPDATE would silently no-op.
func TestHandleTeamMemberExemptUpdate_NotFound_Returns404(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	form := url.Values{}
	form.Set("is_exempt_from_assignment", "1")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/team/members/missing/exempt", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withChiParam(req, uuid.NewString())
	w := httptest.NewRecorder()

	h.handleTeamMemberExemptUpdate(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestHandleTeamMemberExemptUpdate_ValidPost_TogglesFlag pins the
// happy path: a checked form flips the flag on, an unchecked
// form flips it back off. The exempt member must remain eligible
// for voluntary WFH (covered by the picker math at the service
// layer; the integration tests there are the load-bearing proof;
// this test only pins the form's storage write).
func TestHandleTeamMemberExemptUpdate_ValidPost_TogglesFlag(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	setForm := url.Values{}
	setForm.Set("is_exempt_from_assignment", "1")
	setReq := httptest.NewRequestWithContext(ctx, http.MethodPost, "/team/members/"+memberID+"/exempt", strings.NewReader(setForm.Encode()))
	setReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setReq = withChiParam(setReq, memberID)
	setResp := httptest.NewRecorder()

	h.handleTeamMemberExemptUpdate(setResp, setReq)

	assert.Equal(t, http.StatusSeeOther, setResp.Code)
	assert.Equal(t, "/team", setResp.Header().Get("Location"))

	member, err := db.GetMemberByID(ctx, memberID)
	require.NoError(t, err)
	assert.True(t, member.IsExemptFromAssignment, "checked form must flip the flag on")

	// Unchecked form must flip it back off. parseCheckboxBool
	// reads "1", "on", or case-insensitive "true" as true; an
	// absent key is the off path that matters here.
	unsetForm := url.Values{}
	unsetReq := httptest.NewRequestWithContext(ctx, http.MethodPost, "/team/members/"+memberID+"/exempt", strings.NewReader(unsetForm.Encode()))
	unsetReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	unsetReq = withChiParam(unsetReq, memberID)
	unsetResp := httptest.NewRecorder()

	h.handleTeamMemberExemptUpdate(unsetResp, unsetReq)

	assert.Equal(t, http.StatusSeeOther, unsetResp.Code)
	member, err = db.GetMemberByID(ctx, memberID)
	require.NoError(t, err)
	assert.False(t, member.IsExemptFromAssignment, "unchecked form must flip the flag off")
}

// ---------------------------------------------------------------------------
// handleUserAdminUpdate — additional edge cases
// ---------------------------------------------------------------------------

func TestHandleUserAdminUpdate_WrongMethod_Returns405(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/team/users/x/admin", nil)
	req = withChiParam(req, "x")
	w := httptest.NewRecorder()

	h.handleUserAdminUpdate(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestHandleUserAdminUpdate_UserNotFound_Returns404(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	form := url.Values{}
	form.Set("is_admin", "1")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/team/users/nonexistent/admin", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withChiParam(req, uuid.NewString())
	w := httptest.NewRecorder()

	h.handleUserAdminUpdate(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
