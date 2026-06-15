package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCoverAlgorithmState_DefaultIsZero verifies that on a fresh database
// the state is the zero value (AppliedVersion = 0). Callers must be able
// to distinguish "never applied" from "applied version 1" without a
// separate flag.
func TestCoverAlgorithmState_DefaultIsZero(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	state, err := db.GetCoverAlgorithmState(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, state.AppliedVersion, "fresh database should report applied_version=0")
	require.Nil(t, state.LastRunAt, "fresh database should have no last_run_at")
	require.Equal(t, 0, state.LastRunChanged)
}

// TestCoverAlgorithmState_SetAndGet covers the round-trip: write a state
// row, read it back, verify the read matches the write.
func TestCoverAlgorithmState_SetAndGet(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	require.NoError(t, db.SetCoverAlgorithmApplied(ctx, 3, 7))

	state, err := db.GetCoverAlgorithmState(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, state.AppliedVersion)
	require.Equal(t, 7, state.LastRunChanged)
	require.NotNil(t, state.LastRunAt, "last_run_at must be stamped on every apply")
}

// TestCoverAlgorithmState_SetOverwrites covers the update path: applying
// the algorithm a second time should overwrite the prior row, not
// insert a second one (the table is a single-row table by CHECK
// constraint on id = 1).
func TestCoverAlgorithmState_SetOverwrites(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	require.NoError(t, db.SetCoverAlgorithmApplied(ctx, 1, 0))
	require.NoError(t, db.SetCoverAlgorithmApplied(ctx, 2, 5))

	state, err := db.GetCoverAlgorithmState(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, state.AppliedVersion, "second apply should overwrite the version")
	require.Equal(t, 5, state.LastRunChanged, "second apply should overwrite the change count")

	// Confirm only one row exists.
	row := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cover_algorithm_state`)
	var count int
	require.NoError(t, row.Scan(&count))
	require.Equal(t, 1, count, "table must remain a single-row table across updates")
}
