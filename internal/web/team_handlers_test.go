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

	_, err := db.GetQueries().CreateUser(context.Background(), sqlc.CreateUserParams{
		ID:         userID,
		Email:      userID + "@example.com",
		Name:       "User " + userID,
		Provider:   "fake",
		ProviderID: userID,
		IsAdmin:    sql.NullInt64{Int64: adminValue, Valid: true},
		IsActive:   sql.NullInt64{Int64: 1, Valid: true},
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/team/members/x/edit", nil)
	req = withChiParam(req, "x")
	w := httptest.NewRecorder()

	h.handleTeamMemberEdit(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/team/members/x/delete", nil)
	req = withChiParam(req, "x")
	w := httptest.NewRecorder()

	h.handleTeamMemberDelete(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
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
