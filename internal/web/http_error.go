package web

import (
	"errors"
	"log/slog"
	"net/http"
)

// httpError is the standard error responder for every handler
// in the web layer. It does two things:
//
//  1. Logs the full internal error to slog so the operator
//     can diagnose failures. Errors are logged at ERROR
//     level for 5xx responses (server-side failures the
//     operator should investigate) and at WARN level for
//     4xx responses (caller-fixable, not a server bug).
//  2. Writes a generic, action-oriented publicMsg to the
//     response body. The body intentionally does NOT contain
//     the internal error text, the table name, the SQL
//     fragment, the file path, or any other fingerprint
//     surface. Security review finding #12: the pre-fix code
//     used `http.Error(w, err.Error(), …)` across ~100 sites
//     which forwarded SQLite error strings (table names,
//     constraint names, query fragments) and os.PathError
//     paths to the client.
//
// The function returns nothing; it's the last line of a
// handler. Callers don't need to log the error themselves
// afterwards — the helper has already done it.
//
// Behavior is pinned by the TestHTTPError_* tests in
// http_error_test.go.
func httpError(w http.ResponseWriter, r *http.Request, status int, publicMsg string, internalErr error) {
	if internalErr != nil && !errors.Is(internalErr, http.ErrHandlerTimeout) {
		if isServerError(status) {
			slog.ErrorContext(r.Context(), publicMsg,
				"status", status, "error", internalErr)
		} else {
			slog.WarnContext(r.Context(), publicMsg,
				"status", status, "error", internalErr)
		}
	}
	http.Error(w, publicMsg, status)
}

// isServerError reports whether the status code is a 5xx
// server-side failure (operator should investigate) rather
// than a 4xx caller-fixable error. The 500 threshold is the
// standard "server error" boundary; the helper uses it to
// pick the slog level.
func isServerError(status int) bool {
	return status >= http.StatusInternalServerError
}
