package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
//
// Defense in depth: the web/API handlers validate via
// ValidateSwapAssignments that the passed requesterMemberID
// actually matches the requester assignment's current owner.
// The DB layer previously had no such check, so a swap row could
// be inserted (via direct SQL, a migration, a future bug that
// bypasses the handlers) with a stale or wrong requester/target
// member_id. ExecuteSwap later reads the CURRENT member_id from
// rota_assignments and would flip the wrong way, silently
// corrupting the schedule. The production backup shows this exact
// scenario: a Jone↔Alexey swap where the requester_assignment
// was Alexey's row, yet the row's member_id ended up Aashish
// after ExecuteSwap, because the captured member_ids didn't match
// the live owners.
//
// This re-validation at the storage boundary closes that gap. A
// non-API path that bypassed the handler's ValidateSwapAssignments
// call still gets rejected here.
// validateCreateHatSwap enforces the storage-layer invariants for
// a new swap row. The web/API handlers do most of this via
// ValidateSwapAssignments; this is the defense in depth at the
// database boundary. See CreateHatSwap for the rationale.
func (db *DB) validateCreateHatSwap(ctx context.Context, requesterAssignmentID, targetAssignmentID, requesterMemberID, targetMemberID string) error {
	if requesterAssignmentID == "" || targetAssignmentID == "" || requesterMemberID == "" || targetMemberID == "" {
		return errors.New("all swap fields are required")
	}
	if requesterMemberID == targetMemberID {
		return ErrSwapTargetSelf
	}

	reqAssignment, err := db.queries.GetAssignmentByID(ctx, requesterAssignmentID)
	if err != nil {
		return fmt.Errorf("load requester assignment: %w", err)
	}
	if reqAssignment.MemberID != requesterMemberID {
		return ErrSwapNotOwner
	}
	tgtAssignment, err := db.queries.GetAssignmentByID(ctx, targetAssignmentID)
	if err != nil {
		return fmt.Errorf("load target assignment: %w", err)
	}
	if tgtAssignment.MemberID != targetMemberID {
		return errors.New("recorded target member does not own the target assignment")
	}
	return nil
}

// CreateHatSwap creates a new pending HAT day swap request.
func (db *DB) CreateHatSwap(ctx context.Context, requesterAssignmentID, targetAssignmentID, requesterMemberID, targetMemberID string) (string, error) {
	if err := db.validateCreateHatSwap(ctx, requesterAssignmentID, targetAssignmentID, requesterMemberID, targetMemberID); err != nil {
		return "", err
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

	if retErr = db.executeSwapTx(ctx, db.queries.WithTx(tx), reqAssignment, tgtAssignment, swap); retErr != nil {
		return retErr
	}

	retErr = tx.Commit()

	return retErr
}

// executeSwapTx performs the transactional assignment swap and status update.
//
// The flip uses the swap row's CAPTURED requester_member_id and
// target_member_id, NOT the live assignment owners. This is the
// user-facing contract: the dashboard's "Jone ↔ Alexey" arrow
// shows the captured pair, and the flip must land the same way
// regardless of any mid-flight mutations to either assignment's
// owner (e.g. a cover-scheduler overwrite of a leave-related row
// that happened to be one side of the swap). See issue #57.
//
// Pre-flight validation:
//   - loadSwapAssignmentsForExecution already rejected self-swaps
//     (both via the captured pair and via the live pair) and
//     past-dated assignments.
//   - CreateHatSwap already validated that the captured member_ids
//     matched the live assignment owners AT INSERT TIME. If
//     something changed since then, we honor the captured pair
//     anyway — that is what the user agreed to.
func (db *DB) executeSwapTx(ctx context.Context, qtx *sqlc.Queries, reqAssignment, tgtAssignment sqlc.GetAssignmentByIDRow, swap *HatSwap) error {
	// reqAssignment's new owner = the swap's recorded target
	// member. Jone agrees to take Alexey's day, so the requester
	// assignment ends up with the target_member_id (Alexey).
	if err := qtx.UpdateAssignmentMember(ctx, sqlc.UpdateAssignmentMemberParams{
		MemberID: swap.TargetMemberID,
		ID:       reqAssignment.ID,
	}); err != nil {
		return err
	}

	// tgtAssignment's new owner = the swap's recorded requester
	// member. Alexey agrees to take Jone's day, so the target
	// assignment ends up with the requester_member_id (Jone).
	if err := qtx.UpdateAssignmentMember(ctx, sqlc.UpdateAssignmentMemberParams{
		MemberID: swap.RequesterMemberID,
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
		ID:     swap.ID,
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

	// Re-validate the self-swap invariant at execute time. The
	// DB-level CHECK constraint added in migration 000028 prevents
	// future INSERTs of self-swap rows, but a row inserted before
	// that migration (or via a path that bypassed the constraint)
	// could still exist. Two checks cover the same surface from
	// different angles:
	//
	//   1. CAPTURED pair — matches the swap row's
	//      requester_member_id / target_member_id. This catches
	//      legacy bad data that survived past migration 000028.
	//
	//   2. LIVE pair — matches the CURRENT owners of the two
	//      assignments. This catches the case where the captured
	//      pair is fine but mid-flight mutations have left both
	//      rows owned by the same member (e.g. someone reassigned
	//      both via the cover scheduler). Without this guard,
	//      executeSwapTx would write the same member_id to both
	//      sides — a no-op swap with is_swapped=1 set, which
	//      leaves the dashboard showing a false swap badge.
	if swap.RequesterMemberID == swap.TargetMemberID {
		return sqlc.GetAssignmentByIDRow{}, sqlc.GetAssignmentByIDRow{}, ErrSwapTargetSelf
	}
	if reqAssignment.MemberID == tgtAssignment.MemberID {
		return sqlc.GetAssignmentByIDRow{}, sqlc.GetAssignmentByIDRow{}, ErrSwapTargetSelf
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

// HatSwapReconcileResult describes what (if anything) changed on
// the two assignment rows when reconciling one accepted swap. The
// CLI prints this per swap so the operator sees exactly which
// rows drifted from the swap record. Drift is reported as a
// per-side slice of {Field, OldValue, NewValue} so the CLI can
// render a tidy table; the empty slice means "no change".
type HatSwapReconcileResult struct {
	SwapID            string
	RequesterDrift    []FieldChange
	TargetDrift       []FieldChange
	AssignmentMissing bool // true if either side's assignment no longer exists
}

// FieldChange is one column-level drift report: the row's field
// changed from OldValue to NewValue. Empty OldValue means "row
// didn't have this field set before" (insertion); empty NewValue
// means "row no longer has this field" (deletion — not currently
// produced by reconciliation but reserved for symmetry).
type FieldChange struct {
	Field    string
	OldValue string
	NewValue string
}

// GetAcceptedSwaps returns every swap whose status is 'accepted',
// ordered by created_at ascending. Used by the reconcile CLI to
// walk the historical swap rows whose member_id flips may have
// been lost to pre-v0.32.5 / pre-v0.32.3 bugs (see issue #54 and
// the production-anomaly investigation that produced the fix).
func (db *DB) GetAcceptedSwaps(ctx context.Context) ([]HatSwap, error) {
	rows, err := db.queries.GetAcceptedSwaps(ctx)
	if err != nil {
		return nil, err
	}
	return hatSwapsFromRows(rows), nil
}

// ReconcileAcceptedSwap applies the swap's CAPTURED pair to the
// underlying rota_assignments rows. This is the historical-repair
// counterpart to executeSwapTx: where executeSwapTx flips a fresh
// swap on acceptance, this one repairs an existing accepted swap
// whose member_ids drifted from the swap record (typically because
// a cover-scheduler re-run stomped the cover row before v0.32.3,
// or because the swap was executed under the pre-v0.32.5 live-
// value semantics and a subsequent operation reverted one or
// both sides).
//
// The captured pair is the user-facing contract: requester_member_id
// ends up where target_assignment currently is, and vice versa.
// The function also ensures both rows have is_swapped=1 set so the
// dashboard's swap-rendering cue (the green swap icon) appears.
//
// Dry-run by default; commit on Apply=true. The CLI uses both
// modes to mirror the WFHPastPeriods dry-run/apply split.
//
// Idempotent: re-running on an already-reconciled swap produces
// an empty drift list (every column already matches the captured
// pair, is_swapped already set).
func (db *DB) ReconcileAcceptedSwap(ctx context.Context, swapID string, apply bool) (HatSwapReconcileResult, error) {
	result := HatSwapReconcileResult{SwapID: swapID}

	swap, err := db.loadAcceptedSwapForReconcile(ctx, swapID)
	if err != nil {
		return result, err
	}

	reqAssignment, err := db.loadAssignmentForReconcile(ctx, swap.RequesterAssignmentID, &result)
	if err != nil {
		return result, err
	}
	tgtAssignment, err := db.loadAssignmentForReconcile(ctx, swap.TargetAssignmentID, &result)
	if err != nil {
		return result, err
	}

	computeReconcileDrift(&result, swap, reqAssignment, tgtAssignment)

	if !apply || (len(result.RequesterDrift) == 0 && len(result.TargetDrift) == 0) {
		return result, nil
	}

	if err := db.applyReconciliation(ctx, swap); err != nil {
		return result, err
	}
	return result, nil
}

// loadAcceptedSwapForReconcile returns the swap if it exists and
// is in a state reconciliation can apply to. Extracted from
// ReconcileAcceptedSwap so the orchestrator stays under the
// cyclomatic-complexity cap.
func (db *DB) loadAcceptedSwapForReconcile(ctx context.Context, swapID string) (*HatSwap, error) {
	swap, err := db.GetHatSwapByID(ctx, swapID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSwapNotFound
		}
		return nil, err
	}
	if swap == nil {
		return nil, ErrSwapNotFound
	}
	if swap.Status != SwapStatusAccepted {
		return nil, ErrSwapNotPending
	}
	return swap, nil
}

// loadAssignmentForReconcile returns the assignment by ID, or
// sets AssignmentMissing=true on the result and returns an error
// when the row no longer exists. Reconcile needs both rows
// present; missing-data is a config-level issue the operator
// should know about rather than a silent skip.
func (db *DB) loadAssignmentForReconcile(ctx context.Context, assignmentID string, result *HatSwapReconcileResult) (sqlc.GetAssignmentByIDRow, error) {
	a, err := db.queries.GetAssignmentByID(ctx, assignmentID)
	if err != nil {
		result.AssignmentMissing = true
		return sqlc.GetAssignmentByIDRow{}, fmt.Errorf("load assignment %s: %w", assignmentID, err)
	}
	return a, nil
}

// computeReconcileDrift populates result.RequesterDrift /
// result.TargetDrift with the per-column drift list comparing the
// live rota_assignments state to the swap's captured pair. The
// captured pair is the user-facing contract: requester_member_id
// ends up where target_assignment is, and vice versa.
//
// Pure function — no DB calls, no error returns — so it stays
// under the cyclomatic cap.
func computeReconcileDrift(result *HatSwapReconcileResult, swap *HatSwap, reqAssignment, tgtAssignment sqlc.GetAssignmentByIDRow) {
	expectedReqMember := swap.TargetMemberID
	expectedTgtMember := swap.RequesterMemberID

	if reqAssignment.MemberID != expectedReqMember {
		result.RequesterDrift = append(result.RequesterDrift, FieldChange{
			Field:    "member_id",
			OldValue: reqAssignment.MemberID,
			NewValue: expectedReqMember,
		})
	}
	if !isSwappedFlagSet(reqAssignment.IsSwapped) {
		result.RequesterDrift = append(result.RequesterDrift, FieldChange{
			Field:    "is_swapped",
			OldValue: boolToZeroOne(reqAssignment.IsSwapped),
			NewValue: "1",
		})
	}

	if tgtAssignment.MemberID != expectedTgtMember {
		result.TargetDrift = append(result.TargetDrift, FieldChange{
			Field:    "member_id",
			OldValue: tgtAssignment.MemberID,
			NewValue: expectedTgtMember,
		})
	}
	if !isSwappedFlagSet(tgtAssignment.IsSwapped) {
		result.TargetDrift = append(result.TargetDrift, FieldChange{
			Field:    "is_swapped",
			OldValue: boolToZeroOne(tgtAssignment.IsSwapped),
			NewValue: "1",
		})
	}
}

// applyReconciliation commits the two side updates in a single
// transaction. Atomicity matters: partial reconciliation would
// leave the swap in a worse state than the dry-run revealed (one
// side flipped, the other still drifted).
func (db *DB) applyReconciliation(ctx context.Context, swap *HatSwap) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	qtx := db.queries.WithTx(tx)
	if err := qtx.UpdateAssignmentMember(ctx, sqlc.UpdateAssignmentMemberParams{
		MemberID: swap.TargetMemberID,
		ID:       swap.RequesterAssignmentID,
	}); err != nil {
		return fmt.Errorf("update requester assignment member: %w", err)
	}
	if err := qtx.MarkAssignmentSwapped(ctx, swap.RequesterAssignmentID); err != nil {
		return fmt.Errorf("mark requester assignment swapped: %w", err)
	}
	if err := qtx.UpdateAssignmentMember(ctx, sqlc.UpdateAssignmentMemberParams{
		MemberID: swap.RequesterMemberID,
		ID:       swap.TargetAssignmentID,
	}); err != nil {
		return fmt.Errorf("update target assignment member: %w", err)
	}
	if err := qtx.MarkAssignmentSwapped(ctx, swap.TargetAssignmentID); err != nil {
		return fmt.Errorf("mark target assignment swapped: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reconciliation: %w", err)
	}
	committed = true
	return nil
}

// ReconcileAllAcceptedSwaps walks every status='accepted' swap and
// applies ReconcileAcceptedSwap with apply=true (or false for
// dry-run). Returns the per-swap results in input order. Used by
// the bulk form of the CLI command (`swap reconcile --all`). The
// CLI prints one line per swap so the operator can scan for which
// positions actually drifted.
func (db *DB) ReconcileAllAcceptedSwaps(ctx context.Context, apply bool) ([]HatSwapReconcileResult, error) {
	swaps, err := db.GetAcceptedSwaps(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]HatSwapReconcileResult, 0, len(swaps))
	for i := range swaps {
		r, err := db.ReconcileAcceptedSwap(ctx, swaps[i].ID, apply)
		if err != nil {
			// Don't abort the whole bulk run on one bad row —
			// the operator wants to see all drift in one pass
			// and decide which to fix. Surface the error on
			// the per-swap result so it lands in the CLI
			// output alongside the drift reports.
			results = append(results, HatSwapReconcileResult{
				SwapID: swaps[i].ID,
			})
			// Use the last appended slot for the error message
			// by storing it as an extra drift on the requester
			// side; the CLI's renderer treats non-empty
			// RequesterDrift[].Field == "error" as a fatal.
			results[len(results)-1].RequesterDrift = []FieldChange{{
				Field:    "error",
				OldValue: "",
				NewValue: err.Error(),
			}}
			continue
		}
		results = append(results, r)
	}
	return results, nil
}

// isSwappedFlagSet reports whether the sqlc is_swapped column
// (returned as int64 by sqlc) is set to 1.
func isSwappedFlagSet(v int64) bool {
	return v == 1
}

// boolToZeroOne renders a swap flag as "0" or "1" so the CLI's
// dry-run output reads naturally without quoting booleans.
func boolToZeroOne(v int64) string {
	if v == 1 {
		return "1"
	}
	return "0"
}
