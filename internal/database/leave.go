package database

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
)

func (db *DB) CreateLeaveRecord(memberID, leaveType, startDate, endDate string) (string, error) {
	if memberID == "" || leaveType == "" || startDate == "" || endDate == "" {
		return "", errors.New("all fields are required")
	}

	// Verify member exists
	_, err := db.queries.GetMemberByID(context.Background(), memberID)
	if err != nil {
		return "", errors.New("member not found")
	}

	id := uuid.New().String()

	startTime, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return "", err
	}
	endTime, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return "", err
	}

	params := sqlc.CreateLeaveRecordParams{
		ID:        id,
		MemberID:  memberID,
		Type:      leaveType,
		StartDate: startTime,
		EndDate:   endTime,
	}

	_, err = db.queries.CreateLeaveRecord(context.Background(), params)
	return id, err
}

func (db *DB) GetLeaveByDate(date string) ([]LeaveRecord, error) {
	// Parse date to time.Time for sqlc
	dateTime, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, err
	}

	params := sqlc.GetLeaveByDateParams{
		StartDate: dateTime,
		EndDate:   dateTime,
	}

	leaveRecords, err := db.queries.GetLeaveByDate(context.Background(), params)
	if err != nil {
		return nil, err
	}

	result := make([]LeaveRecord, len(leaveRecords))
	for i := range leaveRecords {
		l := &leaveRecords[i]
		coverMemberID := ""
		if l.CoverMemberID.Valid {
			coverMemberID = l.CoverMemberID.String
		}
		result[i] = LeaveRecord{
			ID:            l.ID,
			MemberID:      l.MemberID,
			StartDate:     l.StartDate,
			EndDate:       l.EndDate,
			Type:          l.Type,
			CoverMemberID: coverMemberID,
			Status:        l.Status,
			CreatedAt:     l.CreatedAt.Time,
		}
	}
	return result, nil
}

func (db *DB) UpdateLeaveStatus(leaveID, status string) error {
	params := sqlc.UpdateLeaveStatusParams{
		Status: status,
		ID:     leaveID,
	}
	return db.queries.UpdateLeaveStatus(context.Background(), params)
}

func (db *DB) GetLeaveByID(leaveID string) (*LeaveRecord, error) {
	leave, err := db.queries.GetLeaveByID(context.Background(), leaveID)
	if err != nil {
		return nil, err
	}

	coverMemberID := ""
	if leave.CoverMemberID.Valid {
		coverMemberID = leave.CoverMemberID.String
	}
	return &LeaveRecord{
		ID:            leave.ID,
		MemberID:      leave.MemberID,
		StartDate:     leave.StartDate,
		EndDate:       leave.EndDate,
		Type:          leave.Type,
		CoverMemberID: coverMemberID,
		Status:        leave.Status,
		CreatedAt:     leave.CreatedAt.Time,
	}, nil
}

// GetLeaveRecords retrieves all leave records (optionally filtered by status).
func (db *DB) GetLeaveRecords(statusFilter ...string) ([]LeaveRecord, error) {
	params := sqlc.GetLeaveRecordsParams{
		Status:  "",
		Column2: "",
	}
	if len(statusFilter) > 0 {
		params.Status = statusFilter[0]
		params.Column2 = statusFilter[0]
	}

	leaveRecords, err := db.queries.GetLeaveRecords(context.Background(), params)
	if err != nil {
		return nil, err
	}

	result := make([]LeaveRecord, len(leaveRecords))
	for i := range leaveRecords {
		l := &leaveRecords[i]
		coverMemberID := ""
		if l.CoverMemberID.Valid {
			coverMemberID = l.CoverMemberID.String
		}
		result[i] = LeaveRecord{
			ID:            l.ID,
			MemberID:      l.MemberID,
			StartDate:     l.StartDate,
			EndDate:       l.EndDate,
			Type:          l.Type,
			CoverMemberID: coverMemberID,
			Status:        l.Status,
			CreatedAt:     l.CreatedAt.Time,
		}
	}
	return result, nil
}
