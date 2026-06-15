package rota

import (
	"context"
	"fmt"

	"github.com/inful/madhatter/internal/database"
)

// CoverAlgorithmVersion is the version of the cover-assignment algorithm
// shipped with this binary. It is bumped whenever the algorithm changes
// in a way that could produce a different cover assignment for an
// already-processed leave (e.g. the R2 rotation anchor fix). A periodic
// reassignment runner reads the on-disk applied version from
// cover_algorithm_state and re-runs the algorithm when the binary's
// version is ahead, so the rota converges to the new algorithm's output
// without a manual data migration.
//
// v1: initial implementation (rotation anchored on the most recent cover
//
//	across the whole rota — could be stuck on a future-date cover).
//
// v2: rotation anchored strictly on the most recent cover BEFORE the
//
//	leave's start date, so a future cover never freezes the rotation.
const CoverAlgorithmVersion = 2

// ReassignResult summarizes a reassignment pass.
type ReassignResult struct {
	// LeavesProcessed is the number of leave rows that were walked.
	LeavesProcessed int
	// CoversChanged is the number of leaves whose cover set actually
	// moved during the run. For a multi-day leave that had one cover
	// member swapped, this increments by 1 (one leave changed), not
	// by the number of individual cover rows that flipped.
	CoversChanged int
	// WasStale is true when the runner detected that the on-disk
	// applied version was behind the binary's CoverAlgorithmVersion
	// and therefore re-ran the algorithm. False on a no-op call.
	WasStale bool
	// NewVersion is the version that is now considered "applied" to
	// the rota after the run.
	NewVersion int
}

// ReassignCovers re-runs the cover-assignment algorithm against every
// leave in the database. It is safe to invoke at any time:
//
//   - For active leaves (pending/assigned), HandleLeaveChange is called,
//     which reconciles stale covers and re-creates covers under the
//     current algorithm. Because of the idempotency contract on
//     createCoverAssignment and getNextCoverIndex, the result is the
//     same as the prior state unless the algorithm has actually changed.
//   - For completed/inactive leaves, HandleLeaveChange is a no-op (the
//     reconcile step is already a no-op for them, and AssignCoversForLeave
//     short-circuits on inactive status).
func (sm *ScheduleMaintenance) ReassignCovers(ctx context.Context) (ReassignResult, error) {
	leaves, err := sm.db.GetLeaveRecords(ctx)
	if err != nil {
		return ReassignResult{}, fmt.Errorf("reassign-covers: list leaves: %w", err)
	}

	result := ReassignResult{NewVersion: CoverAlgorithmVersion}
	for i := range leaves {
		l := &leaves[i]
		before := snapshotLeaveCovers(ctx, sm.db, l)
		if err := sm.HandleLeaveChange(ctx, l.ID); err != nil {
			return result, fmt.Errorf("reassign-covers: handle leave %s: %w", l.ID, err)
		}
		after := snapshotLeaveCovers(ctx, sm.db, l)
		result.LeavesProcessed++
		if !sameCoverSet(before, after) {
			result.CoversChanged++
		}
	}
	return result, nil
}

// ReassignCoversIfStale runs ReassignCovers only when the on-disk
// applied_version is behind CoverAlgorithmVersion. On a fresh database
// (applied_version = 0) it will run; on a database that's already at the
// current version it is a no-op. This is the method a periodic runner
// (e.g. a startup hook) should call.
//
// After a successful run, the applied_version is updated to the binary's
// CoverAlgorithmVersion along with the change count and timestamp, so
// operators can confirm what the rerun actually did via the
// cover_algorithm_state table.
func (sm *ScheduleMaintenance) ReassignCoversIfStale(ctx context.Context) (ReassignResult, error) {
	state, err := sm.db.GetCoverAlgorithmState(ctx)
	if err != nil {
		return ReassignResult{}, fmt.Errorf("reassign-covers: read state: %w", err)
	}

	if state.AppliedVersion >= CoverAlgorithmVersion {
		// Up to date. Surface the no-op with WasStale=false so callers can
		// log it cleanly without re-running.
		return ReassignResult{
			LeavesProcessed: 0,
			CoversChanged:   0,
			WasStale:        false,
			NewVersion:      state.AppliedVersion,
		}, nil
	}

	result, err := sm.ReassignCovers(ctx)
	if err != nil {
		return result, err
	}
	result.WasStale = true

	if err := sm.db.SetCoverAlgorithmApplied(ctx, CoverAlgorithmVersion, result.CoversChanged); err != nil {
		return result, fmt.Errorf("reassign-covers: persist state: %w", err)
	}
	return result, nil
}

// snapshotLeaveCovers returns a map of date -> cover member id for every
// day in the leave's date range. The map only contains dates that
// actually have a cover; a date with no cover (e.g. weekend, leave
// member not scheduled) is absent. Used to diff before/after a
// reassignment pass.
//
// One database round-trip per leave via GetAssignmentsByDateRange,
// rather than one per day inside the leave.
func snapshotLeaveCovers(ctx context.Context, db *database.DB, l *database.LeaveRecord) map[string]string {
	out := make(map[string]string)
	if l.EndDate.Before(l.StartDate) {
		return out
	}
	assignments, err := db.GetAssignmentsByDateRange(
		ctx,
		l.StartDate.Format("2006-01-02"),
		l.EndDate.Format("2006-01-02"),
	)
	if err != nil {
		return out
	}
	// Group by date, picking the cover row only.
	for i := range assignments {
		if !assignments[i].IsCover {
			continue
		}
		out[assignments[i].Date] = assignments[i].MemberID
	}
	return out
}

// sameCoverSet reports whether two cover snapshots are identical.
func sameCoverSet(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
