package web

import (
	"errors"
	"fmt"
	"testing"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
)

// TestWFHWebErrorMessage_KnownSentinels pins every WFH sentinel error to
// its user-facing message. The web layer is the dual of the api layer's
// wfhDomainToHumaError — the message comes from the same database table
// so templates can render a friendly string instead of a Go error literal.
// A regression here would surface raw Go errors to end users.
func TestWFHWebErrorMessage_KnownSentinels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{"NotFound", database.ErrWFHNotFound, "WFH request not found."},
		{"NotOwner", database.ErrWFHNotOwner, "You can only modify your own WFH requests."},
		{"AlreadySettled", database.ErrWFHAlreadySettled, "This WFH request has already been settled and cannot be cancelled."},
		{"DuplicateRequest", database.ErrWFHDuplicateRequest, "A WFH request already exists for this date."},
		{"InvalidDate", database.ErrWFHInvalidDate, "invalid date format, expected YYYY-MM-DD"},
		{"DatePassed", database.ErrWFHDatePassed, "This WFH day has already passed."},
		{"DateTooFar", database.ErrWFHDateTooFar, "WFH requests can only be made up to a limited number of days in advance."},
		{"MemberNotFound", database.ErrWFHMemberNotFound, "Member not found."},
		{"RecurringContractDay", database.ErrWFHRecurringContractDay, "This date falls on your contractual recurring WFH day."},
		{"OnHoliday", database.ErrWFHOnHoliday, "WFH requests cannot be made for holidays."},
		{"NotApproved", database.ErrWFHNotApproved, "Only approved WFH requests can be withdrawn."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.wantMsg, wfhWebErrorMessage(tc.err))
		})
	}
}

// TestWFHWebErrorMessage_WrappedSentinel ensures fmt.Errorf("%w", ...)
// chains unwrap via errors.Is so handlers that add context to a returned
// WFH sentinel still surface the friendly message.
func TestWFHWebErrorMessage_WrappedSentinel(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("handleWFHSubmit: %w", database.ErrWFHDateTooFar)
	assert.Equal(t, "WFH requests can only be made up to a limited number of days in advance.", wfhWebErrorMessage(wrapped))
}

// TestWFHWebErrorMessage_UnknownError ensures non-WFH errors fall through
// to err.Error() rather than being swallowed. This is intentionally a
// leaky fallback for the web layer because web handlers are the catch-all
// for unexpected errors and operators reading the message need to know
// what actually went wrong.
func TestWFHWebErrorMessage_UnknownError(t *testing.T) {
	t.Parallel()

	raw := errors.New("connection refused")
	assert.Equal(t, "connection refused", wfhWebErrorMessage(raw))
}

// TestWFHWebErrorMessage_Nil confirms nil input produces an empty
// string. WFHErrorFor(nil) returns ok=false, so the function falls into
// the err.Error() branch which is "". Templates rendering an empty
// message render nothing — which is the correct behavior for a
// successfully-resolved error path.
func TestWFHWebErrorMessage_Nil(t *testing.T) {
	t.Parallel()

	assert.Empty(t, wfhWebErrorMessage(nil))
}
