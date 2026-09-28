package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/inful/madhatter/internal/database"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

// migrateStatusCommand prints the migration status of the production
// database. It deliberately opens the database directly via database/sql
// rather than calling database.New, so a dirty or pending schema does
// not block the inspection: New would auto-advance via RunMigrations
// and refuse to even open a dirty DB. Status inspection must read the
// schema as-is and let the operator decide what to do next.
//
// Routing to this command happens in cmd/root.go:Execute before the
// database.New call so the dispatcher logic stays a single map.
func migrateStatusCommand(ctx context.Context) {
	report, err := runMigrateStatus(ctx, supportRotaDBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate status: %v\n", err)
		os.Exit(1)
	}
	log.Print(report)
}

// runMigrateStatus builds the human-readable migration status
// report for the database at dbPath. Returns the report as a
// single multi-line string so the CLI caller can print it
// directly and so tests can assert on its contents without
// capturing stdout.
//
// On disk, the on-disk migration list comes from
// database.ListMigrationVersions — the same path-resolution logic
// the migration runner uses. When that directory is missing, the
// report shows "Latest on disk: 0" rather than failing; the
// applied-version line is still meaningful on its own.
func runMigrateStatus(ctx context.Context, dbPath string) (string, error) {
	sqlDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return "", fmt.Errorf("open database %s: %w", dbPath, err)
	}
	defer func() { _ = sqlDB.Close() }()

	// foreign_keys is the connection-local PRAGMA used to surface
	// schema_migrations — it doesn't affect the status read but
	// matches the production open-time setup so the report is
	// consistent with whatever a subsequent RunMigrations call would
	// see. WAL/journal are file-level and inherited automatically.
	if _, err = sqlDB.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return "", fmt.Errorf("enable foreign_keys: %w", err)
	}

	status, err := database.GetMigrationStatus(sqlDB)
	if err != nil {
		return "", fmt.Errorf("read migration status: %w", err)
	}

	onDisk, err := database.ListMigrationVersions()
	if err != nil {
		return "", fmt.Errorf("list migrations on disk: %w", err)
	}

	var latestOnDisk uint
	if len(onDisk) > 0 {
		latestOnDisk = onDisk[len(onDisk)-1]
	}
	pending := 0
	for _, v := range onDisk {
		if v > status.Version {
			pending++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Database: %s\n", dbPath)
	fmt.Fprintf(&b, "  Applied version: %d\n", status.Version)
	fmt.Fprintf(&b, "  Dirty:           %t\n", status.Dirty)
	fmt.Fprintf(&b, "  Latest on disk:  %d\n", latestOnDisk)
	fmt.Fprintf(&b, "  Pending:         %d\n", pending)
	if status.Dirty {
		fmt.Fprint(&b, "\n")
		fmt.Fprint(&b, "WARNING: the database is in a dirty state. golang-migrate\n")
		fmt.Fprint(&b, "refuses to advance past a dirty schema; inspect the failing\n")
		fmt.Fprint(&b, "migration manually, then clear the flag with:\n")
		fmt.Fprint(&b, "  UPDATE schema_migrations SET dirty = 0;\n")
	}
	return b.String(), nil
}
