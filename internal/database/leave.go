package database

import (
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

func (db *DB) CreateLeaveRecord(memberID, leaveType, startDate, endDate string) (string, error) {
	if memberID == "" || leaveType == "" || startDate == "" || endDate == "" {
		return "", fmt.Errorf("all fields are required")
	}

	// Verify member exists
	var exists bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM team_members WHERE id = ?)", memberID).Scan(&exists)
	if err != nil || !exists {
		return "", fmt.Errorf("member not found")
	}

	id := uuid.New().String()
	query := `INSERT INTO leave_records (id, member_id, type, start_date, end_date, status)
              VALUES (?, ?, ?, ?, ?, 'pending')`
	_, err = db.Exec(query, id, memberID, leaveType, startDate, endDate)
	return id, err
}

func (db *DB) GetLeaveByDate(date string) ([]LeaveRecord, error) {
	query := `SELECT id, member_id, start_date, end_date, type, cover_member_id, status, created_at
              FROM leave_records 
              WHERE ? >= start_date AND ? <= end_date AND status != 'completed'`
	rows, err := db.Query(query, date, date)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var leaves []LeaveRecord
	for rows.Next() {
		var l LeaveRecord
		var coverMemberID sql.NullString
		err := rows.Scan(&l.ID, &l.MemberID, &l.StartDate, &l.EndDate, &l.Type, &coverMemberID, &l.Status, &l.CreatedAt)
		if err != nil {
			return nil, err
		}
		if coverMemberID.Valid {
			l.CoverMemberID = coverMemberID.String
		}
		leaves = append(leaves, l)
	}
	return leaves, nil
}

func (db *DB) UpdateLeaveStatus(leaveID, status string) error {
	query := `UPDATE leave_records SET status = ? WHERE id = ?`
	_, err := db.Exec(query, status, leaveID)
	return err
}

func (db *DB) GetLeaveByID(leaveID string) (*LeaveRecord, error) {
	query := `SELECT id, member_id, start_date, end_date, type, cover_member_id, status, created_at
              FROM leave_records WHERE id = ?`
	row := db.QueryRow(query, leaveID)

	var l LeaveRecord
	var coverMemberID sql.NullString
	err := row.Scan(&l.ID, &l.MemberID, &l.StartDate, &l.EndDate, &l.Type, &coverMemberID, &l.Status, &l.CreatedAt)
	if err != nil {
		return nil, err
	}
	if coverMemberID.Valid {
		l.CoverMemberID = coverMemberID.String
	}
	return &l, nil
}
