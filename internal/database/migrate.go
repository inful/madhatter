package database

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	embeddedmigrations "github.com/inful/madhatter/migrations"
)

var errMigrationsNotFound = errors.New("migrations directory not found")

// getMigrationsPath returns the absolute path to the migrations directory.
// It tries to find the migrations directory by looking up from the current directory
// until it finds one that contains a migrations folder.
func getMigrationsPath() (string, error) {
	// First, try MIGRATIONS_PATH environment variable
	if path := os.Getenv("MIGRATIONS_PATH"); path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(abs); err != nil { // #nosec G703 -- MIGRATIONS_PATH is an operator-provided local filesystem path.
			return "", fmt.Errorf("MIGRATIONS_PATH does not exist: %w", err)
		}
		return abs, nil
	}

	// Try to find migrations directory relative to current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// Check current directory and up to 3 levels up
	for range 4 {
		testPath := filepath.Join(cwd, "migrations")
		if _, err := os.Stat(testPath); err == nil {
			return testPath, nil
		}
		cwd = filepath.Dir(cwd)
	}

	// As a last resort, try relative to this source file
	_, filename, _, ok := runtime.Caller(0)
	if ok {
		dir := filepath.Dir(filename)
		// Go up to project root (internal/database -> internal -> root)
		projectRoot := filepath.Join(dir, "..", "..")
		migrationsPath := filepath.Join(projectRoot, "migrations")
		if _, err := os.Stat(migrationsPath); err == nil {
			abs, err := filepath.Abs(migrationsPath)
			if err != nil {
				return "", err
			}
			return abs, nil
		}
	}

	return "", errMigrationsNotFound
}

func newMigrator(db *sql.DB) (*migrate.Migrate, error) {
	// Create database driver
	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to create database driver: %w", err)
	}

	// Prefer filesystem migrations if present; fall back to embedded migrations.
	migrationsPath, err := getMigrationsPath()
	if err == nil {
		m, createErr := migrate.NewWithDatabaseInstance(
			"file://"+migrationsPath,
			"sqlite",
			driver,
		)
		if createErr != nil {
			return nil, fmt.Errorf("failed to create migration instance: %w", createErr)
		}
		return m, nil
	}
	if !errors.Is(err, errMigrationsNotFound) {
		return nil, fmt.Errorf("failed to get migrations path: %w", err)
	}

	sourceDriver, err := iofs.New(embeddedmigrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("failed to create embedded migrations source: %w", err)
	}

	m, err := migrate.NewWithInstance(
		"iofs",
		sourceDriver,
		"sqlite",
		driver,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedded migration instance: %w", err)
	}

	return m, nil
}

// RunMigrations executes all pending database migrations.
// It uses migration files from the migrations directory at the project root.
func RunMigrations(db *sql.DB) error {
	m, err := newMigrator(db)
	if err != nil {
		return err
	}

	// Run migrations
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	return nil
}

// GetMigrationVersion returns the current migration version of the database.
func GetMigrationVersion(db *sql.DB) (uint, bool, error) {
	m, err := newMigrator(db)
	if err != nil {
		return 0, false, err
	}

	version, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, fmt.Errorf("failed to get migration version: %w", err)
	}

	return version, dirty, nil
}

// MigrationStatus returns information about the current migration state.
type MigrationStatus struct {
	Version uint
	Dirty   bool
	Applied bool
}

// GetMigrationStatus returns detailed migration status information.
func GetMigrationStatus(db *sql.DB) (*MigrationStatus, error) {
	version, dirty, err := GetMigrationVersion(db)
	if err != nil {
		if errors.Is(err, migrate.ErrNilVersion) {
			return &MigrationStatus{
				Version: 0,
				Dirty:   false,
				Applied: false,
			}, nil
		}
		return nil, err
	}

	return &MigrationStatus{
		Version: version,
		Dirty:   dirty,
		Applied: true,
	}, nil
}

// RollbackMigration rolls back the last applied migration.
// This should be used with caution in production environments.
func RollbackMigration(db *sql.DB) error {
	m, err := newMigrator(db)
	if err != nil {
		return err
	}

	// Roll back one migration
	if err := m.Steps(-1); err != nil {
		return fmt.Errorf("failed to rollback migration: %w", err)
	}

	return nil
}

// MigrateToVersion migrates the database to a specific version.
// Use with caution - this can migrate up or down.
func MigrateToVersion(db *sql.DB, version uint) error {
	m, err := newMigrator(db)
	if err != nil {
		return err
	}

	// Migrate to specific version
	if err := m.Migrate(version); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to migrate to version %d: %w", version, err)
	}

	return nil
}
