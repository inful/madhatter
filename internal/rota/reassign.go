package rota

import (
	"context"
	"fmt"

	"github.com/inful/madhatter/internal/database"
)

// ReassignResult summarizes a reassignment pass.
type ReassignResult struct {
	// LeavesProcessed is the number of leave rows that were walked.
	LeavesProcessed int
	// CoversChanged is the number of leaves whose cover set actually
	// moved during the run. For a multi-day leave that had one cover
	// member swapped, this increments by 1 (one leave changed), not
	// by the number of individual cover rows that flipped.
	CoversChanged int
	// Failures lists the leave IDs whose HandleLeaveChange call
	// returned an error. The runner continues past per-leave failures
	// so a single bad row doesn't strand the rest of the rota in a
	// half-reassigned state; the failures are surfaced here for the
	// caller to log or surface to the operator.
	Failures []string
}

// ReassignCovers re-runs the cover-assignment algorithm against every
// leave in the database. It is safe to invoke at any time and is
// designed to be called on every server startup:
//
//   - For active leaves (pending/assigned), HandleLeaveChange is called,
//     which reconciles stale covers and re-creates covers under the
//     current algorithm. Because of the idempotency contract on
//     createCoverAssignment and getNextCoverIndex, the result is the
//     same as the prior state when the algorithm hasn't changed.
//   - For completed/inactive leaves, HandleLeaveChange is a no-op (the
//     reconcile step is already a no-op for them, and AssignCoversForLeave
//     short-circuits on inactive status).
//
// The runner continues past per-leave errors: a single broken row
// (e.g. an FK violation from a deleted member) does not abort the
// whole pass. The failed leave IDs are collected in Result.Failures
// and the function still returns nil in that case so the startup
// hook can come up cleanly; callers that want fail-fast behavior
// can check len(Result.Failures) > 0.
//
// The cost on a steady-state rota is O(N) DB queries where N is the
// number of leaves, dominated by two GetAssignmentsByDateRange calls
// per leave for the before/after diff. For a typical 14-day window
// with a handful of active leaves, this is a few milliseconds — small
// enough that we don't bother tracking which leaves have been
// "already reassigned" and which haven't. The algorithm's output is
// the source of truth; the reassignment just makes the on-disk rota
// match whatever the current code thinks the covers should be.
//
// This is the self-healing property that lets a future algorithm
// change be deployed without a data migration: the new binary ships
// a different algorithm, the startup hook runs ReassignCovers, and
// any leaves whose cover would differ under the new algorithm are
// rewritten in place.
func (sm *ScheduleMaintenance) ReassignCovers(ctx context.Context) (ReassignResult, error) {
	leaves, err := sm.db.GetLeaveRecords(ctx)
	if err != nil {
		return ReassignResult{}, fmt.Errorf("reassign-covers: list leaves: %w", err)
	}

	result := ReassignResult{}
	for i := range leaves {
		l := &leaves[i]
		before := snapshotLeaveCovers(ctx, sm.db, l)
		if err := sm.HandleLeaveChange(ctx, l.ID); err != nil {
			// Continue past per-leave failures: one broken row must
			// not strand the rest of the rota in a half-reassigned
			// state. The caller (cmd hook or CLI) decides whether to
			// log Failures or surface them.
			result.Failures = append(result.Failures, l.ID)
			continue
		}
		after := snapshotLeaveCovers(ctx, sm.db, l)
		result.LeavesProcessed++
		if !sameCoverSet(before, after) {
			result.CoversChanged++
		}
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
