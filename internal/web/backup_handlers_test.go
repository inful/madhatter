package web

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/database/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleDatabaseBackup_NonAdmin_Forbidden(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/admin/database/backup", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &sqlc.GetSessionByTokenRow{
		Email:   "user@example.com",
		Name:    "User",
		IsAdmin: sql.NullInt64{Int64: 0, Valid: true},
	}))

	rec := httptest.NewRecorder()
	h.handleDatabaseBackup(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "Admin access required")
}

func TestHandleDatabaseBackup_Admin_DownloadsSQLiteFile(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = db.AddTeamMember(context.Background(), "Alice", "alice@example.com")
	require.NoError(t, err)

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/admin/database/backup", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &sqlc.GetSessionByTokenRow{
		Email:   "admin@example.com",
		Name:    "Admin",
		IsAdmin: sql.NullInt64{Int64: 1, Valid: true},
	}))

	rec := httptest.NewRecorder()
	h.handleDatabaseBackup(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/vnd.sqlite3", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment; filename=")
	assert.True(t, strings.HasPrefix(rec.Body.String(), "SQLite format 3\x00"))
}
