package database

import (
	"context"
	"time"
)

// CountApprovedWFHByDate returns the number of approved WFH requests for a given date.
func (db *DB) CountApprovedWFHByDate(ctx context.Context, date string) (int, error) {
	dateTime, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0, ErrWFHInvalidDate
	}
	count, err := db.queries.CountApprovedWFHByDate(ctx, dateTime)
	return int(count), err
}

// CountWFHRequestsBefore returns the number of wfh_requests rows whose
// date is strictly before cutoff. Used by the past-period purge dry-run
// to preview the affected row count without touching the table.
func (db *DB) CountWFHRequestsBefore(ctx context.Context, cutoff string) (int64, error) {
	cutoffTime, err := time.Parse("2006-01-02", cutoff)
	if err != nil {
		return 0, ErrWFHInvalidDate
	}
	return db.queries.CountWFHRequestsBefore(ctx, cutoffTime)
}

// PurgeWFHRequestsBefore hard-deletes every wfh_requests row whose date
// is strictly before cutoff. Returns the number of rows deleted. This is
// a no-recovery operation — callers wanting a preview should call
// CountWFHRequestsBefore first.
func (db *DB) PurgeWFHRequestsBefore(ctx context.Context, cutoff string) (int64, error) {
	cutoffTime, err := time.Parse("2006-01-02", cutoff)
	if err != nil {
		return 0, ErrWFHInvalidDate
	}
	res, err := db.queries.PurgeWFHRequestsBefore(ctx, cutoffTime)
	if err != nil {
		return 0, err
	}
	rows, rowsErr := res.RowsAffected()
	if rowsErr != nil {
		return 0, rowsErr
	}
	return rows, nil
}
