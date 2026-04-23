package database

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
)

var (
	ErrSwapNotPending     = errors.New("swap is no longer pending")
	ErrSwapDatePassed     = errors.New("one of the HAT days has already passed and cannot be swapped")
	ErrSwapSameMember     = errors.New("swap target must belong to another member")
	ErrSwapAssignmentBusy = errors.New("one of the assignments already has an open swap request")
)

const dbHoursInDay = 24

// CreateHatSwap creates a new pending HAT day swap request.
func (db *DB) CreateHatSwap(ctx context.Context, requesterAssignmentID, targetAssignmentID, requesterMemberID, targetMemberID string) (string, error) {
	if requesterAssignmentID == "" || targetAssignmentID == "" || requesterMemberID == "" || targetMemberID == "" {
		return "", errors.New("all swap fields are required")
	}

	if requesterMemberID == targetMemberID {
		return "", ErrSwapSameMember
	}

	id := uuid.New().String()

	_, err := db.queries.CreateHatSwap(ctx, sqlc.CreateHatSwapParams{
		ID:                    id,
		RequesterAssignmentID: requesterAssignmentID,
		TargetAssignmentID:    targetAssignmentID,
		RequesterMemberID:     requesterMemberID,
		TargetMemberID:        targetMemberID,
	})
	if err != nil {
		if strings.Contains(err.Error(), "pending swap already exists") {
			return "", ErrSwapAssignmentBusy
		}

		return "", err
	}

	return id, nil
}

// GetHatSwapByID returns a single HatSwap by ID, or nil if not found.
func (db *DB) GetHatSwapByID(ctx context.Context, id string) (*HatSwap, error) {
	row, err := db.queries.GetHatSwapByID(ctx, id)
	if err != nil {
		return nil, err
	}

	return hatSwapFromRow(row), nil
}

// GetPendingSwapsForMember returns all pending swap requests targeting the given member.
func (db *DB) GetPendingSwapsForMember(ctx context.Context, memberID string) ([]HatSwap, error) {
	rows, err := db.queries.GetPendingSwapsForMember(ctx, memberID)
	if err != nil {
		return nil, err
	}

	return hatSwapsFromRows(rows), nil
}

// GetSwapsForMember returns all swap requests (sent or received) for the given member.
func (db *DB) GetSwapsForMember(ctx context.Context, memberID string) ([]HatSwap, error) {
	rows, err := db.queries.GetSwapsForMember(ctx, sqlc.GetSwapsForMemberParams{
		RequesterMemberID: memberID,
		TargetMemberID:    memberID,
	})
	if err != nil {
		return nil, err
	}

	return hatSwapsFromRows(rows), nil
}

// GetOpenSwapForAssignment returns a pending swap involving the given assignment, if any.
func (db *DB) GetOpenSwapForAssignment(ctx context.Context, assignmentID string) (*HatSwap, error) {
	row, err := db.queries.GetOpenSwapForAssignment(ctx, sqlc.GetOpenSwapForAssignmentParams{
		RequesterAssignmentID: assignmentID,
		TargetAssignmentID:    assignmentID,
	})
	if err != nil {
		return nil, err
	}

	return hatSwapFromRow(row), nil
}

// UpdateHatSwapStatus updates the status of a swap request.
func (db *DB) UpdateHatSwapStatus(ctx context.Context, id, status string) error {
	return db.queries.UpdateHatSwapStatus(ctx, sqlc.UpdateHatSwapStatusParams{
		Status: status,
		ID:     id,
	})
}

// DeleteHatSwap hard-deletes a swap record (admin only).
func (db *DB) DeleteHatSwap(ctx context.Context, id string) error {
	return db.queries.DeleteHatSwap(ctx, id)
}

// CountPendingSwapsForMember returns the count of pending incoming swaps for a member.
func (db *DB) CountPendingSwapsForMember(ctx context.Context, memberID string) (int64, error) {
	return db.queries.CountPendingSwapsForMember(ctx, memberID)
}

// ExecuteSwap accepts a swap: it swaps the member_id on both rota assignments and
// marks the swap record as accepted. All changes run in a single transaction.
func (db *DB) ExecuteSwap(ctx context.Context, swapID string) error {
	swap, err := db.GetHatSwapByID(ctx, swapID)
	if err != nil {
		return err
	}

	if swap.Status != "pending" {
		return ErrSwapNotPending
	}

	reqAssignment, tgtAssignment, err := db.loadSwapAssignmentsForExecution(ctx, swap)
	if err != nil {
		return err
	}

	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	qtx := db.queries.WithTx(tx)

	// Swap member_ids between the two assignments.
	err = qtx.UpdateAssignmentMember(ctx, sqlc.UpdateAssignmentMemberParams{
		MemberID: tgtAssignment.MemberID,
		ID:       reqAssignment.ID,
	})
	if err != nil {
		return err
	}

	err = qtx.UpdateAssignmentMember(ctx, sqlc.UpdateAssignmentMemberParams{
		MemberID: reqAssignment.MemberID,
		ID:       tgtAssignment.ID,
	})
	if err != nil {
		return err
	}

	// Mark swap as accepted.
	err = qtx.UpdateHatSwapStatus(ctx, sqlc.UpdateHatSwapStatusParams{
		Status: "accepted",
		ID:     swapID,
	})
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (db *DB) loadSwapAssignmentsForExecution(ctx context.Context, swap *HatSwap) (sqlc.GetAssignmentByIDRow, sqlc.GetAssignmentByIDRow, error) {
	reqAssignment, err := db.queries.GetAssignmentByID(ctx, swap.RequesterAssignmentID)
	if err != nil {
		return sqlc.GetAssignmentByIDRow{}, sqlc.GetAssignmentByIDRow{}, err
	}

	tgtAssignment, err := db.queries.GetAssignmentByID(ctx, swap.TargetAssignmentID)
	if err != nil {
		return sqlc.GetAssignmentByIDRow{}, sqlc.GetAssignmentByIDRow{}, err
	}

	today := time.Now().Truncate(dbHoursInDay * time.Hour)
	if reqAssignment.Date.Before(today) || tgtAssignment.Date.Before(today) {
		return sqlc.GetAssignmentByIDRow{}, sqlc.GetAssignmentByIDRow{}, ErrSwapDatePassed
	}

	return reqAssignment, tgtAssignment, nil
}

// GetEnrichedSwaps enriches a slice of HatSwap with member names and assignment dates.
func (db *DB) GetEnrichedSwaps(ctx context.Context, swaps []HatSwap) ([]HatSwap, error) {
	for i := range swaps {
		s := &swaps[i]

		reqMember, err := db.queries.GetMemberByID(ctx, s.RequesterMemberID)
		if err == nil {
			s.RequesterName = reqMember.Name
		}

		tgtMember, err := db.queries.GetMemberByID(ctx, s.TargetMemberID)
		if err == nil {
			s.TargetName = tgtMember.Name
		}

		reqAssignment, err := db.queries.GetAssignmentByID(ctx, s.RequesterAssignmentID)
		if err == nil {
			s.RequesterDate = reqAssignment.Date.Format("2006-01-02")
		}

		tgtAssignment, err := db.queries.GetAssignmentByID(ctx, s.TargetAssignmentID)
		if err == nil {
			s.TargetDate = tgtAssignment.Date.Format("2006-01-02")
		}
	}

	return swaps, nil
}

// hatSwapFromRow converts a sqlc.HatSwap to a database.HatSwap.
func hatSwapFromRow(row sqlc.HatSwap) *HatSwap {
	s := &HatSwap{
		ID:                    row.ID,
		RequesterAssignmentID: row.RequesterAssignmentID,
		TargetAssignmentID:    row.TargetAssignmentID,
		RequesterMemberID:     row.RequesterMemberID,
		TargetMemberID:        row.TargetMemberID,
		Status:                row.Status,
	}

	if row.CreatedAt.Valid {
		s.CreatedAt = row.CreatedAt.Time
	}

	if row.UpdatedAt.Valid {
		s.UpdatedAt = row.UpdatedAt.Time
	}

	return s
}

// hatSwapsFromRows converts a slice of sqlc.HatSwap to []database.HatSwap.
func hatSwapsFromRows(rows []sqlc.HatSwap) []HatSwap {
	result := make([]HatSwap, len(rows))
	for i := range rows {
		result[i] = *hatSwapFromRow(rows[i])
	}

	return result
}
