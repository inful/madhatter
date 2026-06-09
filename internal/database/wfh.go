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
	ErrWFHNotFound                 = errors.New("WFH request not found")
	ErrWFHNotOwner                 = errors.New("you can only modify your own WFH requests")
	ErrWFHAlreadySettled           = errors.New("WFH request has already been settled")
	ErrWFHWithdrawalDeadlinePassed = errors.New("withdrawal deadline has passed")
	ErrWFHDuplicateRequest         = errors.New("a WFH request already exists for this date")
	ErrWFHInvalidDate              = errors.New("invalid date format, expected YYYY-MM-DD")
	ErrWFHDatePassed               = errors.New("WFH date has already passed")
	ErrWFHMemberNotFound           = errors.New("member not found")
	ErrWFHRecurringContractDay     = errors.New("this weekday is already configured as recurring WFH for the member")
	ErrWFHPermanentMember          = ErrWFHRecurringContractDay
	ErrWFHNotApproved              = errors.New("WFH request is not approved")
	ErrWFHOnHoliday                = errors.New("WFH requests cannot be made for holidays")
)

// wfhFromSQLCRow converts a sqlc WfhRequest row to the domain WFHRequest model.
func wfhFromSQLCRow(r sqlc.WfhRequest) WFHRequest {
	req := WFHRequest{
		ID:       r.ID,
		MemberID: r.MemberID,
		Date:     r.Date.Format("2006-01-02"),
		Status:   r.Status,
	}
	if r.CreatedAt.Valid {
		req.CreatedAt = r.CreatedAt.Time
	}
	if r.SettledAt.Valid {
		t := r.SettledAt.Time
		req.SettledAt = &t
	}
	if r.WithdrawnBy.Valid {
		s := r.WithdrawnBy.String
		req.WithdrawnBy = &s
	}
	if r.WithdrawnAt.Valid {
		t := r.WithdrawnAt.Time
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

	// Validate member exists.
	member, err := db.queries.GetMemberByID(ctx, memberID)
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

	isRecurringWFH := (dateTime.Weekday() == time.Monday && member.RecurringWfhMonday == 1) ||
		(dateTime.Weekday() == time.Tuesday && member.RecurringWfhTuesday == 1) ||
		(dateTime.Weekday() == time.Wednesday && member.RecurringWfhWednesday == 1) ||
		(dateTime.Weekday() == time.Thursday && member.RecurringWfhThursday == 1) ||
		(dateTime.Weekday() == time.Friday && member.RecurringWfhFriday == 1)
	if isRecurringWFH {
		return nil, ErrWFHRecurringContractDay
	}

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
	result := wfhFromSQLCRow(row)
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
	result := wfhFromSQLCRow(row)
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
	return wfhSliceFromSQLCRows(rows), nil
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
	return wfhSliceFromSQLCRows(rows), nil
}

// GetWFHRequestsByMember returns all WFH requests for a team member.
func (db *DB) GetWFHRequestsByMember(ctx context.Context, memberID string) ([]WFHRequest, error) {
	rows, err := db.queries.GetWFHRequestsByMember(ctx, memberID)
	if err != nil {
		return nil, err
	}
	return wfhSliceFromSQLCRows(rows), nil
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
	return wfhSliceFromSQLCRows(rows), nil
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
	return wfhSliceFromSQLCRows(rows), nil
}

// GetAllWFHRequests returns all WFH requests ordered by date descending.
func (db *DB) GetAllWFHRequests(ctx context.Context) ([]WFHRequest, error) {
	rows, err := db.queries.GetAllWFHRequests(ctx)
	if err != nil {
		return nil, err
	}
	return wfhSliceFromSQLCRows(rows), nil
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

// WithdrawWFHRequest withdraws an approved WFH request (admin action) if the withdrawal
// deadline has not yet passed.
func (db *DB) WithdrawWFHRequest(ctx context.Context, id, adminUserID string, withdrawalHours int) error {
	return db.withdrawWFH(ctx, id, "", adminUserID, withdrawalHours)
}

// WithdrawOwnWFHRequest withdraws an approved WFH request on behalf of the owning
// member. Enforces ownership (MemberID must match) and the withdrawal deadline.
func (db *DB) WithdrawOwnWFHRequest(ctx context.Context, id, memberID string, withdrawalHours int) error {
	return db.withdrawWFH(ctx, id, memberID, "", withdrawalHours)
}

// withdrawWFH is the shared implementation for admin and self-withdrawal. The
// memberID, when non-empty, is enforced as the owning member. The actorUserID
// is recorded as withdrawn_by.
func (db *DB) withdrawWFH(ctx context.Context, id, memberID, actorUserID string, withdrawalHours int) error {
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

	// Check withdrawal deadline: must be called at least withdrawalHours before midnight of the WFH day.
	wfhDate, err := time.Parse("2006-01-02", req.Date)
	if err != nil {
		return errors.New("invalid stored WFH date")
	}
	deadline := wfhDate.UTC().Add(-time.Duration(withdrawalHours) * time.Hour)
	if time.Now().UTC().After(deadline) {
		return ErrWFHWithdrawalDeadlinePassed
	}

	_, err = db.queries.UpdateWFHRequestWithdrawn(ctx, sqlc.UpdateWFHRequestWithdrawnParams{
		WithdrawnBy: sql.NullString{String: actorUserID, Valid: actorUserID != ""},
		ID:          id,
	})
	return err
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

// wfhSliceFromSQLCRows converts a slice of sqlc rows to domain WFHRequest values.
func wfhSliceFromSQLCRows(rows []sqlc.WfhRequest) []WFHRequest {
	result := make([]WFHRequest, len(rows))
	for i := range rows {
		result[i] = wfhFromSQLCRow(rows[i])
	}
	return result
}

// isUniqueConstraintError detects SQLite UNIQUE constraint violations.
func isUniqueConstraintError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
