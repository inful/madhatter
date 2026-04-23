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

	adminID := createTestUser(t, db, true)
	targetID := createTestUser(t, db, false)
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

	adminID := createTestUser(t, db, true)
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

	firstAdminID := createTestUser(t, db, true)
	secondAdminID := createTestUser(t, db, true)
	req := buildAdminUpdateRequest(secondAdminID, "0")
	req = withAdminContext(req, firstAdminID)

	rec := httptest.NewRecorder()
	handler.handleUserAdminUpdate(rec, req)

	assert.Equal(t, http.StatusSeeOther, rec.Code)

	adminCount, err := db.GetQueries().CountAdmins(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), adminCount)
}

func createTestUser(t *testing.T, db *database.DB, isAdmin bool) string {
	t.Helper()

	userID := uuid.NewString()
	adminValue := int64(0)
	if isAdmin {
		adminValue = 1
	}

	_, err := db.GetQueries().CreateUser(context.Background(), sqlc.CreateUserParams{
		ID:         userID,
		Email:      userID + "@example.com",
		Name:       "User " + userID[:8],
		Provider:   "fake",
		ProviderID: userID,
		IsAdmin:    sql.NullInt64{Int64: adminValue, Valid: true},
		IsActive:   sql.NullInt64{Int64: 1, Valid: true},
	})
	require.NoError(t, err)

	return userID
}

func buildAdminUpdateRequest(userID, isAdminValue string) *http.Request {
	form := url.Values{}
	form.Set("is_admin", isAdminValue)

	req := httptest.NewRequest(http.MethodPost, "/team/users/"+userID+"/admin", strings.NewReader(form.Encode()))
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
