package database

import (
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

func (db *DB) CreateRotaAssignment(date, memberID string, isCover bool, leaveID *string) (string, error) {
	if date == "" || memberID == "" {
		return "", fmt.Errorf("date and member_id are required")
	}

	// Verify member exists
	var exists bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM team_members WHERE id = ?)", memberID).Scan(&exists)
	if err != nil || !exists {
		return "", fmt.Errorf("member not found")
	}

	id := uuid.New().String()
	isCoverInt := 0
	if isCover {
		isCoverInt = 1
	}

	var leaveIDNull sql.NullString
	if leaveID != nil {
		leaveIDNull = sql.NullString{String: *leaveID, Valid: true}
	}

	query := `INSERT INTO rota_assignments (id, date, member_id, is_cover, leave_id)
	             VALUES (?, ?, ?, ?, ?)`
	_, err = db.Exec(query, id, date, memberID, isCoverInt, leaveIDNull)
	return id, err
}

func (db *DB) GetAssignmentsByDate(date string) ([]RotaAssignment, error) {
	query := `SELECT ra.id, ra.date, ra.member_id, ra.is_cover, ra.leave_id, tm.name, tm.email
              FROM rota_assignments ra
              JOIN team_members tm ON ra.member_id = tm.id
              WHERE ra.date = ?`
	rows, err := db.Query(query, date)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var assignments []RotaAssignment
	for rows.Next() {
		var a RotaAssignment
		var leaveID sql.NullString
		err := rows.Scan(&a.ID, &a.Date, &a.MemberID, &a.IsCover, &leaveID, &a.MemberName, &a.MemberEmail)
		if err != nil {
			return nil, err
		}
		if leaveID.Valid {
			a.LeaveID = &leaveID.String
		}
		assignments = append(assignments, a)
	}
	return assignments, nil
}

func (db *DB) GetUpcomingAssignments(memberID string, days int) ([]RotaAssignment, error) {
	query := `SELECT id, date, member_id, is_cover, leave_id
              FROM rota_assignments
              WHERE member_id = ? AND date >= date('now') AND date <= date('now', '+'||?||' days')
              ORDER BY date`
	rows, err := db.Query(query, memberID, days)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var assignments []RotaAssignment
	for rows.Next() {
		var a RotaAssignment
		var leaveID sql.NullString
		err := rows.Scan(&a.ID, &a.Date, &a.MemberID, &a.IsCover, &leaveID)
		if err != nil {
			return nil, err
		}
		if leaveID.Valid {
			a.LeaveID = &leaveID.String
		}
		assignments = append(assignments, a)
	}
	return assignments, nil
}
