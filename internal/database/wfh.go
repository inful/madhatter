package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
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

// CreateWFHRequest creates a new pending WFH request for the given member on the given date.
//
//nolint:cyclop // Validation and persistence branches are explicit for domain error clarity.
func (db *DB) CreateWFHRequest(ctx context.Context, memberID, date string) (*WFHRequest, error) {
	if memberID == "" || date == "" {
		return nil, errors.New("memberID and date are required")
	}

	// Validate member exists. The struct itself isn't read here — the
	// existence check is what we need; the column-level recurring flag
	// lives on the materialized row, not on the request flow.
	_, err := db.queries.GetMemberByID(ctx, memberID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWFHMemberNotFound
		}
		return nil, err
	}
	// Validate the date is today or in the future.
	dateTime, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, ErrWFHInvalidDate
	}

	// Members can no longer request a date that is on a contractual recurring
	// weekday; the materializer already inserted an auto-approved row for
	// that date. The UNIQUE(member_id, date) constraint surfaces this as
	// ErrWFHDuplicateRequest, which the form translates to a "withdraw your
	// recurring day first" hint.

	// Reject holidays: a WFH on a non-working day is meaningless — there's no
	// on-site capacity to consume and no presence to track. Fail fast at the
	// data layer so the invariant is enforced regardless of the caller.
	if db.IsHoliday(dateTime) {
		return nil, ErrWFHOnHoliday
	}

	nowUTC := time.Now().UTC()
	today := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	if dateTime.Before(today) {
		return nil, ErrWFHDatePassed
	}

	id := uuid.New().String()
	_, err = db.queries.CreateWFHRequest(ctx, sqlc.CreateWFHRequestParams{
		ID:       id,
		MemberID: memberID,
		Date:     dateTime,
	})
	if err != nil {
		// SQLite UNIQUE constraint violation.
		if isUniqueConstraintError(err) {
			return nil, ErrWFHDuplicateRequest
		}
		return nil, err
	}

	row, err := db.queries.GetWFHRequestByID(ctx, id)
	if err != nil {
		return nil, err
	}
	result := wfhFromSQLCFields(wfhFields{
		ID:          row.ID,
		MemberID:    row.MemberID,
		Date:        row.Date,
		Status:      row.Status,
		CreatedAt:   row.CreatedAt,
		SettledAt:   row.SettledAt,
		WithdrawnBy: row.WithdrawnBy,
		WithdrawnAt: row.WithdrawnAt,
		IsRecurring: row.IsRecurring,
	})
	return &result, nil
}

// GetWFHRequestByID retrieves a WFH request by its ID.
func (db *DB) GetWFHRequestByID(ctx context.Context, id string) (*WFHRequest, error) {
	row, err := db.queries.GetWFHRequestByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWFHNotFound
		}
		return nil, err
	}
	result := wfhFromSQLCFields(wfhFields{
		ID:          row.ID,
		MemberID:    row.MemberID,
		Date:        row.Date,
		Status:      row.Status,
		CreatedAt:   row.CreatedAt,
		SettledAt:   row.SettledAt,
		WithdrawnBy: row.WithdrawnBy,
		WithdrawnAt: row.WithdrawnAt,
		IsRecurring: row.IsRecurring,
	})
	return &result, nil
}

// GetWFHRequestsByDate returns all WFH requests for a specific date.
func (db *DB) GetWFHRequestsByDate(ctx context.Context, date string) ([]WFHRequest, error) {
	dateTime, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, ErrWFHInvalidDate
	}
	rows, err := db.queries.GetWFHRequestsByDate(ctx, dateTime)
	if err != nil {
		return nil, err
	}
	result := make([]WFHRequest, len(rows))
	for i := range rows {
		result[i] = wfhFromSQLCFields(wfhFields{
			ID:          rows[i].ID,
			MemberID:    rows[i].MemberID,
			Date:        rows[i].Date,
			Status:      rows[i].Status,
			CreatedAt:   rows[i].CreatedAt,
			SettledAt:   rows[i].SettledAt,
			WithdrawnBy: rows[i].WithdrawnBy,
			WithdrawnAt: rows[i].WithdrawnAt,
			IsRecurring: rows[i].IsRecurring,
		})
	}
	return result, nil
}

// GetWFHRequestsByDateAndStatus returns WFH requests for a date filtered by status.
func (db *DB) GetWFHRequestsByDateAndStatus(ctx context.Context, date, status string) ([]WFHRequest, error) {
	dateTime, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, ErrWFHInvalidDate
	}
	rows, err := db.queries.GetWFHRequestsByDateAndStatus(ctx, sqlc.GetWFHRequestsByDateAndStatusParams{
		Date:   dateTime,
		Status: status,
	})
	if err != nil {
		return nil, err
	}
	result := make([]WFHRequest, len(rows))
	for i := range rows {
		result[i] = wfhFromSQLCFields(wfhFields{
			ID:          rows[i].ID,
			MemberID:    rows[i].MemberID,
			Date:        rows[i].Date,
			Status:      rows[i].Status,
			CreatedAt:   rows[i].CreatedAt,
			SettledAt:   rows[i].SettledAt,
			WithdrawnBy: rows[i].WithdrawnBy,
			WithdrawnAt: rows[i].WithdrawnAt,
			IsRecurring: rows[i].IsRecurring,
		})
	}
	return result, nil
}

// GetWFHRequestsByMember returns all WFH requests for a team member.
func (db *DB) GetWFHRequestsByMember(ctx context.Context, memberID string) ([]WFHRequest, error) {
	rows, err := db.queries.GetWFHRequestsByMember(ctx, memberID)
	if err != nil {
		return nil, err
	}
	result := make([]WFHRequest, len(rows))
	for i := range rows {
		result[i] = wfhFromSQLCFields(wfhFields{
			ID:          rows[i].ID,
			MemberID:    rows[i].MemberID,
			Date:        rows[i].Date,
			Status:      rows[i].Status,
			CreatedAt:   rows[i].CreatedAt,
			SettledAt:   rows[i].SettledAt,
			WithdrawnBy: rows[i].WithdrawnBy,
			WithdrawnAt: rows[i].WithdrawnAt,
			IsRecurring: rows[i].IsRecurring,
		})
	}
	return result, nil
}

// GetWFHRequestsUsedInPeriod returns the WFH requests (pending + approved) for a member within a period.
func (db *DB) GetWFHRequestsUsedInPeriod(ctx context.Context, memberID, periodStart, periodEnd string) ([]WFHRequest, error) {
	start, err := time.Parse("2006-01-02", periodStart)
	if err != nil {
		return nil, ErrWFHInvalidDate
	}
	end, err := time.Parse("2006-01-02", periodEnd)
	if err != nil {
		return nil, ErrWFHInvalidDate
	}
	rows, err := db.queries.GetWFHRequestsByMemberAndPeriod(ctx, sqlc.GetWFHRequestsByMemberAndPeriodParams{
		MemberID: memberID,
		Date:     start,
		Date_2:   end,
	})
	if err != nil {
		return nil, err
	}
	result := make([]WFHRequest, len(rows))
	for i := range rows {
		result[i] = wfhFromSQLCFields(wfhFields{
			ID:          rows[i].ID,
			MemberID:    rows[i].MemberID,
			Date:        rows[i].Date,
			Status:      rows[i].Status,
			CreatedAt:   rows[i].CreatedAt,
			SettledAt:   rows[i].SettledAt,
			WithdrawnBy: rows[i].WithdrawnBy,
			WithdrawnAt: rows[i].WithdrawnAt,
			IsRecurring: rows[i].IsRecurring,
		})
	}
	return result, nil
}

// HasWFHRequestOnDate reports whether any wfh_requests row exists for the
// (memberID, date) pair in any status. Used by the recurring materializer
// to preserve explicit user state (withdrawn, cancelled, etc.) when filling
// gaps in the upcoming schedule.
func (db *DB) HasWFHRequestOnDate(ctx context.Context, memberID, date string) (bool, error) {
	dateTime, err := time.Parse("2006-01-02", date)
	if err != nil {
		return false, ErrWFHInvalidDate
	}
	row, err := db.queries.GetWFHRequestByMemberAndDate(ctx, sqlc.GetWFHRequestByMemberAndDateParams{
		MemberID: memberID,
		Date:     dateTime,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return row.ID != "", nil
}

// GetPendingForSettlement returns pending WFH requests with dates on or before the cutoff.
func (db *DB) GetPendingForSettlement(ctx context.Context, cutoffDate string) ([]WFHRequest, error) {
	cutoff, err := time.Parse("2006-01-02", cutoffDate)
	if err != nil {
		return nil, ErrWFHInvalidDate
	}
	rows, err := db.queries.GetPendingWFHRequestsForSettlement(ctx, cutoff)
	if err != nil {
		return nil, err
	}
	result := make([]WFHRequest, len(rows))
	for i := range rows {
		result[i] = wfhFromSQLCFields(wfhFields{
			ID:          rows[i].ID,
			MemberID:    rows[i].MemberID,
			Date:        rows[i].Date,
			Status:      rows[i].Status,
			CreatedAt:   rows[i].CreatedAt,
			SettledAt:   rows[i].SettledAt,
			WithdrawnBy: rows[i].WithdrawnBy,
			WithdrawnAt: rows[i].WithdrawnAt,
			IsRecurring: rows[i].IsRecurring,
		})
	}
	return result, nil
}

// GetAllWFHRequests returns all WFH requests ordered by date descending.
func (db *DB) GetAllWFHRequests(ctx context.Context) ([]WFHRequest, error) {
	rows, err := db.queries.GetAllWFHRequests(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]WFHRequest, len(rows))
	for i := range rows {
		result[i] = wfhFromSQLCFields(wfhFields{
			ID:          rows[i].ID,
			MemberID:    rows[i].MemberID,
			Date:        rows[i].Date,
			Status:      rows[i].Status,
			CreatedAt:   rows[i].CreatedAt,
			SettledAt:   rows[i].SettledAt,
			WithdrawnBy: rows[i].WithdrawnBy,
			WithdrawnAt: rows[i].WithdrawnAt,
			IsRecurring: rows[i].IsRecurring,
		})
	}
	return result, nil
}

// UpdateWFHRequestStatus updates the status and settled_at timestamp of a WFH request.
func (db *DB) UpdateWFHRequestStatus(ctx context.Context, id, status string) error {
	now := sql.NullTime{Time: time.Now().UTC(), Valid: true}
	_, err := db.queries.UpdateWFHRequestStatus(ctx, sqlc.UpdateWFHRequestStatusParams{
		Status:    status,
		SettledAt: now,
		ID:        id,
	})
	return err
}

// CancelWFHRequest cancels a pending WFH request. Only the owning member may cancel.
func (db *DB) CancelWFHRequest(ctx context.Context, id, memberID string) error {
	req, err := db.GetWFHRequestByID(ctx, id)
	if err != nil {
		return err
	}
	if req.MemberID != memberID {
		return ErrWFHNotOwner
	}
	if req.Status != WFHStatusPending {
		return ErrWFHAlreadySettled
	}
	return db.UpdateWFHRequestStatus(ctx, id, WFHStatusCancelled)
}

// WithdrawWFHRequest withdraws an approved WFH request (admin
// action) as long as the WFH date has not yet passed. The same
// rule applies to self-withdraw — the date-not-passed check is
// the only gate.
func (db *DB) WithdrawWFHRequest(ctx context.Context, id, adminUserID string) error {
	return db.withdrawWFH(ctx, id, "", adminUserID)
}

// WithdrawOwnWFHRequest withdraws an approved WFH request on behalf of the owning
// member. Enforces ownership (MemberID must match). The WFH date
// must not have passed.
func (db *DB) WithdrawOwnWFHRequest(ctx context.Context, id, memberID string) error {
	return db.withdrawWFH(ctx, id, memberID, "")
}

// withdrawWFH is the shared implementation for admin and self-withdrawal. The
// memberID, when non-empty, is enforced as the owning member. The actorUserID
// is recorded as withdrawn_by.
func (db *DB) withdrawWFH(ctx context.Context, id, memberID, actorUserID string) error {
	req, err := db.GetWFHRequestByID(ctx, id)
	if err != nil {
		return err
	}
	if req.Status != WFHStatusApproved {
		return ErrWFHNotApproved
	}
	if memberID != "" && req.MemberID != memberID {
		return ErrWFHNotOwner
	}

	// Withdrawable as long as the WFH date has not yet passed.
	// "today" is still withdrawable; "yesterday" is not.
	wfhDate, err := time.Parse("2006-01-02", req.Date)
	if err != nil {
		return errors.New("invalid stored WFH date")
	}
	today := time.Now().UTC().Truncate(hoursPerDay * time.Hour)
	if wfhDate.UTC().Before(today) {
		return ErrWFHDatePassed
	}

	_, err = db.queries.UpdateWFHRequestWithdrawn(ctx, sqlc.UpdateWFHRequestWithdrawnParams{
		WithdrawnBy: sql.NullString{String: actorUserID, Valid: actorUserID != ""},
		ID:          id,
	})
	return err
}

// CreateApprovedRecurringWFHRequest inserts an auto-approved, is_recurring=1
// row on behalf of the materializer. Bypasses the holiday and member-exists
// guards in CreateWFHRequest — the materializer iterates the member's own
// recurring weekdays and the date has already been pre-validated.
//
// Returns ErrWFHDuplicateRequest if a row already exists for (memberID, date)
// — the caller treats that as idempotent success.
func (db *DB) CreateApprovedRecurringWFHRequest(ctx context.Context, memberID, date string, settledAt time.Time) error {
	dateTime, err := time.Parse("2006-01-02", date)
	if err != nil {
		return ErrWFHInvalidDate
	}

	nowUTC := time.Now().UTC()
	today := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	if dateTime.Before(today) {
		return ErrWFHDatePassed
	}

	_, err = db.queries.CreateApprovedRecurringWFHRequest(ctx, sqlc.CreateApprovedRecurringWFHRequestParams{
		ID:        uuid.New().String(),
		MemberID:  memberID,
		Date:      dateTime,
		SettledAt: sql.NullTime{Time: settledAt, Valid: true},
	})
	if err != nil {
		if isUniqueConstraintError(err) {
			return ErrWFHDuplicateRequest
		}
		return err
	}
	return nil
}

// CountApprovedWFHByDate returns the number of approved WFH requests for a given date.
func (db *DB) CountApprovedWFHByDate(ctx context.Context, date string) (int, error) {
	dateTime, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0, ErrWFHInvalidDate
	}
	count, err := db.queries.CountApprovedWFHByDate(ctx, dateTime)
	return int(count), err
}

// isUniqueConstraintError detects SQLite UNIQUE constraint violations.
func isUniqueConstraintError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
