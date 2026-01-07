package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
)

func (db *DB) CreateRotaAssignment(date, memberID string, isCover bool, originalAssignmentID *string) (string, error) {
	if date == "" || memberID == "" {
		return "", errors.New("date and member_id are required")
	}

	// Verify member exists
	_, err := db.queries.GetMemberByID(context.Background(), memberID)
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

	_, err = db.queries.CreateRotaAssignment(context.Background(), params)
	return id, err
}

func (db *DB) GetAssignmentsByDate(date string) ([]RotaAssignment, error) {
	dateTime, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, err
	}

	assignments, err := db.queries.GetAssignmentsByDate(context.Background(), dateTime)
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
			CreatedAt:            time.Now(),
			MemberName:           a.MemberName,
			MemberEmail:          a.MemberEmail,
		}
	}
	return result, nil
}
