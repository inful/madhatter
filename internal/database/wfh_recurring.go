package database

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
)

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
