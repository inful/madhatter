package database

import (
	"context"
	"database/sql"
	"errors"
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
			OriginalAssignmentID: getNullString(a.OriginalAssignmentID),
			CreatedAt:            time.Time{},
			MemberName:           a.MemberName,
			MemberEmail:          a.MemberEmail,
		}
	}
	return result, nil
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
