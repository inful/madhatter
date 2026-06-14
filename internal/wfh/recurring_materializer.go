package wfh

import (
	"context"
	"errors"
	"time"

	"github.com/inful/madhatter/internal/database"
)

// EnsureRecurringMaterialized fills any missing approved+is_recurring=1 rows
// for all active members between start and end, based on each member's
// contractual recurring weekdays. Idempotent: re-running the call inserts
// no duplicates because the (member_id, date) UNIQUE constraint and the
// pre-insert existence check both block them. The materializer skips past
// dates silently — there's nothing to insert there.
//
// Note: the request horizon (MaxRequestDate) applies to *ad-hoc* WFH
// submissions only. The materializer may pre-create approved recurring
// rows for any date range the caller asks for; today's call sites all
// pass ranges inside the horizon, but a future caller that walks further
// out would bypass the ad-hoc cap.
//
// Returns the number of rows actually inserted. Errors from individual
// members are logged by the caller; the function aggregates by continuing
// past non-fatal failures and returns the running count alongside the
// last error encountered.
func (s *Service) EnsureRecurringMaterialized(ctx context.Context, start, end time.Time) (int, error) {
	if end.Before(start) {
		return 0, nil
	}

	today := todayUTC()
	effectiveStart := start
	if effectiveStart.Before(today) {
		effectiveStart = today
	}
	if effectiveStart.After(end) {
		return 0, nil
	}

	members, err := s.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return 0, err
	}

	inserted := 0
	var lastErr error
	now := time.Now().UTC()
	for i := range members {
		m := members[i]
		if !m.HasAnyRecurringWFH() {
			continue
		}
		n, err := s.materializeForMember(ctx, m, effectiveStart, end, now)
		inserted += n
		if err != nil {
			lastErr = err
		}
	}
	return inserted, lastErr
}

// EnsureRecurringMaterializedForMember runs the materializer for a single
// member over the given range. Used by handleWFHList so each page load
// fills in the current period for the viewing user without iterating the
// whole team.
func (s *Service) EnsureRecurringMaterializedForMember(ctx context.Context, memberID string, start, end time.Time) (int, error) {
	if end.Before(start) {
		return 0, nil
	}
	today := todayUTC()
	effectiveStart := start
	if effectiveStart.Before(today) {
		effectiveStart = today
	}
	if effectiveStart.After(end) {
		return 0, nil
	}

	member, err := s.db.GetMemberByID(ctx, memberID)
	if err != nil {
		return 0, err
	}
	if !member.HasAnyRecurringWFH() {
		return 0, nil
	}
	return s.materializeForMember(ctx, *member, effectiveStart, end, time.Now().UTC())
}

// materializeForMember walks the date range and inserts approved+is_recurring=1
// rows for every weekday the member is contracted to WFH. Skips dates that
// already have any wfh_requests row (approved, pending, withdrawn, …) so
// the user's explicit state — including a withdrawn recurring day — is
// preserved.
func (s *Service) materializeForMember(ctx context.Context, member database.TeamMember, start, end time.Time, now time.Time) (int, error) {
	inserted := 0
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if !member.IsRecurringWFHOn(d) {
			continue
		}
		dateStr := d.Format("2006-01-02")

		// Skip if any row already exists for (member, date). The user's
		// decision to withdraw, cancel, or replace this date is preserved.
		exists, err := s.db.HasWFHRequestOnDate(ctx, member.ID, dateStr)
		if err != nil {
			return inserted, err
		}
		if exists {
			continue
		}

		if err := s.db.CreateApprovedRecurringWFHRequest(ctx, member.ID, dateStr, now); err != nil {
			// UNIQUE collision = race with a concurrent request. Treat as
			// success: the row exists, the materializer's intent is met.
			if errors.Is(err, database.ErrWFHDuplicateRequest) {
				continue
			}
			return inserted, err
		}
		inserted++
	}
	return inserted, nil
}

// todayUTC returns midnight UTC, normalised.
func todayUTC() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}
