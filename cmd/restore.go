package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/inful/madhatter/internal/database"
)

// maxRestoreInputBytes caps the restore-candidate read at 50 MB. The
// web handler enforces the same ceiling to defend against malicious
// multipart uploads; the CLI is a different trust boundary (operator
// shell access) but a runaway 10 GB read is still a bad day, and
// keeping the cap identical makes the two surfaces consistent for
// users who move between them.
const maxRestoreInputBytes = 50 << 20

// restoreCommand is the CLI dispatcher for `support-rota restore <path>`.
// It extracts the Kong-parsed flags and forwards to runRestore, the
// testable inner function. Without --apply the command validates the
// candidate file and exits without mutating — matches the web UI's
// two-step flow (validate → review → apply) so an operator can dry-run
// before committing.
func restoreCommand(ctx context.Context, db *database.DB) {
	mode, err := runRestore(ctx, db, CLI.Restore.Input, CLI.Restore.Apply)
	if err != nil {
		fmt.Fprintf(os.Stderr, "restore: %v\n", err)
		os.Exit(1)
	}
	if mode == "apply" {
		log.Printf("restore: applied %s\n", CLI.Restore.Input)
		return
	}
	log.Printf("restore: validated %s; re-run with --apply to commit\n", CLI.Restore.Input)
}

// runRestore reads the candidate file, validates it, and (when apply
// is true) commits it to the live database. The returned mode string
// is "validate" or "apply" — the dispatcher echoes it so the operator
// can see which branch ran.
//
// The validation gate is the same database.ValidateRestoreCandidate
// the web handler uses, so the CLI and web surfaces agree on what
// counts as a valid candidate. Apply also re-runs the validation
// inside ApplyRestoreCandidate, so a tampered or misread file fails
// closed: the function returns the validation error without touching
// state.
//
// A non-existent input file surfaces as a clear "not found" error;
// anything else (permission denied, IO error) wraps the underlying
// error so the operator can triage.
func runRestore(ctx context.Context, db *database.DB, inputPath string, apply bool) (string, error) {
	if inputPath == "" {
		return "", errors.New("input path is required")
	}

	file, err := os.Open(inputPath) //nolint:gosec // inputPath comes from the CLI operator.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("input file not found: %s", inputPath)
		}
		return "", fmt.Errorf("open input: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	content, err := io.ReadAll(io.LimitReader(file, int64(maxRestoreInputBytes)+1))
	if err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}
	if int64(len(content)) > maxRestoreInputBytes {
		return "", fmt.Errorf("input file exceeds maximum allowed size (%d bytes)", maxRestoreInputBytes)
	}

	// Reject empty input before dispatching to the DB layer. The
	// validate path catches this via validateSQLiteHeader, but the
	// apply path would silently succeed and wipe every row in the
	// live database — an unacceptable outcome for a CLI command.
	// Catching it here keeps both modes consistent and gives the
	// operator a clear, actionable error.
	if len(content) == 0 {
		return "", errors.New("backup file is empty")
	}

	if !apply {
		if err := db.ValidateRestoreCandidate(ctx, content); err != nil {
			return "validate", fmt.Errorf("validate restore: %w", err)
		}
		return "validate", nil
	}

	if err := db.ApplyRestoreCandidate(ctx, content); err != nil {
		return "apply", fmt.Errorf("apply restore: %w", err)
	}
	return "apply", nil
}
