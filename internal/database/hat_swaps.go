package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
	"github.com/ncruces/go-sqlite3"
)

// Swap status constants shared across the application.
const (
	SwapStatusPending   = "pending"
	SwapStatusAccepted  = "accepted"
	SwapStatusRejected  = "rejected"
	SwapStatusCancelled = "cancelled"
)

var (
	ErrSwapNotPending              = errors.New("swap is no longer pending")
	ErrSwapNotFound                = errors.New("swap not found")
	ErrSwapDatePassed              = errors.New("one of the HAT days has already passed and cannot be swapped")
	ErrSwapAssignmentBusy          = errors.New("one of the assignments already has an open swap request")
	ErrSwapSameAssignment          = errors.New("cannot swap an assignment with itself")
	ErrSwapNotOwner                = errors.New("you can only swap your own assignments")
	ErrSwapTargetSelf              = errors.New("swap target must belong to another member")
	ErrRequesterAssignmentNotFound = errors.New("requester assignment not found")
	ErrTargetAssignmentNotFound    = errors.New("target assignment not found")
	ErrSwapRequesterDatePassed     = errors.New("your HAT day has already passed and cannot be swapped")
	ErrSwapTargetDatePassed        = errors.New("the target HAT day has already passed and cannot be swapped")
	ErrSwapRequesterDateInvalid    = errors.New("requester assignment date is invalid")
	ErrSwapTargetDateInvalid       = errors.New("target assignment date is invalid")
)

// ValidateSwapAssignments checks that both assignments exist, the requester owns their
// assignment, the target belongs to a different member, and both dates are in the future.
// It returns the two resolved assignments when all checks pass.
func (db *DB) ValidateSwapAssignments(ctx context.Context, requesterAssignmentID, targetAssignmentID, requesterMemberID string) (*RotaAssignment, *RotaAssignment, error) {
	if requesterAssignmentID == targetAssignmentID {
		return nil, nil, ErrSwapSameAssignment
	}

	reqAssignment, err := db.GetAssignmentByID(ctx, requesterAssignmentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrRequesterAssignmentNotFound
		}

		return nil, nil, err
	}

	tgtAssignment, err := db.GetAssignmentByID(ctx, targetAssignmentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrTargetAssignmentNotFound
		}

		return nil, nil, err
	}

	if reqAssignment.MemberID != requesterMemberID {
		return nil, nil, ErrSwapNotOwner
	}

	if tgtAssignment.MemberID == reqAssignment.MemberID {
		return nil, nil, ErrSwapTargetSelf
	}

	if err = validateSwapAssignmentDates(reqAssignment.Date, tgtAssignment.Date); err != nil {
		return nil, nil, err
	}

	return reqAssignment, tgtAssignment, nil
}

// validateSwapAssignmentDates checks that both assignment dates are in the future.
func validateSwapAssignmentDates(reqDate, tgtDate string) error {
	nowUTC := time.Now().UTC()
	today := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)

	req, err := time.Parse("2006-01-02", reqDate)
	if err != nil {
		return ErrSwapRequesterDateInvalid
	}

	if req.Before(today) {
		return ErrSwapRequesterDatePassed
	}

	tgt, err := time.Parse("2006-01-02", tgtDate)
	if err != nil {
		return ErrSwapTargetDateInvalid
	}

	if tgt.Before(today) {
		return ErrSwapTargetDatePassed
	}

	return nil
}

// CheckNoOpenSwaps returns ErrSwapAssignmentBusy if any of the given assignments
// already has a pending swap request.
func (db *DB) CheckNoOpenSwaps(ctx context.Context, assignmentIDs ...string) error {
	for _, aid := range assignmentIDs {
		existing, err := db.GetOpenSwapForAssignment(ctx, aid)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}

			continue
		}

		if existing != nil {
			return ErrSwapAssignmentBusy
		}
	}

	return nil
}

// CreateHatSwap creates a new pending HAT day swap request.
func (db *DB) CreateHatSwap(ctx context.Context, requesterAssignmentID, targetAssignmentID, requesterMemberID, targetMemberID string) (string, error) {
	if requesterAssignmentID == "" || targetAssignmentID == "" || requesterMemberID == "" || targetMemberID == "" {
		return "", errors.New("all swap fields are required")
	}

	if requesterMemberID == targetMemberID {
		return "", ErrSwapTargetSelf
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
		var sqliteErr *sqlite3.Error
		if errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode() == sqlite3.CONSTRAINT_TRIGGER {
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

// GetAcceptedSwapForAssignment returns the most recent accepted swap involving the given assignment.
func (db *DB) GetAcceptedSwapForAssignment(ctx context.Context, assignmentID string) (*HatSwap, error) {
	const query = `
SELECT id, requester_assignment_id, target_assignment_id,
       requester_member_id, target_member_id, status, created_at, updated_at
FROM hat_swaps
WHERE (requester_assignment_id = ? OR target_assignment_id = ?)
  AND status = 'accepted'
ORDER BY updated_at DESC, created_at DESC
LIMIT 1
`

	var swap HatSwap
	var createdAt sql.NullTime
	var updatedAt sql.NullTime

	err := db.db.QueryRowContext(ctx, query, assignmentID, assignmentID).Scan(
		&swap.ID,
		&swap.RequesterAssignmentID,
		&swap.TargetAssignmentID,
		&swap.RequesterMemberID,
		&swap.TargetMemberID,
		&swap.Status,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSwapNotFound
		}
		return nil, err
	}

	if createdAt.Valid {
		swap.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		swap.UpdatedAt = updatedAt.Time
	}

	return &swap, nil
}

// UpdateHatSwapStatus updates the status of a pending swap request.
// Returns ErrSwapNotPending if the swap was not in pending state.
func (db *DB) UpdateHatSwapStatus(ctx context.Context, id, status string) error {
	result, err := db.queries.UpdateHatSwapStatus(ctx, sqlc.UpdateHatSwapStatusParams{
		Status: status,
		ID:     id,
	})
	if err != nil {
		return err
	}

	n, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if n == 0 {
		return ErrSwapNotPending
	}

	return nil
}

// DeleteHatSwap hard-deletes a swap record (admin only).
func (db *DB) DeleteHatSwap(ctx context.Context, id string) error {
	return db.queries.DeleteHatSwap(ctx, id)
}

// CountPendingSwapsForMember returns the count of pending incoming swaps for a member.
func (db *DB) CountPendingSwapsForMember(ctx context.Context, memberID string) (int64, error) {
	return db.queries.CountPendingSwapsForMember(ctx, memberID)
}

// CleanupExpiredPendingSwaps cancels pending swaps when either assignment date is already in the past.
func (db *DB) CleanupExpiredPendingSwaps(ctx context.Context) (int64, error) {
	today := time.Now().UTC().Format("2006-01-02")

	const query = `
UPDATE hat_swaps
SET status = ?, updated_at = CURRENT_TIMESTAMP
WHERE status = 'pending'
  AND EXISTS (
      SELECT 1
      FROM rota_assignments req
      JOIN rota_assignments tgt ON tgt.id = hat_swaps.target_assignment_id
      WHERE req.id = hat_swaps.requester_assignment_id
        AND (req.date < ? OR tgt.date < ?)
  )
`

	result, err := db.db.ExecContext(ctx, query, SwapStatusCancelled, today, today)
	if err != nil {
		return 0, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}

	return affected, nil
}

// ExecuteSwap accepts a swap: it swaps the member_id on both rota assignments and
// marks the swap record as accepted. All changes run in a single transaction.
func (db *DB) ExecuteSwap(ctx context.Context, swapID string) (retErr error) {
	swap, err := db.GetHatSwapByID(ctx, swapID)
	if err != nil {
		return err
	}

	if swap.Status != SwapStatusPending {
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
		if retErr != nil {
			_ = tx.Rollback()
		}
	}()

	if retErr = db.executeSwapTx(ctx, db.queries.WithTx(tx), reqAssignment, tgtAssignment, swapID); retErr != nil {
		return retErr
	}

	retErr = tx.Commit()

	return retErr
}

// executeSwapTx performs the transactional assignment swap and status update.
func (db *DB) executeSwapTx(ctx context.Context, qtx *sqlc.Queries, reqAssignment, tgtAssignment sqlc.GetAssignmentByIDRow, swapID string) error {
	if err := qtx.UpdateAssignmentMember(ctx, sqlc.UpdateAssignmentMemberParams{
		MemberID: tgtAssignment.MemberID,
		ID:       reqAssignment.ID,
	}); err != nil {
		return err
	}

	if err := qtx.UpdateAssignmentMember(ctx, sqlc.UpdateAssignmentMemberParams{
		MemberID: reqAssignment.MemberID,
		ID:       tgtAssignment.ID,
	}); err != nil {
		return err
	}

	if err := qtx.MarkAssignmentSwapped(ctx, reqAssignment.ID); err != nil {
		return err
	}

	if err := qtx.MarkAssignmentSwapped(ctx, tgtAssignment.ID); err != nil {
		return err
	}

	result, err := qtx.UpdateHatSwapStatus(ctx, sqlc.UpdateHatSwapStatusParams{
		Status: SwapStatusAccepted,
		ID:     swapID,
	})
	if err != nil {
		return err
	}

	n, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if n == 0 {
		return ErrSwapNotPending
	}

	return nil
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

	nowUTC := time.Now().UTC()
	today := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	if reqAssignment.Date.Before(today) || tgtAssignment.Date.Before(today) {
		return sqlc.GetAssignmentByIDRow{}, sqlc.GetAssignmentByIDRow{}, ErrSwapDatePassed
	}

	return reqAssignment, tgtAssignment, nil
}

// GetEnrichedSwaps enriches a slice of HatSwap with member names and assignment dates.
// It batch-loads members and assignments to avoid N+1 queries.
func (db *DB) GetEnrichedSwaps(ctx context.Context, swaps []HatSwap) ([]HatSwap, error) {
	if len(swaps) == 0 {
		return swaps, nil
	}

	memberIDSet, assignmentIDSet := collectSwapIDs(swaps)

	// Batch-load member names.
	memberNames := make(map[string]string, len(memberIDSet))
	for id := range memberIDSet {
		if member, err := db.queries.GetMemberByID(ctx, id); err == nil {
			memberNames[id] = member.Name
		}
	}

	// Batch-load assignment dates.
	assignmentDates := make(map[string]string, len(assignmentIDSet))
	for id := range assignmentIDSet {
		if assignment, err := db.queries.GetAssignmentByID(ctx, id); err == nil {
			assignmentDates[id] = assignment.Date.Format("2006-01-02")
		}
	}

	applySwapEnrichment(swaps, memberNames, assignmentDates)

	return swaps, nil
}

// collectSwapIDs collects unique member and assignment IDs from a swap slice.
func collectSwapIDs(swaps []HatSwap) (memberIDs, assignmentIDs map[string]struct{}) {
	memberIDs = make(map[string]struct{})
	assignmentIDs = make(map[string]struct{})

	for i := range swaps {
		memberIDs[swaps[i].RequesterMemberID] = struct{}{}
		memberIDs[swaps[i].TargetMemberID] = struct{}{}
		assignmentIDs[swaps[i].RequesterAssignmentID] = struct{}{}
		assignmentIDs[swaps[i].TargetAssignmentID] = struct{}{}
	}

	return memberIDs, assignmentIDs
}

// applySwapEnrichment populates enriched name and date fields on each swap.
func applySwapEnrichment(swaps []HatSwap, memberNames, assignmentDates map[string]string) {
	for i := range swaps {
		s := &swaps[i]

		if name, ok := memberNames[s.RequesterMemberID]; ok {
			s.RequesterName = name
		}

		if name, ok := memberNames[s.TargetMemberID]; ok {
			s.TargetName = name
		}

		if date, ok := assignmentDates[s.RequesterAssignmentID]; ok {
			s.RequesterDate = date
		}

		if date, ok := assignmentDates[s.TargetAssignmentID]; ok {
			s.TargetDate = date
		}
	}
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
