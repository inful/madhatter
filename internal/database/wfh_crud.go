package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
)

// CreateWFHRequest creates a new pending WFH request for the given member on the given date.
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

	if vErr := db.validateRequestDate(dateTime); vErr != nil {
		return nil, vErr
	}

	id := uuid.New().String()
	_, err = db.queries.CreateWFHRequest(ctx, sqlc.CreateWFHRequestParams{
		ID:       id,
		MemberID: memberID,
		Date:     dateTime,
	})
	if err != nil {
		// SQLite UNIQUE constraint violation. If the colliding row is one
		// the user owns (a previous cancel, or a self-withdrawal), resurrect
		// it in place — the user is allowed to change their mind. Any other
		// status (pending, approved, denied, admin-withdrawn) keeps the
		// existing "already exists" semantics.
		if isUniqueConstraintError(err) {
			return db.resurrectOrDuplicate(ctx, memberID, dateTime)
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

// validateRequestDate enforces the data-layer invariants that hold for
// any WFH request, fresh or resurrected: the date must not be a holiday
// (no on-site capacity to consume on non-working days) and must not be
// in the past (the day has already been lived). Shared between the
// INSERT path in CreateWFHRequest and the resurrect path so a row that's
// resurrected after time has passed is rejected for the same reasons a
// fresh request would be.
func (db *DB) validateRequestDate(dateTime time.Time) error {
	// Reject holidays: a WFH on a non-working day is meaningless — there's no
	// on-site capacity to consume and no presence to track. Fail fast at the
	// data layer so the invariant is enforced regardless of the caller.
	if db.IsHoliday(dateTime) {
		return ErrWFHOnHoliday
	}

	nowUTC := time.Now().UTC()
	today := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	if dateTime.Before(today) {
		return ErrWFHDatePassed
	}
	return nil
}

// resurrectOrDuplicate handles the UNIQUE-constraint path inside
// CreateWFHRequest. If the existing row for (member, date) is in a state
// the user owns — 'cancelled' or self-withdrawn ('withdrawn' with
// withdrawn_by IS NULL) — flip it back to 'pending' and clear the audit
// fields (plus is_recurring, so a self-withdrawn recurring day is treated
// as an ad-hoc request rather than getting stuck in pending with neither
// settlement nor the materializer able to advance it). The user can then
// re-request after changing their mind. Any other status (pending,
// approved, denied, admin-withdrawn) surfaces the existing
// ErrWFHDuplicateRequest: admin withdrawals are intentionally not
// resurrectable because they represent an admin's decision the user
// should not be able to override by re-requesting.
func (db *DB) resurrectOrDuplicate(ctx context.Context, memberID string, dateTime time.Time) (*WFHRequest, error) {
	// Re-run the data-layer invariants: the date may have moved into the
	// past, or been added to the holiday set, between the original
	// cancel/withdraw and the re-request. A resurrected row that fails
	// these checks leaves the user with a pending request for an invalid
	// date — the same state a fresh insert would reject.
	if err := db.validateRequestDate(dateTime); err != nil {
		return nil, err
	}

	existing, err := db.queries.GetWFHRequestByMemberAndDate(ctx, sqlc.GetWFHRequestByMemberAndDateParams{
		MemberID: memberID,
		Date:     dateTime,
	})
	if err != nil {
		// Lookup failure (DB error or, in a race, no row) — preserve the
		// original caller-visible behavior: a duplicate insert surfaced
		// as "already exists".
		return nil, ErrWFHDuplicateRequest
	}
	if !isUserResurrectable(existing.Status, existing.WithdrawnBy) {
		return nil, ErrWFHDuplicateRequest
	}
	res, err := db.queries.ResurrectWFHRequest(ctx, existing.ID)
	if err != nil {
		return nil, err
	}
	// The SQL has a defensive status guard so a concurrent change to the
	// row between the SELECT and the UPDATE will leave it untouched. In
	// that case 0 rows are affected; treat the same as a non-resurrectable
	// duplicate rather than silently returning the un-resurrected row.
	rows, raErr := res.RowsAffected()
	if raErr != nil {
		return nil, raErr
	}
	if rows == 0 {
		return nil, ErrWFHDuplicateRequest
	}
	return db.GetWFHRequestByID(ctx, existing.ID)
}

// isUserResurrectable reports whether a row in the given status (with the
// given withdrawn_by value) is eligible to be resurrected by the owning
// user. Cancelled rows and self-withdrawn rows (no recorded actor) are
// user-owned and resurrectable; everything else is a final decision the
// user should not be able to override.
func isUserResurrectable(status string, withdrawnBy sql.NullString) bool {
	switch status {
	case WFHStatusCancelled:
		return true
	case WFHStatusWithdrawn:
		return !withdrawnBy.Valid
	default:
		return false
	}
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
		ID:            row.ID,
		MemberID:      row.MemberID,
		Date:          row.Date,
		Status:        row.Status,
		CreatedAt:     row.CreatedAt,
		SettledAt:     row.SettledAt,
		WithdrawnBy:   row.WithdrawnBy,
		WithdrawnAt:   row.WithdrawnAt,
		IsRecurring:   row.IsRecurring,
		IsAdminMarked: row.IsAdminMarked,
		MarkedBy:      row.MarkedBy,
		MarkedAt:      row.MarkedAt,
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
			ID:            rows[i].ID,
			MemberID:      rows[i].MemberID,
			Date:          rows[i].Date,
			Status:        rows[i].Status,
			CreatedAt:     rows[i].CreatedAt,
			SettledAt:     rows[i].SettledAt,
			WithdrawnBy:   rows[i].WithdrawnBy,
			WithdrawnAt:   rows[i].WithdrawnAt,
			IsRecurring:   rows[i].IsRecurring,
			IsAdminMarked: rows[i].IsAdminMarked,
			MarkedBy:      rows[i].MarkedBy,
			MarkedAt:      rows[i].MarkedAt,
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
			ID:            rows[i].ID,
			MemberID:      rows[i].MemberID,
			Date:          rows[i].Date,
			Status:        rows[i].Status,
			CreatedAt:     rows[i].CreatedAt,
			SettledAt:     rows[i].SettledAt,
			WithdrawnBy:   rows[i].WithdrawnBy,
			WithdrawnAt:   rows[i].WithdrawnAt,
			IsRecurring:   rows[i].IsRecurring,
			IsAdminMarked: rows[i].IsAdminMarked,
			MarkedBy:      rows[i].MarkedBy,
			MarkedAt:      rows[i].MarkedAt,
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
			ID:            rows[i].ID,
			MemberID:      rows[i].MemberID,
			Date:          rows[i].Date,
			Status:        rows[i].Status,
			CreatedAt:     rows[i].CreatedAt,
			SettledAt:     rows[i].SettledAt,
			WithdrawnBy:   rows[i].WithdrawnBy,
			WithdrawnAt:   rows[i].WithdrawnAt,
			IsRecurring:   rows[i].IsRecurring,
			IsAdminMarked: rows[i].IsAdminMarked,
			MarkedBy:      rows[i].MarkedBy,
			MarkedAt:      rows[i].MarkedAt,
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
			ID:            rows[i].ID,
			MemberID:      rows[i].MemberID,
			Date:          rows[i].Date,
			Status:        rows[i].Status,
			CreatedAt:     rows[i].CreatedAt,
			SettledAt:     rows[i].SettledAt,
			WithdrawnBy:   rows[i].WithdrawnBy,
			WithdrawnAt:   rows[i].WithdrawnAt,
			IsRecurring:   rows[i].IsRecurring,
			IsAdminMarked: rows[i].IsAdminMarked,
			MarkedBy:      rows[i].MarkedBy,
			MarkedAt:      rows[i].MarkedAt,
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
			ID:            rows[i].ID,
			MemberID:      rows[i].MemberID,
			Date:          rows[i].Date,
			Status:        rows[i].Status,
			CreatedAt:     rows[i].CreatedAt,
			SettledAt:     rows[i].SettledAt,
			WithdrawnBy:   rows[i].WithdrawnBy,
			WithdrawnAt:   rows[i].WithdrawnAt,
			IsRecurring:   rows[i].IsRecurring,
			IsAdminMarked: rows[i].IsAdminMarked,
			MarkedBy:      rows[i].MarkedBy,
			MarkedAt:      rows[i].MarkedAt,
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
			ID:            rows[i].ID,
			MemberID:      rows[i].MemberID,
			Date:          rows[i].Date,
			Status:        rows[i].Status,
			CreatedAt:     rows[i].CreatedAt,
			SettledAt:     rows[i].SettledAt,
			WithdrawnBy:   rows[i].WithdrawnBy,
			WithdrawnAt:   rows[i].WithdrawnAt,
			IsRecurring:   rows[i].IsRecurring,
			IsAdminMarked: rows[i].IsAdminMarked,
			MarkedBy:      rows[i].MarkedBy,
			MarkedAt:      rows[i].MarkedAt,
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

// MarkAdminWFH inserts an admin-asserted WFH row in one shot. The
// row is created with status='approved' and is_admin_marked=1 so
// every downstream query (quota, floor, ICS, dashboard presence)
// picks it up unchanged. The UNIQUE(member_id, date) constraint
// guarantees idempotency: a second mark for the same (member, date)
// returns the UNIQUE-constraint error which the service layer
// translates into ErrWFHDuplicateRequest. We do NOT pre-check for
// the existing row here — the INSERT-and-catch-UNIQUE pattern is
// the race-safe way to handle concurrent admin marks against the
// same member on the same day.
func (db *DB) MarkAdminWFH(ctx context.Context, id, memberID, date, adminUserID string) error {
	dateTime, err := time.Parse("2006-01-02", date)
	if err != nil {
		return ErrWFHInvalidDate
	}
	markedAt := time.Now().UTC()
	_, err = db.queries.MarkAdminWFH(ctx, sqlc.MarkAdminWFHParams{
		ID:       id,
		MemberID: memberID,
		Date:     dateTime,
		MarkedBy: sql.NullString{String: adminUserID, Valid: adminUserID != ""},
		MarkedAt: sql.NullTime{Time: markedAt, Valid: true},
	})
	if isUniqueConstraintError(err) {
		return ErrWFHDuplicateRequest
	}
	return err
}

// IsAdminMarkedWFH returns true if a row with is_admin_marked=1
// exists for the given (member_id, date). Used by the dashboard
// presence builder to pick the right chip color.
func (db *DB) IsAdminMarkedWFH(ctx context.Context, memberID, date string) (bool, error) {
	dateTime, err := time.Parse("2006-01-02", date)
	if err != nil {
		return false, ErrWFHInvalidDate
	}
	flag, err := db.queries.IsAdminMarkedWFH(ctx, sqlc.IsAdminMarkedWFHParams{
		MemberID: memberID,
		Date:     dateTime,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return flag == 1, nil
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
	nowUTC := time.Now().UTC()
	today := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	if wfhDate.UTC().Before(today) {
		return ErrWFHDatePassed
	}

	_, err = db.queries.UpdateWFHRequestWithdrawn(ctx, sqlc.UpdateWFHRequestWithdrawnParams{
		WithdrawnBy: sql.NullString{String: actorUserID, Valid: actorUserID != ""},
		ID:          id,
	})
	return err
}
