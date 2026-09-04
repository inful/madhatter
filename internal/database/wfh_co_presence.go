package database

import (
	"context"
	"time"

	"github.com/inful/madhatter/internal/database/sqlc"
)

// coPresenceCohortPad is the index of the third cohort slot
// in the GetLatestCoPresenceWithCohort IN list. Matches the
// coPresenceCohortCap constant in the wfh service.
const coPresenceCohortPad = 2

// RecordWFHCoPresencePair inserts a single co-presence pair for
// (working_date, member_id_a, member_id_b). Idempotent: the
// UNIQUE(working_date, a, b) constraint plus INSERT OR IGNORE
// mean repeated calls are no-ops. Self-pairs (a == b) are a
// silent no-op. The canonical ordering (member_id_a <
// member_id_b) is enforced in InsertWFHCoPresencePair before
// insert so the table's CHECK constraint is never tripped —
// callers don't have to pre-sort.
//
// This is the error-only convenience: tests use it for fixture
// insertion. For the (inserted, error) bool return used by the
// picker and backfill, see InsertWFHCoPresencePair.
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
	_, err := db.InsertWFHCoPresencePair(ctx, workingDate, memberIDA, memberIDB)
	return err
}

// InsertWFHCoPresencePair is the same as RecordWFHCoPresencePair
// but returns whether a row was actually inserted (true) or
// skipped as a duplicate (false). The picker and backfill
// paths use this to count actual writes without re-reading
// the table.
func (db *DB) InsertWFHCoPresencePair(ctx context.Context, workingDate, memberIDA, memberIDB string) (bool, error) {
	if memberIDA == "" || memberIDB == "" || workingDate == "" {
		return false, ErrWFHInvalidDate
	}
	if memberIDA == memberIDB {
		return false, nil
	}
	if memberIDA > memberIDB {
		memberIDA, memberIDB = memberIDB, memberIDA
	}
	id := "co-pres-" + workingDate + "-" + memberIDA + "-" + memberIDB
	result, err := db.queries.RecordWFHCoPresencePair(ctx, sqlc.RecordWFHCoPresencePairParams{
		CoPresenceID: id,
		WorkingDate:  parseCoPresenceDate(workingDate),
		MemberIDA:    memberIDA,
		MemberIDB:    memberIDB,
	})
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
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
//
// The cutoff is wrapped in julianday() at the SQL layer
// (julianday(working_date) < julianday(?)) so the comparison
// is numeric regardless of the stored working_date format
// (RFC3339 20-byte via time.Time, 10-byte YYYY-MM-DD via
// raw SQL, etc). See v0.31.6 — the rewrite is preemptive
// defensive coding because the function was previously
// dead code, but the comment said "called by the scheduler"
// so we don't want it to misbehave when a caller wires up.
func (db *DB) PruneWFHCoPresenceOlderThan(ctx context.Context, cutoff time.Time) error {
	_, err := db.queries.PruneCoPresenceOlderThan(ctx, cutoff)
	return err
}

// GetLatestCoPresenceWithCohort returns the most recent
// working_date on which candidateID was on-site with any
// member of cohortIDs, within the horizon window
// [start, end). Returns the zero time.Time when the
// candidate has no co-presence history with the cohort
// in the window — the picker treats this as "horizon_days
// + 1" via the history-clamp in section 4 of
// plans/assigned-wfh-plan.md.
//
// Cohort cap: the query accepts up to 3 cohort IDs via
// explicit placeholders. A future enhancement could use
// sqlc's slice support or a helper table to handle larger
// cohorts. With the current cap, teams of 3-12 members
// (the typical Support Rota deployment) always fit; the
// picker falls back to the sentinel when a cohort exceeds
// 3, which is the same fallback the empty-cohort branch
// uses.
//
// Implementation: two sqlc :many queries (candidate-as-A
// and candidate-as-B) with ORDER BY working_date DESC
// LIMIT 1, then take the max of the two results. We split
// because sqlc v1.28 returns interface{} for SELECT
// MAX(...) over a complex WHERE clause — the simpler
// LIMIT 1 shape infers time.Time cleanly. The cost is one
// extra round-trip; both queries hit the
// idx_wfh_co_presence_member_{a,b} index, so each is O(1)
// over the horizon.
//
// v0.31.6: switched the WHERE and ORDER BY to
// julianday(working_date) so the comparison is numeric
// regardless of the stored working_date format. The
// previous form relied on the lex compare
// 'YYYY-MM-DDTHH:MM:SSZ' < 'YYYY-MM-DDTHH:MM:SSZ' being
// consistent — which it was, except when the upper bound
// is exactly midnight UTC (e.g. the picker runs at
// 00:00:00Z). At that moment the upper bound becomes
// 'YYYY-MM-DDTHH:MM:SSZ' which is lex-equal to the stored
// midnight value, and the half-open interval excludes
// today's row. The rewrite makes the result correct at
// any clock time.
//
// Used by the seat-cap picker tiebreaker (step 10 of
// plans/assigned-wfh-plan.md).
func (db *DB) GetLatestCoPresenceWithCohort(ctx context.Context, candidateID string, cohortIDs []string, start, end time.Time) (time.Time, error) {
	// Pad cohort to exactly coPresenceCohortCap IDs with empty
	// sentinels that never match any real member. Empty string
	// in the IN list means "no row can have member_id_b = ''"
	// so the OR branch never fires for the padded slots. This
	// keeps the SQL placeholder count fixed at the cap.
	a, b, c := "", "", ""
	if len(cohortIDs) > 0 {
		a = cohortIDs[0]
	}
	if len(cohortIDs) > 1 {
		b = cohortIDs[1]
	}
	if len(cohortIDs) > coPresenceCohortPad {
		c = cohortIDs[coPresenceCohortPad]
	}

	rowsA, err := db.queries.GetLatestCoPresenceWithCohortA(ctx, sqlc.GetLatestCoPresenceWithCohortAParams{
		Julianday:   start,
		Julianday_2: end,
		MemberIDA:   candidateID,
		MemberIDB:   a,
		MemberIDB_2: b,
		MemberIDB_3: c,
	})
	if err != nil {
		return time.Time{}, err
	}
	rowsB, err := db.queries.GetLatestCoPresenceWithCohortB(ctx, sqlc.GetLatestCoPresenceWithCohortBParams{
		Julianday:   start,
		Julianday_2: end,
		MemberIDB:   candidateID,
		MemberIDA:   a,
		MemberIDA_2: b,
		MemberIDA_3: c,
	})
	if err != nil {
		return time.Time{}, err
	}

	var latest time.Time
	if len(rowsA) > 0 && rowsA[0].After(latest) {
		latest = rowsA[0]
	}
	if len(rowsB) > 0 && rowsB[0].After(latest) {
		latest = rowsB[0]
	}
	return latest, nil
}
