package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/inful/madhatter/internal/database"
)

// backupCommand is the CLI dispatcher for `support-rota backup <path>`.
// It extracts the Kong-parsed flags and forwards to runBackup, the
// testable inner function. Pattern matches the wfhGoPurge /
// swapReconcileCommand split.
func backupCommand(ctx context.Context, db *database.DB) {
	written, err := runBackup(ctx, db, CLI.Backup.Output, CLI.Backup.Force)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup: %v\n", err)
		os.Exit(1)
	}
	log.Printf("backup: wrote %d bytes to %s\n", written, CLI.Backup.Output)
}

// runBackup writes a consistent SQLite snapshot of db to outputPath
// and tightens the file mode to 0600 (backup files contain the full
// database — including OAuth tokens and session data — so they must
// be readable only by the running user).
//
// The snapshot is produced via VACUUM INTO, which the DB layer wraps
// to overwrite the destination on every call (it refuses to overwrite
// a non-SQLite file). When force is false the command refuses to
// clobber an existing file at outputPath — protecting an operator who
// fat-fingers the path and points at a previous backup.
//
// Returns the number of bytes written so the dispatcher can echo
// the snapshot size to the operator.
func runBackup(ctx context.Context, db *database.DB, outputPath string, force bool) (int64, error) {
	if outputPath == "" {
		return 0, errors.New("output path is required")
	}

	if !force {
		if _, err := os.Stat(outputPath); err == nil {
			return 0, fmt.Errorf("output path %q already exists; pass --force to overwrite", outputPath)
		}
	}

	if err := db.CreateBackupTo(ctx, outputPath); err != nil {
		return 0, fmt.Errorf("create backup: %w", err)
	}

	if err := os.Chmod(outputPath, filePermissionICS); err != nil {
		return 0, fmt.Errorf("tighten permissions on %s: %w", outputPath, err)
	}

	info, err := os.Stat(outputPath)
	if err != nil {
		return 0, fmt.Errorf("stat written backup: %w", err)
	}

	return info.Size(), nil
}
