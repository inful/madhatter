package cmd

import (
	"context"
	"database/sql"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupMigrateStatusTestDB creates a fresh sqlite file at the
// returned temp path and applies migrations up to targetVersion.
// When targetVersion is 0 the file is created but no migrations
// are applied, which is the "fresh empty database" fixture.
//
// Mirrors setupSwapTestDB (cmd/swap_reconcile_test.go) so the test
// stays decoupled from the binary's working directory.
func setupMigrateStatusTestDB(t *testing.T, targetVersion uint) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	migrationsDir := filepath.Join(
		filepath.Clean(filepath.Join(filepath.Dir(filename), "..")),
		"migrations",
	)
	t.Setenv("MIGRATIONS_PATH", migrationsDir)

	dbPath := filepath.Join(t.TempDir(), "test.db")

	sqlDB, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	// Match the production open-time PRAGMA so the test exercises
	// the same setup the binary does. foreign_keys matters as soon
	// as the schema_migrations table has foreign-keyed neighbors;
	// applying it on the empty path is a no-op but consistent.
	_, err = sqlDB.ExecContext(context.Background(), "PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	if targetVersion == 0 {
		return dbPath
	}

	require.NoError(t, advanceSchemaTo(t, sqlDB, targetVersion))
	return dbPath
}

// advanceSchemaTo brings the on-disk schema at sqlDB up to
// targetVersion. The helper applies every migration on disk and
// then calls MigrateToVersion so a target equal to the latest
// version is a no-op rather than an off-by-one crash. Used by
// the migrate-status tests to fixture a partially-applied schema.
func advanceSchemaTo(t *testing.T, sqlDB *sql.DB, targetVersion uint) error {
	t.Helper()
	if err := database.RunMigrations(sqlDB); err != nil {
		return err
	}
	return database.MigrateToVersion(sqlDB, targetVersion)
}

// TestRunMigrateStatus_FreshDatabase pins the "I just created the
// DB, no migrations have run" report shape. GetMigrationStatus's
// documented behavior is to create the schema_migrations table
// implicitly on first inspection — version reports as 0 with
// applied=true. The migrate-status output must reflect that,
// not invent a fake higher version.
func TestRunMigrateStatus_FreshDatabase(t *testing.T) {
	dbPath := setupMigrateStatusTestDB(t, 0)

	report, err := runMigrateStatus(context.Background(), dbPath)
	require.NoError(t, err)

	assert.Contains(t, report, "Applied version: 0")
	assert.Contains(t, report, "Dirty:           false")
	assert.Contains(t, report, "Latest on disk:  29", "project currently has 29 migrations on disk")
	assert.Contains(t, report, "Pending:         29")
}

// TestRunMigrateStatus_AtVersion pins the partial-application
// case. With the DB at version 25 and the project shipping 29
// migrations, the operator should see "Pending: 4" so they know
// to restart the service for auto-apply.
func TestRunMigrateStatus_AtVersion(t *testing.T) {
	dbPath := setupMigrateStatusTestDB(t, 25)

	report, err := runMigrateStatus(context.Background(), dbPath)
	require.NoError(t, err)

	assert.Contains(t, report, "Applied version: 25")
	assert.Contains(t, report, "Dirty:           false")
	assert.Contains(t, report, "Latest on disk:  29")
	assert.Contains(t, report, "Pending:         4", "5 migrations sit ahead of 25: 26, 27, 28, 29 — but only when 25 is not on disk")
}

// TestRunMigrateStatus_Dirty pins the "operator must intervene
// before further migrations can run" case. The output must
// include a warning with the recovery SQL so the operator does
// not have to dig through docs to find the right command.
func TestRunMigrateStatus_Dirty(t *testing.T) {
	dbPath := setupMigrateStatusTestDB(t, 25)

	// Flip the dirty flag to simulate a half-applied migration.
	sqlDB, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	_, err = sqlDB.ExecContext(context.Background(),
		"UPDATE schema_migrations SET dirty = 1")
	require.NoError(t, err)

	report, err := runMigrateStatus(context.Background(), dbPath)
	require.NoError(t, err)

	assert.Contains(t, report, "Applied version: 25")
	assert.Contains(t, report, "Dirty:           true")
	assert.Contains(t, report, "WARNING", "dirty output must include the recovery warning")
	assert.Contains(t, report, "UPDATE schema_migrations SET dirty = 0",
		"operator-facing warning must include the recovery SQL")
}

// TestRunMigrateStatus_BadPath pins the error path when the user
// points at a path that the driver refuses to open. A bare
// "not exists" path is lazy-opened by sqlite3, so we deliberately
// point at a directory: sql.Open surfaces the "not a database"
// error during GetMigrationStatus. The CLI maps this to exit 1.
func TestRunMigrateStatus_BadPath(t *testing.T) {
	// A directory is a valid path that sqlite3 cannot use as a
	// database file. t.TempDir() never errors and cleans up after
	// the test, satisfying the t.Chdir helper on the lint side.
	bogusPath := t.TempDir()

	_, err := runMigrateStatus(context.Background(), bogusPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), bogusPath,
		"error should include the path for operator triage")
}
