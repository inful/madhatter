package database

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

// WFH sentinel errors.
var (
	ErrWFHNotFound             = errors.New("WFH request not found")
	ErrWFHNotOwner             = errors.New("you can only modify your own WFH requests")
	ErrWFHAlreadySettled       = errors.New("WFH request has already been settled")
	ErrWFHDuplicateRequest     = errors.New("a WFH request already exists for this date")
	ErrWFHInvalidDate          = errors.New("invalid date format, expected YYYY-MM-DD")
	ErrWFHDatePassed           = errors.New("WFH date has already passed")
	ErrWFHDateTooFar           = errors.New("WFH date is beyond the request horizon")
	ErrWFHMemberNotFound       = errors.New("member not found")
	ErrWFHRecurringContractDay = errors.New("this weekday is already configured as recurring WFH for the member")
	ErrWFHPermanentMember      = ErrWFHRecurringContractDay
	ErrWFHNotApproved          = errors.New("WFH request is not approved")
	ErrWFHOnHoliday            = errors.New("WFH requests cannot be made for holidays")
	ErrWFHQuotaExhausted       = errors.New("WFH quota for this period has been reached")
	ErrWFHDisabled             = errors.New("the WFH feature is disabled")
)

// WFHErrorInfo describes the transport-level meaning of a WFH sentinel
// error. The database package is the single source of truth for which HTTP
// status each WFH error maps to and what user-facing message applies —
// both the API (huma) and web handlers consult this table to produce their
// own error format.
type WFHErrorInfo struct {
	Status  int    // HTTP status code
	Message string // User-facing message safe to surface
}

// wfhErrorTable maps each WFH sentinel error to its transport-level info.
// Adding a new ErrWFH* sentinel requires adding a row here so both the API
// and web layers route it correctly.
var wfhErrorTable = []struct {
	sentinel error
	info     WFHErrorInfo
}{
	{ErrWFHNotFound, WFHErrorInfo{Status: http.StatusNotFound, Message: "WFH request not found."}},
	{ErrWFHNotOwner, WFHErrorInfo{Status: http.StatusForbidden, Message: "You can only modify your own WFH requests."}},
	{ErrWFHAlreadySettled, WFHErrorInfo{Status: http.StatusConflict, Message: "This WFH request has already been settled and cannot be cancelled."}},
	{ErrWFHDuplicateRequest, WFHErrorInfo{Status: http.StatusConflict, Message: "A WFH request already exists for this date."}},
	{ErrWFHInvalidDate, WFHErrorInfo{Status: http.StatusUnprocessableEntity, Message: "invalid date format, expected YYYY-MM-DD"}},
	{ErrWFHDatePassed, WFHErrorInfo{Status: http.StatusUnprocessableEntity, Message: "This WFH day has already passed."}},
	{ErrWFHDateTooFar, WFHErrorInfo{Status: http.StatusUnprocessableEntity, Message: "WFH requests can only be made up to a limited number of days in advance."}},
	{ErrWFHMemberNotFound, WFHErrorInfo{Status: http.StatusUnprocessableEntity, Message: "Member not found."}},
	{ErrWFHRecurringContractDay, WFHErrorInfo{Status: http.StatusConflict, Message: "This date falls on your contractual recurring WFH day."}},
	{ErrWFHOnHoliday, WFHErrorInfo{Status: http.StatusUnprocessableEntity, Message: "WFH requests cannot be made for holidays."}},
	{ErrWFHNotApproved, WFHErrorInfo{Status: http.StatusConflict, Message: "Only approved WFH requests can be withdrawn."}},
	{ErrWFHQuotaExhausted, WFHErrorInfo{Status: http.StatusUnprocessableEntity, Message: "You have reached your WFH quota for this period. Withdraw an approved WFH to free a slot, or contact an admin."}},
	{ErrWFHDisabled, WFHErrorInfo{Status: http.StatusServiceUnavailable, Message: "The WFH feature is disabled on this server."}},
}

// WFHErrorFor returns the transport-level info for err if it is (or wraps)
// a known WFH sentinel error. Returns ok=false for any other error so
// callers can decide on a generic fallback (typically a 500).
func WFHErrorFor(err error) (WFHErrorInfo, bool) {
	for _, e := range wfhErrorTable {
		if errors.Is(err, e.sentinel) {
			return e.info, true
		}
	}
	return WFHErrorInfo{}, false
}

// wfhFields holds the WFH request columns selected by every read query.
// sqlc v1.31 emits a per-query *Row type, so adapters in this file copy the
// fields into a wfhFields value before delegating to wfhFromSQLCFields.
type wfhFields struct {
	ID          string
	MemberID    string
	Date        time.Time
	Status      string
	CreatedAt   sql.NullTime
	SettledAt   sql.NullTime
	WithdrawnBy sql.NullString
	WithdrawnAt sql.NullTime
	IsRecurring int64
}

// wfhFromSQLCFields converts the canonical column set to the domain WFHRequest.
func wfhFromSQLCFields(f wfhFields) WFHRequest {
	req := WFHRequest{
		ID:          f.ID,
		MemberID:    f.MemberID,
		Date:        f.Date.Format("2006-01-02"),
		Status:      f.Status,
		IsRecurring: f.IsRecurring == 1,
	}
	if f.CreatedAt.Valid {
		req.CreatedAt = f.CreatedAt.Time
	}
	if f.SettledAt.Valid {
		t := f.SettledAt.Time
		req.SettledAt = &t
	}
	if f.WithdrawnBy.Valid {
		s := f.WithdrawnBy.String
		req.WithdrawnBy = &s
	}
	if f.WithdrawnAt.Valid {
		t := f.WithdrawnAt.Time
		req.WithdrawnAt = &t
	}
	return req
}

// isUniqueConstraintError detects SQLite UNIQUE constraint violations.
func isUniqueConstraintError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
