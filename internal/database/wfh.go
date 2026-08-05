package database

import (
	"database/sql"
	"errors"
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
)

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
