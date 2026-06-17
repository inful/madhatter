package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
)

const maxDateLength = 10

func (db *DB) CreateRotaAssignment(ctx context.Context, date, memberID string, isCover bool, originalAssignmentID *string) (string, error) {
	if date == "" || memberID == "" {
		return "", errors.New("date and member_id are required")
	}

	// Verify member exists
	_, err := db.queries.GetMemberByID(ctx, memberID)
	if err != nil {
		return "", errors.New("member not found")
	}

	id := uuid.New().String()
	dateTime, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", err
	}

	params := sqlc.CreateRotaAssignmentParams{
		ID:       id,
		Date:     dateTime,
		MemberID: memberID,
		IsCover:  sql.NullInt64{Int64: boolToInt(isCover), Valid: true},
	}

	if originalAssignmentID != nil {
		params.OriginalAssignmentID = sql.NullString{String: *originalAssignmentID, Valid: true}
	}

	_, err = db.queries.CreateRotaAssignment(ctx, params)
	return id, err
}

func (db *DB) GetAssignmentsByDate(ctx context.Context, date string) ([]RotaAssignment, error) {
	dateTime, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, err
	}

	assignments, err := db.queries.GetAssignmentsByDate(ctx, dateTime)
	if err != nil {
		return nil, err
	}

	result := make([]RotaAssignment, len(assignments))
	for i := range assignments {
		a := &assignments[i]
		result[i] = RotaAssignment{
			ID:                   a.ID,
			Date:                 a.Date.Format("2006-01-02"),
			MemberID:             a.MemberID,
			IsCover:              a.IsCover.Valid && a.IsCover.Int64 == 1,
			IsSwapped:            a.IsSwapped == 1,
			OriginalAssignmentID: getNullString(a.OriginalAssignmentID),
			CreatedAt:            time.Time{},
			MemberName:           a.MemberName,
			MemberEmail:          a.MemberEmail,
		}
	}
	return result, nil
}

// GetAssignmentsByDateRange returns all assignments between start and end dates (inclusive).
func (db *DB) GetAssignmentsByDateRange(ctx context.Context, startDate, endDate string) ([]RotaAssignment, error) {
	startDateTime, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return nil, err
	}

	endDateTime, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return nil, err
	}

	params := sqlc.GetAssignmentsByDateRangeParams{
		Date:   startDateTime,
		Date_2: endDateTime,
	}

	assignments, err := db.queries.GetAssignmentsByDateRange(ctx, params)
	if err != nil {
		return nil, err
	}

	result := make([]RotaAssignment, len(assignments))
	for i := range assignments {
		a := &assignments[i]
		result[i] = RotaAssignment{
			ID:                   a.ID,
			Date:                 a.Date.Format("2006-01-02"),
			MemberID:             a.MemberID,
			IsCover:              a.IsCover.Valid && a.IsCover.Int64 == 1,
			IsSwapped:            a.IsSwapped == 1,
			OriginalAssignmentID: getNullString(a.OriginalAssignmentID),
			CreatedAt:            time.Time{},
			MemberName:           a.MemberName,
			MemberEmail:          a.MemberEmail,
		}
	}
	return result, nil
}

// GetMostRecentCoverMemberID was removed when the cover rotation
// switched to a date-derivable index (see Engine.coverRotationIndex
// in internal/rota). The DB-anchored rotation was the source of the
// "same person always covers" bug and is no longer consulted.

// GetCoverRotationState returns the last computed cover rotation state
// (last_date, last_index). Returns sql.ErrNoRows if no state has been
// written yet; the caller is responsible for initializing the state
// row on first use.
func (db *DB) GetCoverRotationState(ctx context.Context) (time.Time, int, error) {
	row, err := db.queries.GetCoverRotationState(ctx)
	if err != nil {
		return time.Time{}, 0, err
	}
	return row.LastDate, int(row.LastIndex), nil
}

// UpsertCoverRotationState writes the cover rotation state. The table
// is constrained to a single row, so this is the only write path.
func (db *DB) UpsertCoverRotationState(ctx context.Context, date time.Time, index int) error {
	return db.queries.UpsertCoverRotationState(ctx, sqlc.UpsertCoverRotationStateParams{
		LastDate:  date,
		LastIndex: int64(index),
	})
}

// GetReassignmentAnchor returns the ReassignCovers-only anchor
// (last_reassign_date, last_reassign_index). The valid flag is true
// only when both fields are populated. sql.ErrNoRows and a row with
// NULL columns are both treated as "no prior reassign" (valid=false,
// err=nil) so callers can seed the anchor at the first leave they
// process without special-casing the empty-database path.
func (db *DB) GetReassignmentAnchor(ctx context.Context) (date time.Time, index int, valid bool, err error) {
	row, qErr := db.queries.GetReassignmentAnchor(ctx)
	if qErr != nil {
		if errors.Is(qErr, sql.ErrNoRows) {
			return time.Time{}, 0, false, nil
		}
		return time.Time{}, 0, false, qErr
	}
	if !row.LastReassignDate.Valid || !row.LastReassignIndex.Valid {
		return time.Time{}, 0, false, nil
	}
	return row.LastReassignDate.Time, int(row.LastReassignIndex.Int64), true, nil
}

// WriteReassignmentAnchor ensures the cover_rotation_state row
// exists (so the subsequent UPDATE has something to write to on a
// fresh DB) and then updates the reassign anchor columns. The
// ad-hoc last_date / last_index columns are NEVER read or written
// by this path, so a concurrent ad-hoc HandleLeaveChange is safe
// — there is no read-modify-write window in which a stale ad-hoc
// value could be re-written. On a fresh database the row is
// created with NULL ad-hoc columns (last_date, last_index became
// nullable in migration 000021 for exactly this reason); the
// first subsequent ad-hoc HandleLeaveChange populates them.
func (db *DB) WriteReassignmentAnchor(ctx context.Context, reassignDate time.Time, reassignIndex int) error {
	if err := db.queries.EnsureReassignmentAnchorRow(ctx); err != nil {
		return fmt.Errorf("ensure reassign anchor row: %w", err)
	}
	return db.queries.UpdateReassignmentAnchor(ctx, sqlc.UpdateReassignmentAnchorParams{
		LastReassignDate:  sql.NullTime{Time: reassignDate, Valid: true},
		LastReassignIndex: sql.NullInt64{Int64: int64(reassignIndex), Valid: true},
	})
}

// GetR1RotationState returns the R1 (original-HAT) rotation state
// (last_date, last_index). Returns sql.ErrNoRows if no R1
// assignment has been written yet. The R1 state lives in its own
// table (r1_rotation_state) so its write rules don't collide with
// the cover rotation's.
func (db *DB) GetR1RotationState(ctx context.Context) (time.Time, int, error) {
	row, err := db.queries.GetR1RotationState(ctx)
	if err != nil {
		return time.Time{}, 0, err
	}
	return row.LastDate, int(row.LastIndex), nil
}

// UpsertR1RotationState writes the R1 rotation state. The table
// holds at most one row, so this either inserts the first row or
// replaces the existing one.
func (db *DB) UpsertR1RotationState(ctx context.Context, date time.Time, index int) error {
	return db.queries.UpsertR1RotationState(ctx, sqlc.UpsertR1RotationStateParams{
		LastDate:  date,
		LastIndex: int64(index),
	})
}

// GetLatestAssignmentDate returns the latest date that has any assignments.
// Returns empty string if no assignments exist.
func (db *DB) GetLatestAssignmentDate(ctx context.Context) (string, error) {
	// Query for the maximum date in rota_assignments
	var maxDateStr sql.NullString

	query := `SELECT MAX(date) as max_date FROM rota_assignments`
	err := db.db.QueryRowContext(ctx, query).Scan(&maxDateStr)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}

	// Check if we got a valid result
	if !maxDateStr.Valid {
		return "", nil
	}

	// Parse the date string to ensure it's in the correct format
	// SQLite may return "2025-01-16 00:00:00 +0000 UTC" format
	dateStr := maxDateStr.String
	if len(dateStr) > maxDateLength {
		// Extract just the date part (YYYY-MM-DD)
		parsed, err := time.Parse("2006-01-02", dateStr[:maxDateLength])
		if err != nil {
			return "", err
		}
		return parsed.Format("2006-01-02"), nil
	}

	return dateStr, nil
}

// DeleteAssignmentsInRange deletes all assignments within the specified date range (inclusive).
func (db *DB) DeleteAssignmentsInRange(ctx context.Context, startDate, endDate string) error {
	startDateTime, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return err
	}

	endDateTime, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return err
	}

	params := sqlc.DeleteAssignmentsByDateRangeParams{
		Date:   startDateTime,
		Date_2: endDateTime,
	}

	return db.queries.DeleteAssignmentsByDateRange(ctx, params)
}

// GetAssignmentByID returns a single RotaAssignment by its ID.
func (db *DB) GetAssignmentByID(ctx context.Context, id string) (*RotaAssignment, error) {
	a, err := db.queries.GetAssignmentByID(ctx, id)
	if err != nil {
		return nil, err
	}

	return &RotaAssignment{
		ID:                   a.ID,
		Date:                 a.Date.Format("2006-01-02"),
		MemberID:             a.MemberID,
		IsCover:              a.IsCover.Valid && a.IsCover.Int64 == 1,
		IsSwapped:            a.IsSwapped == 1,
		OriginalAssignmentID: getNullString(a.OriginalAssignmentID),
	}, nil
}

// GetFutureAssignmentsForMember returns all upcoming (today onwards) assignments for one member.
func (db *DB) GetFutureAssignmentsForMember(ctx context.Context, memberID string) ([]RotaAssignment, error) {
	rows, err := db.queries.GetFutureAssignmentsForMember(ctx, memberID)
	if err != nil {
		return nil, err
	}

	result := make([]RotaAssignment, len(rows))
	for i := range rows {
		a := &rows[i]
		result[i] = RotaAssignment{
			ID:          a.ID,
			Date:        a.Date.Format("2006-01-02"),
			MemberID:    a.MemberID,
			IsCover:     a.IsCover.Valid && a.IsCover.Int64 == 1,
			IsSwapped:   a.IsSwapped == 1,
			MemberName:  a.MemberName,
			MemberEmail: a.MemberEmail,
		}
	}

	return result, nil
}

// GetFutureAssignments returns all upcoming (today onwards) assignments for all members.
func (db *DB) GetFutureAssignments(ctx context.Context) ([]RotaAssignment, error) {
	rows, err := db.queries.GetFutureAssignments(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]RotaAssignment, len(rows))
	for i := range rows {
		a := &rows[i]
		result[i] = RotaAssignment{
			ID:          a.ID,
			Date:        a.Date.Format("2006-01-02"),
			MemberID:    a.MemberID,
			IsCover:     a.IsCover.Valid && a.IsCover.Int64 == 1,
			IsSwapped:   a.IsSwapped == 1,
			MemberName:  a.MemberName,
			MemberEmail: a.MemberEmail,
		}
	}

	return result, nil
}
