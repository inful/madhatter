package rota

import (
	"context"
	"fmt"
	"sort"
	"time"

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
// designed to be called on every server startup.
//
// Idempotency contract: calling ReassignCovers twice on the same
// set of active leaves produces the same covers and the same
// reassign anchor. This relies on two things working together:
//
//  1. Leaves are processed in chronological order (start_date
//     ASC). Within a leave, days are processed in date order. The
//     rotation state advances forward only on each day; no
//     backward-walk happens, so the forward-walk asymmetry that
//     breaks idempotency on the ad-hoc path never fires here.
//
//  2. The reassign run uses the separate reassign rotation anchor
//     (last_reassign_date, last_reassign_index), not the ad-hoc
//     one (last_date, last_index). A reassign run therefore never
//     disturbs the state that AssignCoversForLeave (web form,
//     API, manual reprocess) reads. Ad-hoc calls between reassigns
//     advance the ad-hoc anchor; the next reassign starts from the
//     unchanged reassign anchor and produces the same result.
//
// The reassign anchor plays no role in computing covers — the
// rotation position is derived from the in-memory state of the
// reassign run itself (mirroring how the ad-hoc path walks
// forward from an empty state on the first call). The anchor is
// written at the end of the run as a "last completed reassign"
// checkpoint; it can be inspected for debugging but is not
// consulted by the algorithm.
//
// For active leaves (pending/assigned), the per-leave pipeline is
// reconcile + assign-with-reassign-anchor; for completed/inactive
// leaves it's a no-op (reconcile deletes the cover row if any, and
// AssignCoversForLeaveWithReassignAnchor short-circuits on inactive
// status).
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
// with a handful of active leaves, this is a few milliseconds.
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

	// Process in chronological order. GetLeaveRecords orders DESC,
	// so sort here in Go rather than changing the SQL contract
	// (the SQL contract is shared with the admin UI which wants
	// newest first).
	sort.Slice(leaves, func(i, j int) bool {
		return leaves[i].StartDate.Before(leaves[j].StartDate)
	})

	// Reset the in-memory reassign state so each run starts from a
	// clean seed (index 0). Without this reset, a subsequent run
	// would inherit the previous run's last-day state, and the
	// multi-day leaves processed earlier in this run would
	// forward-walk from a date that's actually older than them.
	// Deferred so a panic in the per-leave loop still leaves the
	// engine in a clean state for the next call.
	sm.engine.reassignLastDate = time.Time{}
	sm.engine.reassignLastIndex = 0
	defer func() {
		sm.engine.reassignLastDate = time.Time{}
		sm.engine.reassignLastIndex = 0
	}()

	result := ReassignResult{}
	for i := range leaves {
		l := &leaves[i]
		before := snapshotLeaveCovers(ctx, sm.db, l)
		if err := sm.reassignHandleLeaveChange(ctx, l); err != nil {
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

// reassignHandleLeaveChange is the reassign-side mirror of
// HandleLeaveChange. Same reconcile + assign flow, but the assign
// step uses the reassign rotation anchor instead of the ad-hoc one.
// Notifications are intentionally skipped here — the same cover may
// be assigned twice (first by the ad-hoc path, then again by the
// reassign path) and only the first notification should fire.
//
// The anchor that AssignCoversForLeaveWithReassignAnchor reads and
// writes is intentionally a no-op for the idempotency-correct
// implementation: the rotation index is derived from the reassign
// run's in-memory state (which mirrors the ad-hoc path's forward
// walk from an empty state), not from the persisted anchor. The
// anchor is still written at the end as a record of "the last time
// ReassignCovers ran", but it is not consulted by the algorithm
// itself.
func (sm *ScheduleMaintenance) reassignHandleLeaveChange(ctx context.Context, l *database.LeaveRecord) error {
	if err := sm.reconcileCoversForDateRange(ctx, l.StartDate, l.EndDate); err != nil {
		return err
	}
	return sm.engine.AssignCoversForLeaveWithReassignAnchor(ctx, l.ID)
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
