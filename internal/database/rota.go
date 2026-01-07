package database

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

func (db *DB) CreateRotaAssignment(date, memberID string, isCover bool, originalAssignmentID *string) (string, error) {
	if date == "" || memberID == "" {
		return "", errors.New("date and member_id are required")
	}

	ctx := context.Background()

	// Verify member exists
	var exists bool
	err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM team_members WHERE id = ?)", memberID).Scan(&exists)
	if err != nil || !exists {
		return "", errors.New("member not found")
	}

	id := uuid.New().String()
	isCoverInt := 0
	if isCover {
		isCoverInt = 1
	}

	var origIDNull sql.NullString
	if originalAssignmentID != nil {
		origIDNull = sql.NullString{String: *originalAssignmentID, Valid: true}
	}

	query := `INSERT INTO rota_assignments (id, date, member_id, is_cover, original_assignment_id)
	             VALUES (?, ?, ?, ?, ?)`
	_, err = db.ExecContext(ctx, query, id, date, memberID, isCoverInt, origIDNull)
	return id, err
}

func (db *DB) GetAssignmentsByDate(date string) ([]RotaAssignment, error) {
	ctx := context.Background()
	query := `SELECT ra.id, ra.date, ra.member_id, ra.is_cover, ra.original_assignment_id, tm.name, tm.email
	             FROM rota_assignments ra
	             JOIN team_members tm ON ra.member_id = tm.id
	             WHERE ra.date = ?`
	rows, err := db.QueryContext(ctx, query, date)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var assignments []RotaAssignment
	for rows.Next() {
		var a RotaAssignment
		var originalAssignmentID sql.NullString
		err := rows.Scan(&a.ID, &a.Date, &a.MemberID, &a.IsCover, &originalAssignmentID, &a.MemberName, &a.MemberEmail)
		if err != nil {
			return nil, err
		}
		if originalAssignmentID.Valid {
			a.OriginalAssignmentID = &originalAssignmentID.String
		}
		assignments = append(assignments, a)
	}
	return assignments, nil
}
