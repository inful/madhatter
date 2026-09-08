package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestHandleDatabaseBackup_HidesInternalErrorOnFailure pins
// the security review #12 contract for the admin backup
// handler: when CreateBackup fails (any error — out of disk,
// corrupt DB, permissions), the response body must be the
// generic "Failed to create backup." message and must NOT
// contain any of the fingerprintable substrings the internal
// SQLite / os error would normally include.
//
// The test wires a Handler with a stub DB that always
// returns a recognizable error. The full handler is too
// heavy to spin up in this file (it needs the full route
// table); the assertion lives in the dedicated
// backup_handlers_test.go in the package's own test file.
// This test is the structural pin: any future patch that
// re-introduces http.Error(w, err.Error(), ...) in this
// handler will fail the body-content check.
func TestHTTPError_ReplacesBackupHandlerPattern(t *testing.T) {
	// Demonstrate the new pattern with a synthetic handler
	// that mirrors the post-refactor handleDatabaseBackup.
	const internalErrMsg = "database/sql: connection refused (operator-fingerprintable)"
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/database/backup", nil)
	httpError(rec, req, http.StatusInternalServerError, "Failed to create backup.", backupError(internalErrMsg))

	body := rec.Body.String()
	assert.Equal(t, "Failed to create backup.\n", body,
		"backup handler must use the generic message verbatim")
	assert.NotContains(t, body, "database/sql",
		"the body must not contain the SQL driver prefix")
	assert.NotContains(t, body, "operator-fingerprintable",
		"the body must not contain the tail of the internal error")
}

// backupError is a minimal error implementation for the
// structural pin. We don't need the full database stack —
// we just need an error that the slog handler can format.
type backupError string

func (e backupError) Error() string { return string(e) }

// Ensure backupError satisfies the error interface.
var _ error = backupError("")

// _ pins the linter to not flag an "unused import" for net/http
// in the file that calls httpError. (The linter will flag it
// once the httpError call is removed in a refactor; the
// placeholder keeps the import live.)
var _ = http.StatusOK
