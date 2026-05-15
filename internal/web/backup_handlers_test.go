package web

import (
	"bytes"
	"context"
	"database/sql"
	"mime/multipart"
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

func TestHandleDatabaseRestore_NonAdmin_Forbidden(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/admin/database/restore", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &sqlc.GetSessionByTokenRow{
		Email:   "user@example.com",
		Name:    "User",
		IsAdmin: sql.NullInt64{Int64: 0, Valid: true},
	}))

	rec := httptest.NewRecorder()
	h.handleDatabaseRestore(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "Admin access required")
}

func TestHandleDatabaseRestore_GetAdmin_RendersForm(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/admin/database/restore", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &sqlc.GetSessionByTokenRow{
		Email:   "admin@example.com",
		Name:    "Admin",
		IsAdmin: sql.NullInt64{Int64: 1, Valid: true},
	}))

	rec := httptest.NewRecorder()
	h.handleDatabaseRestore(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Validate Restore File")
}

func TestHandleDatabaseRestore_PostAdmin_ValidBackup(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = db.AddTeamMember(context.Background(), "Alice", "alice@example.com")
	require.NoError(t, err)

	backupBytes, err := db.CreateBackup(context.Background())
	require.NoError(t, err)

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("backup_file", "backup.db")
	require.NoError(t, err)
	_, err = part.Write(backupBytes)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/admin/database/restore", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &sqlc.GetSessionByTokenRow{
		Email:   "admin@example.com",
		Name:    "Admin",
		IsAdmin: sql.NullInt64{Int64: 1, Valid: true},
	}))

	rec := httptest.NewRecorder()
	h.handleDatabaseRestore(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Validation succeeded")
}

func TestHandleDatabaseRestore_PostAdmin_InvalidBackup(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("backup_file", "bad.db")
	require.NoError(t, err)
	_, err = part.Write([]byte("invalid-file"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/admin/database/restore", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &sqlc.GetSessionByTokenRow{
		Email:   "admin@example.com",
		Name:    "Admin",
		IsAdmin: sql.NullInt64{Int64: 1, Valid: true},
	}))

	rec := httptest.NewRecorder()
	h.handleDatabaseRestore(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Validation failed")
}

func TestHandleDatabaseRestore_PostAdmin_ApplyWithoutConfirmation(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = db.AddTeamMember(context.Background(), "Alice", "alice@example.com")
	require.NoError(t, err)

	backupBytes, err := db.CreateBackup(context.Background())
	require.NoError(t, err)

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("backup_file", "backup.db")
	require.NoError(t, err)
	_, err = part.Write(backupBytes)
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("mode", "apply"))
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/admin/database/restore", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &sqlc.GetSessionByTokenRow{
		Email:   "admin@example.com",
		Name:    "Admin",
		IsAdmin: sql.NullInt64{Int64: 1, Valid: true},
	}))

	rec := httptest.NewRecorder()
	h.handleDatabaseRestore(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "must confirm overwrite")
}

func TestHandleDatabaseRestore_PostAdmin_ApplyWithConfirmation(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = db.AddTeamMember(context.Background(), "Alice", "alice@example.com")
	require.NoError(t, err)

	backupBytes, err := db.CreateBackup(context.Background())
	require.NoError(t, err)

	_, err = db.AddTeamMember(context.Background(), "Bob", "bob@example.com")
	require.NoError(t, err)

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("backup_file", "backup.db")
	require.NoError(t, err)
	_, err = part.Write(backupBytes)
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("mode", "apply"))
	require.NoError(t, writer.WriteField("confirm_overwrite", "on"))
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/admin/database/restore", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &sqlc.GetSessionByTokenRow{
		Email:   "admin@example.com",
		Name:    "Admin",
		IsAdmin: sql.NullInt64{Int64: 1, Valid: true},
	}))

	rec := httptest.NewRecorder()
	h.handleDatabaseRestore(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Restore applied successfully")

	members, err := db.GetActiveTeamMembers(context.Background())
	require.NoError(t, err)
	require.Len(t, members, 1)
	require.Equal(t, "Alice", members[0].Name)
}
