package api

import (
	"errors"
	"fmt"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statusOf extracts the HTTP status from a huma error. Huma's helpers
// return *ErrorModel directly, but errorlint rejects the bare type
// assertion in case a future huma version wraps the model — errors.As
// is the safe form.
func statusOf(t *testing.T, err error) int {
	t.Helper()
	var se huma.StatusError
	require.ErrorAs(t, err, &se, "wfhDomainToHumaError must return a huma.StatusError")
	return se.GetStatus()
}

// modelOf extracts the *ErrorModel so the test can inspect the Errors
// array where huma stashes wrapped causes. Same rationale as statusOf.
func modelOf(t *testing.T, err error) *huma.ErrorModel {
	t.Helper()
	var m *huma.ErrorModel
	require.ErrorAs(t, err, &m, "wfhDomainToHumaError must return a *huma.ErrorModel")
	return m
}

// TestWFHDomainToHumaError_KnownSentinels pins every WFH sentinel error to
// its expected huma HTTP status and message. The function is the single
// adapter between the database package's transport-level table and the
// huma error format used by the REST API; a regression silently swaps 4xx
// for 5xx (or vice versa) for quota / horizon / approval errors.
//
// The expected values are duplicated from database.WFHErrorFor's tests so
// a refactor that changes the table without updating both layers fails
// both suites.
func TestWFHDomainToHumaError_KnownSentinels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
	}{
		{"NotFound", database.ErrWFHNotFound, 404, "WFH request not found."},
		{"NotOwner", database.ErrWFHNotOwner, 403, "You can only modify your own WFH requests."},
		{"AlreadySettled", database.ErrWFHAlreadySettled, 409, "This WFH request has already been settled and cannot be cancelled."},
		{"DuplicateRequest", database.ErrWFHDuplicateRequest, 409, "A WFH request already exists for this date."},
		{"InvalidDate", database.ErrWFHInvalidDate, 422, "invalid date format, expected YYYY-MM-DD"},
		{"DatePassed", database.ErrWFHDatePassed, 422, "This WFH day has already passed."},
		{"DateTooFar", database.ErrWFHDateTooFar, 422, "WFH requests can only be made up to a limited number of days in advance."},
		{"MemberNotFound", database.ErrWFHMemberNotFound, 422, "Member not found."},
		{"RecurringContractDay", database.ErrWFHRecurringContractDay, 409, "This date falls on your contractual recurring WFH day."},
		{"OnHoliday", database.ErrWFHOnHoliday, 422, "WFH requests cannot be made for holidays."},
		{"NotApproved", database.ErrWFHNotApproved, 409, "Only approved WFH requests can be withdrawn."},
		{"QuotaExhausted", database.ErrWFHQuotaExhausted, 422, "You have reached your WFH quota for this period. Withdraw an approved WFH to free a slot, or contact an admin."},
		{"Disabled", database.ErrWFHDisabled, 503, "The WFH feature is disabled on this server."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out := wfhDomainToHumaError(tc.err)

			assert.Equal(t, tc.wantStatus, statusOf(t, out), "status for %s", tc.name)
			assert.Contains(t, out.Error(), tc.wantMsg, "message for %s", tc.name)
		})
	}
}

// TestWFHDomainToHumaError_WrappedSentinel ensures fmt.Errorf("%w", ...)
// chains propagate to huma correctly via errors.Is — without this, every
// call site that adds context to a returned WFH sentinel would silently
// fall back to a 500.
func TestWFHDomainToHumaError_WrappedSentinel(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("updateWFHRequest: %w", database.ErrWFHNotOwner)

	out := wfhDomainToHumaError(wrapped)
	assert.Equal(t, 403, statusOf(t, out), "wrapped sentinel must unwrap to its mapped status")
	assert.Contains(t, out.Error(), "You can only modify your own WFH requests.")
}

// TestWFHDomainToHumaError_UnknownError ensures non-WFH errors return a
// generic 500. The api must not leak database/sql/transport-level detail
// into the top-level message; the wrapped err is preserved in huma's
// Errors array so structured logs still surface the cause.
func TestWFHDomainToHumaError_UnknownError(t *testing.T) {
	t.Parallel()

	raw := errors.New("connection refused")
	out := wfhDomainToHumaError(raw)

	assert.Equal(t, 500, statusOf(t, out), "unknown error must map to a 500")
	assert.Contains(t, out.Error(), "An unexpected error occurred")

	// Huma puts the wrapped errs in the Errors array on *ErrorModel.
	// Inspecting via errors.As so a future huma refactor that returns
	// a plain error fails this test instead of silently dropping the
	// cause.
	model := modelOf(t, out)
	require.NotEmpty(t, model.Errors, "the wrapped err must be retained for structured logs")
	assert.Contains(t, model.Errors[0].Message, "connection refused")
}

// TestWFHDomainToHumaError_Nil confirms nil input doesn't panic and
// routes through the unknown-error branch (500) since WFHErrorFor(nil)
// returns ok=false.
func TestWFHDomainToHumaError_Nil(t *testing.T) {
	t.Parallel()

	out := wfhDomainToHumaError(nil)
	assert.Equal(t, 500, statusOf(t, out), "nil must take the 500 branch")
}
