package database

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
)

func (db *DB) CreateLeaveRecord(ctx context.Context, memberID, startDate, endDate string) (string, error) {
	if memberID == "" || startDate == "" || endDate == "" {
		return "", errors.New("memberID, startDate, and endDate are required")
	}

	// Verify member exists
	_, err := db.queries.GetMemberByID(ctx, memberID)
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
		StartDate: startTime,
		EndDate:   endTime,
	}

	_, err = db.queries.CreateLeaveRecord(ctx, params)
	return id, err
}

func (db *DB) GetLeaveByDate(ctx context.Context, date string) ([]LeaveRecord, error) {
	// Parse date to time.Time for sqlc
	dateTime, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, err
	}

	params := sqlc.GetLeaveByDateParams{
		StartDate: dateTime,
		EndDate:   dateTime,
	}

	leaveRecords, err := db.queries.GetLeaveByDate(ctx, params)
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
			CoverMemberID: coverMemberID,
			Status:        l.Status,
			CreatedAt:     l.CreatedAt.Time,
		}
	}
	return result, nil
}

func (db *DB) UpdateLeaveStatus(ctx context.Context, leaveID, status string) error {
	params := sqlc.UpdateLeaveStatusParams{
		Status: status,
		ID:     leaveID,
	}
	return db.queries.UpdateLeaveStatus(ctx, params)
}

func (db *DB) GetLeaveByID(ctx context.Context, leaveID string) (*LeaveRecord, error) {
	leave, err := db.queries.GetLeaveByID(ctx, leaveID)
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
		CoverMemberID: coverMemberID,
		Status:        leave.Status,
		CreatedAt:     leave.CreatedAt.Time,
	}, nil
}

// GetLeaveRecords retrieves all leave records (optionally filtered by status).
func (db *DB) GetLeaveRecords(ctx context.Context, statusFilter ...string) ([]LeaveRecord, error) {
	params := sqlc.GetLeaveRecordsParams{
		Status:  "",
		Column2: "",
	}
	if len(statusFilter) > 0 {
		params.Status = statusFilter[0]
		params.Column2 = statusFilter[0]
	}

	leaveRecords, err := db.queries.GetLeaveRecords(ctx, params)
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
			CoverMemberID: coverMemberID,
			Status:        l.Status,
			CreatedAt:     l.CreatedAt.Time,
		}
	}
	return result, nil
}

// parseLeaveDates parses and validates leave date strings.
func parseLeaveDates(startDate, endDate string) (time.Time, time.Time, error) {
	startTime, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	endTime, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	// Validate date ordering
	if endTime.Before(startTime) {
		return time.Time{}, time.Time{}, errors.New("endDate must be on or after startDate")
	}

	return startTime, endTime, nil
}

func (db *DB) UpdateLeaveRecord(ctx context.Context, leaveID, memberID, startDate, endDate, status string) error {
	if leaveID == "" || memberID == "" || startDate == "" || endDate == "" || status == "" {
		return errors.New("leaveID, memberID, startDate, endDate, and status are required")
	}

	// Verify member exists
	_, err := db.queries.GetMemberByID(ctx, memberID)
	if err != nil {
		return errors.New("member not found")
	}

	// Parse and validate dates
	startTime, endTime, err := parseLeaveDates(startDate, endDate)
	if err != nil {
		return err
	}
	if endTime.Before(startTime) {
		return errors.New("endDate must be on or after startDate")
	}

	params := sqlc.UpdateLeaveRecordParams{
		MemberID:  memberID,
		StartDate: startTime,
		EndDate:   endTime,
		Status:    status,
		ID:        leaveID,
	}

	return db.queries.UpdateLeaveRecord(ctx, params)
}

func (db *DB) DeleteLeaveRecord(ctx context.Context, leaveID string) error {
	if leaveID == "" {
		return errors.New("leaveID cannot be empty")
	}

	return db.queries.DeleteLeaveRecord(ctx, leaveID)
}

// hoursPerDay is the number of hours in a day.
const hoursPerDay = 24

// DeleteExpiredLeaveRecords removes leave records whose end date is before today.
func (db *DB) DeleteExpiredLeaveRecords(ctx context.Context) error {
	today := time.Now().UTC().Truncate(hoursPerDay * time.Hour)
	return db.queries.DeleteExpiredLeaveRecords(ctx, today)
}
