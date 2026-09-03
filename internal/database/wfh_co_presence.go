package database

import (
	"context"
	"time"

	"github.com/inful/madhatter/internal/database/sqlc"
)

// RecordWFHCoPresencePair inserts a single co-presence pair for
// (working_date, member_id_a, member_id_b). Idempotent: the
// UNIQUE(working_date, a, b) constraint plus INSERT OR IGNORE
// mean repeated calls are no-ops. The canonical ordering
// (member_id_a < member_id_b) is enforced by the table CHECK
// constraint, not by this function — the caller is responsible
// for ordering before calling.
//
// Used by the co-presence writer (step 10 of
// plans/assigned-wfh-plan.md) which calls this in O(n²)
// pairs for the on-site set of a past date, and by the
// eventual-consistent backfill (step 11) which calls it
// across the last N working days.
//
// The co_presence_id is generated here (not by the caller) so
// the writer path stays simple — callers don't need to
// import a UUID library just to record co-presence.
func (db *DB) RecordWFHCoPresencePair(ctx context.Context, workingDate, memberIDA, memberIDB string) error {
	if memberIDA == "" || memberIDB == "" || workingDate == "" {
		return ErrWFHInvalidDate
	}
	if memberIDA == memberIDB {
		// Skip self-pairs. The CHECK constraint catches this too,
		// but skipping here avoids a wasted INSERT.
		return nil
	}
	if memberIDA > memberIDB {
		memberIDA, memberIDB = memberIDB, memberIDA
	}
	id := "co-pres-" + workingDate + "-" + memberIDA + "-" + memberIDB
	_, err := db.queries.RecordWFHCoPresencePair(ctx, sqlc.RecordWFHCoPresencePairParams{
		CoPresenceID: id,
		WorkingDate:  parseCoPresenceDate(workingDate),
		MemberIDA:    memberIDA,
		MemberIDB:    memberIDB,
	})
	return err
}

// parseCoPresenceDate parses YYYY-MM-DD for the co-presence
// writer. Errors fall back to today so a malformed date in the
// writer path doesn't crash the scheduler — the co-presence
// row will land on today instead of the intended date, which
// the daily backfill will then correct.
func parseCoPresenceDate(dateStr string) time.Time {
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return time.Now().UTC()
	}
	return t
}

// PruneWFHCoPresenceOlderThan hard-deletes co-presence rows
// whose working_date is strictly before the cutoff. The
// cutoff is computed in code as (today -
// WFH_COPRESENCE_RETENTION_DAYS); the env loader validates
// the retention >= horizon invariant at boot. Called by the
// scheduler (step 11 of plans/assigned-wfh-plan.md) after
// each settlement tick.
func (db *DB) PruneWFHCoPresenceOlderThan(ctx context.Context, cutoff time.Time) error {
	_, err := db.queries.PruneCoPresenceOlderThan(ctx, cutoff)
	return err
}
