package database

import (
	"context"
	"database/sql"
	"time"
)

// CoverAlgorithmState records the last applied version of the
// cover-assignment algorithm and the most recent reassignment outcome.
// The ReassignCovers runner in the rota package uses this to decide
// whether the on-disk rota needs to be re-run under the current binary.
type CoverAlgorithmState struct {
	AppliedVersion int
	LastRunAt      *time.Time
	LastRunChanged int
}

// GetCoverAlgorithmState returns the single row in cover_algorithm_state.
// The zero value is returned (no error) if the table is empty for any
// reason; callers should treat AppliedVersion = 0 as "never applied".
func (db *DB) GetCoverAlgorithmState(ctx context.Context) (CoverAlgorithmState, error) {
	var (
		applied int
		runAt   sql.NullTime
		changed int
	)
	row := db.db.QueryRowContext(ctx, `SELECT applied_version, last_run_at, last_run_changed FROM cover_algorithm_state WHERE id = 1`)
	if err := row.Scan(&applied, &runAt, &changed); err != nil {
		if err == sql.ErrNoRows {
			return CoverAlgorithmState{}, nil
		}
		return CoverAlgorithmState{}, err
	}

	state := CoverAlgorithmState{
		AppliedVersion: applied,
		LastRunChanged: changed,
	}
	if runAt.Valid {
		t := runAt.Time
		state.LastRunAt = &t
	}
	return state, nil
}

// SetCoverAlgorithmApplied records that the algorithm version has been
// applied, along with the timestamp and the number of covers that changed
// during the run. Diagnostics for operators to confirm what a rerun did.
//
// Implemented as an upsert so the single-row invariant is enforced by
// the query itself, not by relying on the migration to have inserted
// the row. Without the ON CONFLICT clause, a row that was somehow
// missing would silently affect 0 rows and leave the runner in a
// permanent re-run loop.
func (db *DB) SetCoverAlgorithmApplied(ctx context.Context, version, changed int) error {
	_, err := db.db.ExecContext(ctx, `
		INSERT INTO cover_algorithm_state (id, applied_version, last_run_at, last_run_changed)
		VALUES (1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			applied_version = excluded.applied_version,
			last_run_at = excluded.last_run_at,
			last_run_changed = excluded.last_run_changed
	`, version, time.Now().UTC(), changed)
	return err
}
