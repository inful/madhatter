package database

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
)

// CreateWFHAssignmentSwap inserts a new pending swap. Returns
// the new swap's ID. Caller is responsible for the 409-conflict
// check (no two pending swaps on the same assigned row) — see
// GetPendingSwapForRequesterRow.
//
// Phase 3 of plans/assigned-wfh-plan.md. Migration 000025.
func (db *DB) CreateWFHAssignmentSwap(ctx context.Context, requesterWfhRequestID, targetMemberID, swapDate string) (string, error) {
	id := "swap-" + uuid.New().String()
	parsedDate, err := time.Parse("2006-01-02", swapDate)
	if err != nil {
		return "", ErrWFHInvalidDate
	}
	_, err = db.queries.CreateSwap(ctx, sqlc.CreateSwapParams{
		ID:                    id,
		RequesterWfhRequestID: requesterWfhRequestID,
		TargetMemberID:        targetMemberID,
		SwapDate:              parsedDate,
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// GetWFHAssignmentSwapByID reads a single swap by ID.
func (db *DB) GetWFHAssignmentSwapByID(ctx context.Context, id string) (*WFHAssignmentSwap, error) {
	row, err := db.queries.GetSwapByID(ctx, id)
	if err != nil {
		if errorsIsNoRows(err) {
			return nil, ErrWFHNotFound
		}
		return nil, err
	}
	return swapFromSQLC(row), nil
}

// GetPendingWFHSwapsForTarget returns all pending swaps where
// the given member is the target. Used by the swap inbox page.
// Ordered by created_at DESC (newest first).
func (db *DB) GetPendingWFHSwapsForTarget(ctx context.Context, targetMemberID string) ([]WFHAssignmentSwap, error) {
	rows, err := db.queries.GetPendingSwapsForTarget(ctx, targetMemberID)
	if err != nil {
		return nil, err
	}
	out := make([]WFHAssignmentSwap, len(rows))
	for i := range rows {
		out[i] = *swapFromSQLC(rows[i])
	}
	return out, nil
}

// GetPendingWFHSwapsForRequesterMember returns all pending
// swaps that any of the given member's WFH rows have
// originated. Used by the WFH list page to render "your swap
// is awaiting N's decision" and to expose the cancel button.
func (db *DB) GetPendingWFHSwapsForRequesterMember(ctx context.Context, memberID string) ([]WFHAssignmentSwap, error) {
	rows, err := db.queries.GetPendingSwapsForRequester(ctx, memberID)
	if err != nil {
		return nil, err
	}
	out := make([]WFHAssignmentSwap, len(rows))
	for i := range rows {
		out[i] = *swapFromSQLC(rows[i])
	}
	return out, nil
}

// GetPendingWFHSwapForRequesterRow returns the pending swap
// for the given assigned wfh_request row, or nil if none
// exists. The 409-conflict guard in handleWFHSwapCreate
// relies on this returning a non-nil value to refuse the
// second concurrent swap submission.
func (db *DB) GetPendingWFHSwapForRequesterRow(ctx context.Context, requesterWfhRequestID string) (*WFHAssignmentSwap, error) {
	row, err := db.queries.GetPendingSwapForRequesterRow(ctx, requesterWfhRequestID)
	if err != nil {
		if errorsIsNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return swapFromSQLC(row), nil
}

// UpdateWFHAssignmentSwapStatus transitions a swap to the
// given status. Sets resolved_at to the supplied timestamp
// (the auto-cancel pass uses now; manual accept/reject uses
// now; the requester cancel uses now too).
func (db *DB) UpdateWFHAssignmentSwapStatus(ctx context.Context, id string, status WFHSwapStatus, resolvedAt time.Time) error {
	_, err := db.queries.UpdateSwapStatus(ctx, sqlc.UpdateSwapStatusParams{
		Status:     string(status),
		ResolvedAt: sql.NullTime{Time: resolvedAt, Valid: true},
		ID:         id,
	})
	return err
}

// CancelExpiredWFHSwaps flips every pending swap whose
// swap_date is strictly before the cutoff to status='cancelled'.
// Step 15 of plans/assigned-wfh-plan.md: SettlePendingRequests
// calls this with cutoff=now-after-ApplyPass to surface
// stale swaps to the operator (and to free the assigned row's
// 409-conflict lock for any future re-swap attempt).
func (db *DB) CancelExpiredWFHSwaps(ctx context.Context, cutoff time.Time) error {
	_, err := db.queries.CancelExpiredSwaps(ctx, cutoff)
	return err
}

// swapFromSQLC converts the sqlc-generated row to the domain
// type, normalizing sql.NullTime to *time.Time for nullable
// timestamps (UpdatedAt, ResolvedAt).
func swapFromSQLC(row sqlc.WfhAssignmentSwap) *WFHAssignmentSwap {
	s := &WFHAssignmentSwap{
		ID:                    row.ID,
		RequesterWFHRequestID: row.RequesterWfhRequestID,
		TargetMemberID:        row.TargetMemberID,
		Status:                row.Status,
	}
	if !row.SwapDate.IsZero() {
		s.SwapDate = row.SwapDate.Format("2006-01-02")
	}
	if row.CreatedAt.Valid {
		s.CreatedAt = row.CreatedAt.Time
	}
	if row.UpdatedAt.Valid {
		s.UpdatedAt = row.UpdatedAt.Time
	}
	if row.ResolvedAt.Valid {
		t := row.ResolvedAt.Time
		s.ResolvedAt = &t
	}
	return s
}

// errorsIsNoRows is a small wrapper to keep the swap read
// adapters readable. Same semantics as errors.Is(err,
// sql.ErrNoRows); the inline form hurts scanning.
func errorsIsNoRows(err error) bool {
	return err != nil && (err == sql.ErrNoRows || (err.Error() == "sql: no rows in result set"))
}
