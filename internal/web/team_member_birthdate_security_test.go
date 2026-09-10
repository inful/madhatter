package web

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/inful/madhatter/internal/database"
)

// rawPost is the standard form-encoded POST helper used by
// the security-review safety-net tests in this file. Mirrors
// the existing `withUser` helper shape so the new tests
// read consistently with the rest of internal/web's suite.
// Uses context.Background() (not t.Context()) because the
// helper is called from multiple tests; the noctx linter
// wants a context on every request, and the existing
// handlers_leave_test.go pattern uses context.Background()
// for the same reason.
func rawPost(path, body string) *http.Request {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// lookupByEmail wraps db.GetMemberByEmail so the safety-net
// tests can assert "no row" without the helper returning
// sql.ErrNoRows. The caller treats "not found" the same as
// "the row was not created".
func lookupByEmail(t *testing.T, db *database.DB, email string) *database.TeamMember {
	t.Helper()
	m, err := db.GetMemberByEmail(t.Context(), email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	require.NoError(t, err)
	return m
}

// TestHandleTeamPost_Birthdate_RawHTTPRejectsMissingSession is
// the security-review #60 safety net: a POST to /team with a
// birthdate field but no session cookie must NOT mutate the
// DB. The handler refuses at the new defense-in-depth auth
// check before parsing the body. (Defends the new birthdate
// column from unauthenticated tampering. Mirrors the existing
// leave/WFH safety-net pattern.)
func TestHandleTeamPost_Birthdate_RawHTTPRejectsMissingSession(t *testing.T) {
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	body := "name=Alice&[email protected]&birthdate=1990-05-15"
	req := rawPost("/team", body)
	rr := httptest.NewRecorder()
	h.handleTeam(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code,
		"a /team POST with no session must be rejected with 401; body=%s", rr.Body.String())
	row := lookupByEmail(t, db, "alice0@example.com")
	assert.Nil(t, row,
		"the row must NOT have been created when the session is missing")
}

// TestHandleTeamMemberEdit_Birthdate_RawHTTPRejectsEscalation
// pins the cross-user write protection on the new field. A
// non-admin POSTing to /team/{id}/edit for someone else's row
// must not change the birthdate, even with a valid form body.
// (Mirrors the existing edit/delete safety-net tests.)
func TestHandleTeamMemberEdit_Birthdate_RawHTTPRejectsEscalation(t *testing.T) {
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	original := time.Date(1990, 5, 15, 0, 0, 0, 0, time.UTC)
	id, err := db.AddTeamMember(t.Context(), "Alice", "[email protected]", &original)
	require.NoError(t, err)

	form := url.Values{}
	form.Set("name", "Alice Hacked")
	form.Set("email", "alice1@example.com")
	form.Set("birthdate", "2099-09-09") // attacker wants to escalate to far future

	req := rawPost("/team/"+id+"/edit", form.Encode())
	req = withUser(req, "[email protected]", "Mallory", false)
	rr := httptest.NewRecorder()
	h.handleTeamMemberEdit(rr, req)

	// Non-admin edits another member's row → blocked.
	assert.True(t, rr.Code == http.StatusForbidden || rr.Code == http.StatusNotFound,
		"a non-admin editing another member's row must be rejected; got %d body=%s",
		rr.Code, rr.Body.String())

	got, err := db.GetMemberByID(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, "Alice", got.Name,
		"name must NOT have been changed")
	assert.Equal(t, "[email protected]", got.Email,
		"email must NOT have been changed")
	require.NotNil(t, got.Birthdate,
		"the original birthdate must still be in place")
	assert.Equal(t, "1990-05-15", got.Birthdate.Format("2006-01-02"),
		"the original birthdate must NOT have been overwritten by the attacker")
}

// TestHandleTeamPost_Birthdate_ValidDateAccepted pins the
// happy path: a valid YYYY-MM-DD birthdate is persisted.
func TestHandleTeamPost_Birthdate_ValidDateAccepted(t *testing.T) {
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	form := url.Values{}
	form.Set("name", "Alice")
	form.Set("email", "alice2@example.com")
	form.Set("birthdate", "1990-05-15")

	req := rawPost("/team", form.Encode())
	req = withUser(req, "[email protected]", "Admin", true)
	rr := httptest.NewRecorder()
	h.handleTeam(rr, req)

	require.True(t, rr.Code == http.StatusOK || rr.Code == http.StatusSeeOther,
		"admin POST with valid birthdate must succeed; got %d, body=%s",
		rr.Code, rr.Body.String())
	row := lookupByEmail(t, db, "alice2@example.com")
	require.NotNil(t, row)
	require.NotNil(t, row.Birthdate,
		"the persisted member must have a birthdate")
	assert.Equal(t, "1990-05-15", row.Birthdate.Format("2006-01-02"))
}

// TestHandleTeamPost_Birthdate_MissingFieldAccepted pins the
// privacy default: omitting the field is a valid signal that
// the member has not shared their birthday. The row is
// created with birthdate = NULL.
func TestHandleTeamPost_Birthdate_MissingFieldAccepted(t *testing.T) {
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	form := url.Values{}
	form.Set("name", "Alice")
	form.Set("email", "alice3@example.com")
	// birthdate intentionally omitted

	req := rawPost("/team", form.Encode())
	req = withUser(req, "[email protected]", "Admin", true)
	rr := httptest.NewRecorder()
	h.handleTeam(rr, req)

	require.True(t, rr.Code == http.StatusOK || rr.Code == http.StatusSeeOther,
		"admin POST without birthdate must succeed; got %d, body=%s",
		rr.Code, rr.Body.String())
	row := lookupByEmail(t, db, "alice3@example.com")
	require.NotNil(t, row)
	assert.Nil(t, row.Birthdate,
		"a missing birthdate field must round-trip as SQL NULL")
}

// TestHandleTeamPost_Birthdate_FutureDateRejected pins the
// "no future birthdays" guard.
func TestHandleTeamPost_Birthdate_FutureDateRejected(t *testing.T) {
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	form := url.Values{}
	form.Set("name", "Alice")
	form.Set("email", "alice4@example.com")
	form.Set("birthdate", "2099-01-01")

	req := rawPost("/team", form.Encode())
	req = withUser(req, "[email protected]", "Admin", true)
	rr := httptest.NewRecorder()
	h.handleTeam(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code,
		"a future birthdate must be rejected with 400; body=%s", rr.Body.String())
	row := lookupByEmail(t, db, "alice4@example.com")
	assert.Nil(t, row,
		"a rejected birthdate must not create a row")
}

// TestHandleTeamPost_Birthdate_Pre1900Rejected pins the sanity
// floor: pre-1900 dates are almost certainly typos.
func TestHandleTeamPost_Birthdate_Pre1900Rejected(t *testing.T) {
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	form := url.Values{}
	form.Set("name", "Alice")
	form.Set("email", "alice5@example.com")
	form.Set("birthdate", "1850-01-01")

	req := rawPost("/team", form.Encode())
	req = withUser(req, "[email protected]", "Admin", true)
	rr := httptest.NewRecorder()
	h.handleTeam(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code,
		"a pre-1900 birthdate must be rejected with 400; body=%s", rr.Body.String())
	row := lookupByEmail(t, db, "alice5@example.com")
	assert.Nil(t, row,
		"a pre-1900 birthdate must not create a row")
}

// TestHandleTeamPost_Birthdate_MalformedDateRejected pins the
// shape guard — anything that doesn't parse as YYYY-MM-DD is
// rejected.
func TestHandleTeamPost_Birthdate_MalformedDateRejected(t *testing.T) {
	cases := []string{
		"05/15/1990",  // US format
		"1990-5-15",   // missing zero-padding
		"May 15 1990", // free-form
		"1990-13-40",  // impossible MM-DD
		"<script>",    // XSS attempt
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			db, h, cleanup := setupLeaveTestDB(t)
			defer cleanup()

			form := url.Values{}
			form.Set("name", "Alice")
			form.Set("email", "alice6@example.com")
			form.Set("birthdate", raw)

			req := rawPost("/team", form.Encode())
			req = withUser(req, "[email protected]", "Admin", true)
			rr := httptest.NewRecorder()
			h.handleTeam(rr, req)

			require.Equal(t, http.StatusBadRequest, rr.Code,
				"malformed birthdate %q must be rejected with 400", raw)
			row := lookupByEmail(t, db, "alice6@example.com")
			assert.Nil(t, row)
		})
	}
}

// TestHandleTeamMemberEdit_Birthdate_UpdatesRow pins the
// edit path: a valid POST to /team/{id}/edit must overwrite
// the existing birthdate.
func TestHandleTeamMemberEdit_Birthdate_UpdatesRow(t *testing.T) {
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	original := time.Date(1990, 5, 15, 0, 0, 0, 0, time.UTC)
	id, err := db.AddTeamMember(t.Context(), "Alice", "[email protected]", &original)
	require.NoError(t, err)

	form := url.Values{}
	form.Set("name", "Alice")
	form.Set("email", "alice7@example.com")
	form.Set("birthdate", "1992-09-03")

	req := rawPost("/team/"+id+"/edit", form.Encode())
	req = withUser(req, "[email protected]", "Admin", true)
	req = withChiParam(req, id)
	rr := httptest.NewRecorder()
	h.handleTeamMemberEdit(rr, req)

	require.True(t, rr.Code == http.StatusOK || rr.Code == http.StatusSeeOther,
		"admin edit with valid birthdate must succeed; got %d, body=%s",
		rr.Code, rr.Body.String())
	got, err := db.GetMemberByID(t.Context(), id)
	require.NoError(t, err)
	require.NotNil(t, got.Birthdate)
	assert.Equal(t, "1992-09-03", got.Birthdate.Format("2006-01-02"))
}

// TestHandleTeamMemberEdit_Birthdate_EmptyClearsRow pins the
// "admin removed the birthday" path. Empty string → SQL NULL.
func TestHandleTeamMemberEdit_Birthdate_EmptyClearsRow(t *testing.T) {
	db, h, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	original := time.Date(1990, 5, 15, 0, 0, 0, 0, time.UTC)
	id, err := db.AddTeamMember(t.Context(), "Alice", "[email protected]", &original)
	require.NoError(t, err)

	form := url.Values{}
	form.Set("name", "Alice")
	form.Set("email", "alice8@example.com")
	form.Set("birthdate", "") // admin cleared the field

	req := rawPost("/team/"+id+"/edit", form.Encode())
	req = withUser(req, "[email protected]", "Admin", true)
	req = withChiParam(req, id)
	rr := httptest.NewRecorder()
	h.handleTeamMemberEdit(rr, req)

	require.True(t, rr.Code == http.StatusOK || rr.Code == http.StatusSeeOther,
		"admin edit with empty birthdate must succeed; got %d, body=%s",
		rr.Code, rr.Body.String())
	got, err := db.GetMemberByID(t.Context(), id)
	require.NoError(t, err)
	assert.Nil(t, got.Birthdate,
		"empty birthdate field must clear the column to SQL NULL")
}
