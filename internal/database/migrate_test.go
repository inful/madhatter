package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestMigrations creates a temporary directory with test migration files.
func setupTestMigrations(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	migrationsDir := filepath.Join(tmpDir, "migrations")
	err := os.Mkdir(migrationsDir, 0o755)
	require.NoError(t, err)

	// Create a simple test migration
	upSQL := `
CREATE TABLE IF NOT EXISTS test_table (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL
);
`
	downSQL := `DROP TABLE IF EXISTS test_table;`

	err = os.WriteFile(filepath.Join(migrationsDir, "000001_test_migration.up.sql"), []byte(upSQL), 0o644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(migrationsDir, "000001_test_migration.down.sql"), []byte(downSQL), 0o644)
	require.NoError(t, err)

	// Create a second migration for testing multiple migrations
	up2SQL := `
CREATE TABLE IF NOT EXISTS test_table2 (
    id TEXT PRIMARY KEY,
    value INTEGER NOT NULL
);
`
	down2SQL := `DROP TABLE IF EXISTS test_table2;`

	err = os.WriteFile(filepath.Join(migrationsDir, "000002_second_migration.up.sql"), []byte(up2SQL), 0o644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(migrationsDir, "000002_second_migration.down.sql"), []byte(down2SQL), 0o644)
	require.NoError(t, err)

	return migrationsDir
}

// TestGetMigrationsPath tests the migration path resolution logic.
func TestGetMigrationsPath(t *testing.T) {
	t.Run("Environment variable", func(t *testing.T) {
		tmpDir := t.TempDir()
		migrationsDir := filepath.Join(tmpDir, "migrations")
		err := os.Mkdir(migrationsDir, 0o755)
		require.NoError(t, err)

		// Set environment variable
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()

		err = os.Setenv("MIGRATIONS_PATH", migrationsDir)
		require.NoError(t, err)

		path, err := getMigrationsPath()
		require.NoError(t, err)
		assert.Contains(t, path, "migrations")
	})

	t.Run("Working directory search", func(t *testing.T) {
		// This test uses the actual project migrations directory
		// Save current directory
		originalWd, err := os.Getwd()
		require.NoError(t, err)
		defer func() { _ = os.Chdir(originalWd) }()

		// Change to project root (go up from internal/database)
		projectRoot := filepath.Join(originalWd, "..", "..")
		err = os.Chdir(projectRoot)
		require.NoError(t, err)

		// Unset environment variable to test directory search
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()
		_ = os.Unsetenv("MIGRATIONS_PATH")

		path, err := getMigrationsPath()
		require.NoError(t, err)
		assert.Contains(t, path, "migrations")

		// Verify the directory actually exists
		_, err = os.Stat(path)
		assert.NoError(t, err, "Migrations directory should exist")
	})

	t.Run("No migrations directory found", func(t *testing.T) {
		// Create a temporary directory without migrations
		tmpDir := t.TempDir()

		// Change to temp directory
		originalWd, err := os.Getwd()
		require.NoError(t, err)
		defer func() { _ = os.Chdir(originalWd) }()

		err = os.Chdir(tmpDir)
		require.NoError(t, err)

		// Unset environment variable
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()
		_ = os.Unsetenv("MIGRATIONS_PATH")

		path, err := getMigrationsPath()
		// Should either find the project migrations or error
		if err != nil {
			assert.Contains(t, err.Error(), "migrations directory not found")
		} else {
			// If it found one, it should be the project migrations
			assert.Contains(t, path, "migrations")
		}
	})
}

// TestRunMigrations tests the migration execution.
func TestRunMigrations(t *testing.T) {
	t.Run("Successful migration", func(t *testing.T) {
		// Create test database
		db, err := sql.Open("sqlite3", ":memory:")
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Enable foreign keys
		_, err = db.Exec("PRAGMA foreign_keys = ON")
		require.NoError(t, err)

		// Setup test migrations
		migrationsDir := setupTestMigrations(t)

		// Set migrations path
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()
		err = os.Setenv("MIGRATIONS_PATH", migrationsDir)
		require.NoError(t, err)

		// Run migrations
		err = RunMigrations(db)
		require.NoError(t, err)

		// Verify table was created
		var tableName string
		err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='test_table'").Scan(&tableName)
		require.NoError(t, err)
		assert.Equal(t, "test_table", tableName)
	})

	t.Run("No pending migrations", func(t *testing.T) {
		// Create test database
		db, err := sql.Open("sqlite3", ":memory:")
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Enable foreign keys
		_, err = db.Exec("PRAGMA foreign_keys = ON")
		require.NoError(t, err)

		// Setup test migrations
		migrationsDir := setupTestMigrations(t)

		// Set migrations path
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()
		err = os.Setenv("MIGRATIONS_PATH", migrationsDir)
		require.NoError(t, err)

		// Run migrations twice
		err = RunMigrations(db)
		require.NoError(t, err)

		// Second run should succeed (no pending migrations)
		err = RunMigrations(db)
		require.NoError(t, err)
	})

	t.Run("Invalid migrations path", func(t *testing.T) {
		// Create test database
		db, err := sql.Open("sqlite3", ":memory:")
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Set invalid migrations path
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()
		err = os.Setenv("MIGRATIONS_PATH", "/nonexistent/path")
		require.NoError(t, err)

		// Run migrations should fail
		err = RunMigrations(db)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create migration instance")
	})
}

// TestGetMigrationVersion tests getting the current migration version.
func TestGetMigrationVersion(t *testing.T) {
	t.Run("Get version after migration", func(t *testing.T) {
		// Create test database
		db, err := sql.Open("sqlite3", ":memory:")
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Enable foreign keys
		_, err = db.Exec("PRAGMA foreign_keys = ON")
		require.NoError(t, err)

		// Setup test migrations
		migrationsDir := setupTestMigrations(t)

		// Set migrations path
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()
		err = os.Setenv("MIGRATIONS_PATH", migrationsDir)
		require.NoError(t, err)

		// Run migrations
		err = RunMigrations(db)
		require.NoError(t, err)

		// Get version
		version, dirty, err := GetMigrationVersion(db)
		require.NoError(t, err)
		assert.Equal(t, uint(2), version, "Should be at version 2 after running both migrations")
		assert.False(t, dirty, "Database should not be in dirty state")
	})

	t.Run("Get version from empty database", func(t *testing.T) {
		// Create test database
		db, err := sql.Open("sqlite3", ":memory:")
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Setup test migrations
		migrationsDir := setupTestMigrations(t)

		// Set migrations path
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()
		err = os.Setenv("MIGRATIONS_PATH", migrationsDir)
		require.NoError(t, err)

		// Get version without running migrations
		version, dirty, err := GetMigrationVersion(db)
		require.NoError(t, err)
		assert.Equal(t, uint(0), version, "Empty database should have version 0")
		assert.False(t, dirty)
	})
}

// TestGetMigrationStatus tests the migration status reporting.
func TestGetMigrationStatus(t *testing.T) {
	t.Run("Status after migration", func(t *testing.T) {
		// Create test database
		db, err := sql.Open("sqlite3", ":memory:")
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Enable foreign keys
		_, err = db.Exec("PRAGMA foreign_keys = ON")
		require.NoError(t, err)

		// Setup test migrations
		migrationsDir := setupTestMigrations(t)

		// Set migrations path
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()
		err = os.Setenv("MIGRATIONS_PATH", migrationsDir)
		require.NoError(t, err)

		// Run migrations
		err = RunMigrations(db)
		require.NoError(t, err)

		// Get status
		status, err := GetMigrationStatus(db)
		require.NoError(t, err)
		require.NotNil(t, status)
		assert.Equal(t, uint(2), status.Version)
		assert.False(t, status.Dirty)
		assert.True(t, status.Applied)
	})

	t.Run("Status before migration", func(t *testing.T) {
		// Create test database
		db, err := sql.Open("sqlite3", ":memory:")
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Setup test migrations
		migrationsDir := setupTestMigrations(t)

		// Set migrations path
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()
		err = os.Setenv("MIGRATIONS_PATH", migrationsDir)
		require.NoError(t, err)

		// Get status without running migrations
		status, err := GetMigrationStatus(db)
		require.NoError(t, err)
		require.NotNil(t, status)
		assert.Equal(t, uint(0), status.Version)
		assert.False(t, status.Dirty)
		// Applied is true because the migration instance creates the schema_migrations table
		assert.True(t, status.Applied)
	})
}

// TestRollbackMigration tests the migration rollback functionality.
func TestRollbackMigration(t *testing.T) {
	t.Run("Successful rollback", func(t *testing.T) {
		// Create test database
		db, err := sql.Open("sqlite3", ":memory:")
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Enable foreign keys
		_, err = db.Exec("PRAGMA foreign_keys = ON")
		require.NoError(t, err)

		// Setup test migrations
		migrationsDir := setupTestMigrations(t)

		// Set migrations path
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()
		err = os.Setenv("MIGRATIONS_PATH", migrationsDir)
		require.NoError(t, err)

		// Run migrations
		err = RunMigrations(db)
		require.NoError(t, err)

		// Verify both tables exist
		var tableCount int
		err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND (name='test_table' OR name='test_table2')").Scan(&tableCount)
		require.NoError(t, err)
		assert.Equal(t, 2, tableCount, "Should have 2 tables after migration")

		// Rollback one migration
		err = RollbackMigration(db)
		require.NoError(t, err)

		// Verify version is now 1
		version, dirty, err := GetMigrationVersion(db)
		require.NoError(t, err)
		assert.Equal(t, uint(1), version, "Should be at version 1 after rollback")
		assert.False(t, dirty)

		// Verify test_table2 no longer exists
		err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='test_table2'").Scan(&tableCount)
		assert.Error(t, err, "test_table2 should not exist after rollback")

		// Verify test_table still exists
		var tableName string
		err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='test_table'").Scan(&tableName)
		require.NoError(t, err)
		assert.Equal(t, "test_table", tableName)
	})

	t.Run("Rollback with no migrations", func(t *testing.T) {
		// Create test database
		db, err := sql.Open("sqlite3", ":memory:")
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Setup test migrations
		migrationsDir := setupTestMigrations(t)

		// Set migrations path
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()
		err = os.Setenv("MIGRATIONS_PATH", migrationsDir)
		require.NoError(t, err)

		// Try to rollback without any migrations applied
		err = RollbackMigration(db)
		require.Error(t, err, "Should fail to rollback when no migrations are applied")
	})
}

// TestMigrateToVersion tests migrating to a specific version.
func TestMigrateToVersion(t *testing.T) {
	t.Run("Migrate to version 1", func(t *testing.T) {
		// Create test database
		db, err := sql.Open("sqlite3", ":memory:")
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Enable foreign keys
		_, err = db.Exec("PRAGMA foreign_keys = ON")
		require.NoError(t, err)

		// Setup test migrations
		migrationsDir := setupTestMigrations(t)

		// Set migrations path
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()
		err = os.Setenv("MIGRATIONS_PATH", migrationsDir)
		require.NoError(t, err)

		// Migrate to version 1
		err = MigrateToVersion(db, 1)
		require.NoError(t, err)

		// Verify version is 1
		version, dirty, err := GetMigrationVersion(db)
		require.NoError(t, err)
		assert.Equal(t, uint(1), version)
		assert.False(t, dirty)

		// Verify only test_table exists
		var tableName string
		err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='test_table'").Scan(&tableName)
		require.NoError(t, err)
		assert.Equal(t, "test_table", tableName)

		// Verify test_table2 doesn't exist
		err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='test_table2'").Scan(&tableName)
		assert.Error(t, err, "test_table2 should not exist at version 1")
	})

	t.Run("Migrate to version 2 then back to version 1", func(t *testing.T) {
		// Create test database
		db, err := sql.Open("sqlite3", ":memory:")
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Enable foreign keys
		_, err = db.Exec("PRAGMA foreign_keys = ON")
		require.NoError(t, err)

		// Setup test migrations
		migrationsDir := setupTestMigrations(t)

		// Set migrations path
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()
		err = os.Setenv("MIGRATIONS_PATH", migrationsDir)
		require.NoError(t, err)

		// Migrate to version 2
		err = MigrateToVersion(db, 2)
		require.NoError(t, err)

		// Verify version is 2
		version, dirty, err := GetMigrationVersion(db)
		require.NoError(t, err)
		assert.Equal(t, uint(2), version)
		assert.False(t, dirty)

		// Migrate back to version 1
		err = MigrateToVersion(db, 1)
		require.NoError(t, err)

		// Verify version is 1
		version, dirty, err = GetMigrationVersion(db)
		require.NoError(t, err)
		assert.Equal(t, uint(1), version)
		assert.False(t, dirty)

		// Verify test_table2 doesn't exist anymore
		var tableName string
		err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='test_table2'").Scan(&tableName)
		assert.Error(t, err, "test_table2 should not exist after downgrade")
	})

	t.Run("Migrate to same version", func(t *testing.T) {
		// Create test database
		db, err := sql.Open("sqlite3", ":memory:")
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Enable foreign keys
		_, err = db.Exec("PRAGMA foreign_keys = ON")
		require.NoError(t, err)

		// Setup test migrations
		migrationsDir := setupTestMigrations(t)

		// Set migrations path
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()
		err = os.Setenv("MIGRATIONS_PATH", migrationsDir)
		require.NoError(t, err)

		// Migrate to version 1
		err = MigrateToVersion(db, 1)
		require.NoError(t, err)

		// Migrate to version 1 again (should succeed with no change)
		err = MigrateToVersion(db, 1)
		require.NoError(t, err)

		// Verify version is still 1
		version, dirty, err := GetMigrationVersion(db)
		require.NoError(t, err)
		assert.Equal(t, uint(1), version)
		assert.False(t, dirty)
	})

	t.Run("Migrate to invalid version", func(t *testing.T) {
		// Create test database
		db, err := sql.Open("sqlite3", ":memory:")
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Setup test migrations
		migrationsDir := setupTestMigrations(t)

		// Set migrations path
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()
		err = os.Setenv("MIGRATIONS_PATH", migrationsDir)
		require.NoError(t, err)

		// Try to migrate to non-existent version 999
		err = MigrateToVersion(db, 999)
		require.Error(t, err, "Should fail to migrate to non-existent version")
		assert.Contains(t, err.Error(), "failed to migrate to version 999")
	})
}

// TestMigrationEdgeCases tests various edge cases in migration handling.
func TestMigrationEdgeCases(t *testing.T) {
	t.Run("Migration with invalid SQL", func(t *testing.T) {
		// Create test database
		db, err := sql.Open("sqlite3", ":memory:")
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Enable foreign keys
		_, err = db.Exec("PRAGMA foreign_keys = ON")
		require.NoError(t, err)

		// Create temporary directory for migrations
		tmpDir := t.TempDir()
		migrationsDir := filepath.Join(tmpDir, "migrations")
		err = os.Mkdir(migrationsDir, 0o755)
		require.NoError(t, err)

		// Create a migration with invalid SQL
		invalidSQL := "THIS IS NOT VALID SQL;"
		err = os.WriteFile(filepath.Join(migrationsDir, "000001_invalid.up.sql"), []byte(invalidSQL), 0o644)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(migrationsDir, "000001_invalid.down.sql"), []byte(""), 0o644)
		require.NoError(t, err)

		// Set migrations path
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()
		err = os.Setenv("MIGRATIONS_PATH", migrationsDir)
		require.NoError(t, err)

		// Run migrations should fail
		err = RunMigrations(db)
		require.Error(t, err, "Should fail with invalid SQL")

		// Database should now be in dirty state
		version, dirty, err := GetMigrationVersion(db)
		if err == nil {
			// If we can get version, it should be dirty
			if version > 0 {
				assert.True(t, dirty, "Database should be in dirty state after failed migration")
			}
		}
	})

	t.Run("Empty migrations directory", func(t *testing.T) {
		// Create test database
		db, err := sql.Open("sqlite3", ":memory:")
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Create empty migrations directory
		tmpDir := t.TempDir()
		migrationsDir := filepath.Join(tmpDir, "migrations")
		err = os.Mkdir(migrationsDir, 0o755)
		require.NoError(t, err)

		// Create a placeholder file so the directory isn't completely empty
		err = os.WriteFile(filepath.Join(migrationsDir, ".gitkeep"), []byte(""), 0o644)
		require.NoError(t, err)

		// Set migrations path
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()
		err = os.Setenv("MIGRATIONS_PATH", migrationsDir)
		require.NoError(t, err)

		// Run migrations should succeed (no migrations to run)
		// Note: golang-migrate requires at least one valid migration file
		// So we'll expect this to fail with "no migration" error
		err = RunMigrations(db)
		// Either succeeds or fails with "no migration" error
		if err != nil {
			assert.Contains(t, err.Error(), "file does not exist", "Should fail gracefully with empty directory")
		}
	})
}

// TestMigrationConcurrency tests that migrations are safe with concurrent access.
func TestMigrationConcurrency(t *testing.T) {
	t.Run("Multiple readers after migration", func(t *testing.T) {
		// Create test database file (not in-memory for concurrent access)
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		// Create and setup database
		db, err := sql.Open("sqlite3", dbPath)
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		// Enable foreign keys
		_, err = db.Exec("PRAGMA foreign_keys = ON")
		require.NoError(t, err)

		// Setup test migrations
		migrationsDir := setupTestMigrations(t)

		// Set migrations path
		originalEnv := os.Getenv("MIGRATIONS_PATH")
		defer func() {
			if originalEnv != "" {
				_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
			} else {
				_ = os.Unsetenv("MIGRATIONS_PATH")
			}
		}()
		err = os.Setenv("MIGRATIONS_PATH", migrationsDir)
		require.NoError(t, err)

		// Run migrations
		err = RunMigrations(db)
		require.NoError(t, err)

		// Now open multiple connections and verify they all see the migrated state
		for i := range 5 {
			conn, err := sql.Open("sqlite3", dbPath)
			require.NoError(t, err)
			defer func() { _ = conn.Close() }()

			version, dirty, err := GetMigrationVersion(conn)
			require.NoError(t, err)
			assert.Equal(t, uint(2), version, "Connection %d should see version 2", i)
			assert.False(t, dirty, "Connection %d should not see dirty state", i)
		}
	})
}

// TestMigrationStatusStruct tests the MigrationStatus struct.
func TestMigrationStatusStruct(t *testing.T) {
	status := MigrationStatus{
		Version: 5,
		Dirty:   false,
		Applied: true,
	}

	assert.Equal(t, uint(5), status.Version)
	assert.False(t, status.Dirty)
	assert.True(t, status.Applied)
}

// TestGetMigrationVersionHandlesNilVersion tests that GetMigrationVersion properly handles ErrNilVersion.
func TestGetMigrationVersionHandlesNilVersion(t *testing.T) {
	// Create test database
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Setup test migrations but don't run them
	migrationsDir := setupTestMigrations(t)

	// Set migrations path
	originalEnv := os.Getenv("MIGRATIONS_PATH")
	defer func() {
		if originalEnv != "" {
			_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
		} else {
			_ = os.Unsetenv("MIGRATIONS_PATH")
		}
	}()
	err = os.Setenv("MIGRATIONS_PATH", migrationsDir)
	require.NoError(t, err)

	// Get version should return 0, false, nil for a fresh database
	version, dirty, err := GetMigrationVersion(db)
	require.NoError(t, err)
	assert.Equal(t, uint(0), version)
	assert.False(t, dirty)
}

// TestGetMigrationStatusHandlesNilVersion tests that GetMigrationStatus properly handles ErrNilVersion.
func TestGetMigrationStatusHandlesNilVersion(t *testing.T) {
	// Create test database
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Setup test migrations but don't run them
	migrationsDir := setupTestMigrations(t)

	// Set migrations path
	originalEnv := os.Getenv("MIGRATIONS_PATH")
	defer func() {
		if originalEnv != "" {
			_ = os.Setenv("MIGRATIONS_PATH", originalEnv)
		} else {
			_ = os.Unsetenv("MIGRATIONS_PATH")
		}
	}()
	err = os.Setenv("MIGRATIONS_PATH", migrationsDir)
	require.NoError(t, err)

	// Get status should return version 0 for a fresh database
	// Note: Applied is true because creating the migration instance creates schema_migrations table
	status, err := GetMigrationStatus(db)
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.Equal(t, uint(0), status.Version)
	assert.False(t, status.Dirty)
	assert.True(t, status.Applied)
}
