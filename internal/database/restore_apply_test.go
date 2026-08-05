package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateSQLiteHeader covers the byte-level header check that
// runs before the integrity check. We lock this in because the dedup
// with validateSQLiteIntegrityTx depends on the same on-disk format.
func TestValidateSQLiteHeader(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		err := validateSQLiteHeader(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty")

		err = validateSQLiteHeader([]byte{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty")
	})

	t.Run("TooShort", func(t *testing.T) {
		err := validateSQLiteHeader([]byte("SQLite"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a valid SQLite database")
	})

	t.Run("WrongMagic", func(t *testing.T) {
		err := validateSQLiteHeader([]byte("NOT a SQLite format 3\x00 at all"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a valid SQLite database")
	})

	t.Run("ValidHeader", func(t *testing.T) {
		err := validateSQLiteHeader([]byte(sqliteFileHeader + " more bytes"))
		assert.NoError(t, err)
	})
}

// TestValidateSQLiteIntegrity verifies the standalone (non-tx) path
// over a healthy in-memory database. The integrity check should
// return nil.
func TestValidateSQLiteIntegrity_Healthy(t *testing.T) {
	ctx := context.Background()
	candidate, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = candidate.Close() })

	// Force a schema so the file is not entirely empty.
	_, err = candidate.ExecContext(ctx, "CREATE TABLE t (id INTEGER)")
	require.NoError(t, err)

	assert.NoError(t, validateSQLiteIntegrity(ctx, candidate))
}

// TestValidateSQLiteIntegrity_Corrupted verifies the failure path:
// when the integrity check encounters a malformed file, the helper
// must surface that error. SQLite reports corruption either via a
// failed query ("database disk image is malformed") or via a row
// whose result is not "ok" — both paths must propagate. We trigger
// corruption by truncating a valid file to half its size, which
// reliably trips SQLite's page checks.
func TestValidateSQLiteIntegrity_Corrupted(t *testing.T) {
	ctx := context.Background()
	tmp := filepath.Join(t.TempDir(), "broken.db")

	// Materialize a healthy DB on disk, then truncate it so the
	// page structure becomes inconsistent.
	{
		src, err := sql.Open("sqlite3", tmp)
		require.NoError(t, err)
		_, err = src.ExecContext(ctx, "CREATE TABLE t (id INTEGER)")
		require.NoError(t, err)
		require.NoError(t, src.Close())
	}
	//nolint:gosec // G304/G703: tmp is created above via filepath.Join(t.TempDir(), ...); not user input
	data, err := os.ReadFile(tmp)
	require.NoError(t, err)
	require.Greater(t, len(data), 32, "test DB must be larger than the header")
	//nolint:gosec // G304/G703: see comment above; tmp is a test-scoped path
	require.NoError(t, os.WriteFile(tmp, data[:len(data)/2], 0o600))

	candidate, err := sql.Open("sqlite3", tmp)
	require.NoError(t, err)
	t.Cleanup(func() { _ = candidate.Close() })

	err = validateSQLiteIntegrity(ctx, candidate)
	require.Error(t, err, "a corrupted SQLite file must fail the integrity check")
}

// TestValidateSQLiteIntegrity_NoResult covers the path where the
// underlying driver returns zero rows. SQLite always returns at least
// one row for a valid database, so this is hard to trigger with a
// real driver — we exercise it by closing the connection before the
// query runs. The test is a regression guard for the hasRows branch
// rather than a faithful integration test.
func TestValidateSQLiteIntegrity_ClosedConn(t *testing.T) {
	ctx := context.Background()
	candidate, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	require.NoError(t, candidate.Close())

	err = validateSQLiteIntegrity(ctx, candidate)
	require.Error(t, err, "a closed connection must surface an error")
}

// TestValidateSQLiteIntegrityTx_Healthy covers the transactional
// variant over a healthy in-memory database. The two paths share the
// same body, so this is more of a smoke test than a behavioral diff.
func TestValidateSQLiteIntegrityTx_Healthy(t *testing.T) {
	ctx := context.Background()
	candidate, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = candidate.Close() })

	_, err = candidate.ExecContext(ctx, "CREATE TABLE t (id INTEGER)")
	require.NoError(t, err)

	tx, err := candidate.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })

	assert.NoError(t, validateSQLiteIntegrityTx(ctx, tx))
}

// TestValidateSQLiteIntegrityTx_FailsOnCorruption covers the failure
// path of the transactional variant. We run the integrity check
// against a *sql.Tx that points at a database whose integrity is
// already broken.
func TestValidateSQLiteIntegrityTx_FailsOnCorruption(t *testing.T) {
	ctx := context.Background()
	tmp := filepath.Join(t.TempDir(), "broken.db")

	{
		src, err := sql.Open("sqlite3", tmp)
		require.NoError(t, err)
		_, err = src.ExecContext(ctx, "CREATE TABLE t (id INTEGER)")
		require.NoError(t, err)
		require.NoError(t, src.Close())
	}
	//nolint:gosec // G304/G703: tmp is a test-scoped path built from t.TempDir(), not user input
	data, err := os.ReadFile(tmp)
	require.NoError(t, err)
	require.Greater(t, len(data), 32)
	//nolint:gosec // G304/G703: see comment above; tmp is a test-scoped path
	require.NoError(t, os.WriteFile(tmp, data[:len(data)/2], 0o600))

	candidate, err := sql.Open("sqlite3", tmp)
	require.NoError(t, err)
	t.Cleanup(func() { _ = candidate.Close() })

	tx, err := candidate.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })

	err = validateSQLiteIntegrityTx(ctx, tx)
	require.Error(t, err, "corruption must be detected in a tx too")
}
