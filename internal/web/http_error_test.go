package web

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestHTTPError_LogsButHidesInternalError pins the contract
// that httpError enforces. The test wires a slog handler
// that writes to a buffer, calls httpError with a recognizable
// internal error, and asserts:
//
//  1. The response body is the generic publicMsg — no trace
//     of the internal error.
//  2. The internal error IS captured in the slog output so
//     operators can diagnose.
//
// This is the behavioral contract every refactored handler
// site in the codebase depends on. The test sits here next
// to the helper so a future change to the helper can't
// accidentally drop either half of the contract.
func TestHTTPError_LogsButHidesInternalError(t *testing.T) {
	// Capture slog output for the duration of the test.
	var logBuf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
	const publicMsg = "Internal server error."
	const internalMsg = "sqlite3: UNIQUE constraint failed: team_members.email (operator-fingerprintable string)"

	httpError(rec, req, http.StatusInternalServerError, publicMsg, errors.New(internalMsg))

	// Body must be the generic message.
	body := rec.Body.String()
	assert.Equal(t, publicMsg+"\n", body,
		"the response body must be exactly the generic publicMsg (with http.Error's trailing newline)")
	assert.NotContains(t, body, "sqlite3",
		"the body must not contain any prefix of the internal error message")
	assert.NotContains(t, body, "team_members",
		"the body must not contain the table name from the internal error")
	assert.NotContains(t, body, "operator-fingerprintable",
		"the body must not contain any tail of the internal error message")
	assert.Equal(t, http.StatusInternalServerError, rec.Code,
		"the status code must be honored as-is")

	// Internal error must be in the log.
	logged := logBuf.String()
	assert.Contains(t, logged, internalMsg,
		"the internal error must be captured in the slog output")
}

// TestHTTPError_NoInternalErrorStillResponds pins the nil-error
// case: a handler that wants a 4xx body without a backing
// error can call httpError(w, r, 400, "bad input", nil) and
// get a 400 with the body. The helper must not panic or log
// garbage when internalErr is nil.
func TestHTTPError_NoInternalErrorStillResponds(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
	httpError(rec, req, http.StatusBadRequest, "bad input", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "bad input\n", rec.Body.String())
}

// TestHTTPError_5xxLogsAtErrorLevel pins the log level for
// server-side failures (5xx) vs caller-fixable errors (4xx).
// The contract is: 5xx = slog.Error, 4xx = slog.Warn. The
// helper makes the split so an operator scanning the logs
// can filter for "is this our fault or theirs?".
func TestHTTPError_5xxLogsAtErrorLevel(t *testing.T) {
	var logBuf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
	httpError(rec, req, http.StatusInternalServerError, "boom", errors.New("internal err"))
	assert.Contains(t, logBuf.String(), "level=ERROR")

	logBuf.Reset()
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
	httpError(rec2, req2, http.StatusBadRequest, "bad", errors.New("caller err"))
	assert.Contains(t, logBuf.String(), "level=WARN")
	assert.NotContains(t, logBuf.String(), "level=ERROR")
}
