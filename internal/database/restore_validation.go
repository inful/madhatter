package database

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
)

const sqliteFileHeader = "SQLite format 3\x00"

var requiredRestoreTables = []string{
	"team_members",
	"leave_records",
	"rota_assignments",
	"calendar_subscriptions",
	"users",
	"sessions",
	"oauth_tokens",
	"api_tokens",
}

// ValidateRestoreCandidate validates an uploaded SQLite backup file without applying it.
func (db *DB) ValidateRestoreCandidate(ctx context.Context, backupBytes []byte) error {
	if err := validateSQLiteHeader(backupBytes); err != nil {
		return err
	}

	tmpPath, err := writeTempRestoreCandidate(backupBytes)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	candidateDB, err := sql.Open("sqlite3", tmpPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = candidateDB.Close()
	}()

	return runRestoreCandidateChecks(ctx, db.db, candidateDB)
}

func validateSQLiteHeader(backupBytes []byte) error {
	if len(backupBytes) == 0 {
		return errors.New("backup file is empty")
	}

	if len(backupBytes) < len(sqliteFileHeader) || !bytes.Equal(backupBytes[:len(sqliteFileHeader)], []byte(sqliteFileHeader)) {
		return errors.New("uploaded file is not a valid SQLite database")
	}

	return nil
}

func writeTempRestoreCandidate(backupBytes []byte) (string, error) {
	tmpFile, err := os.CreateTemp("", "madhatter-restore-*.db")
	if err != nil {
		return "", err
	}
	defer func() {
		_ = tmpFile.Close()
	}()

	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(backupBytes); err != nil {
		return "", err
	}

	if err := tmpFile.Sync(); err != nil {
		return "", err
	}

	return tmpPath, nil
}

func runRestoreCandidateChecks(ctx context.Context, liveDB *sql.DB, candidateDB *sql.DB) error {
	if _, err := candidateDB.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return err
	}

	if err := validateSQLiteIntegrity(ctx, candidateDB); err != nil {
		return err
	}

	if err := validateRequiredTables(ctx, candidateDB); err != nil {
		return err
	}

	return validateMigrationCompatibility(liveDB, candidateDB)
}

func validateSQLiteIntegrity(ctx context.Context, candidateDB *sql.DB) error {
	rows, err := candidateDB.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return err
	}
	defer func() {
		_ = rows.Close()
	}()

	hasRows := false
	for rows.Next() {
		hasRows = true
		var result string
		if err := rows.Scan(&result); err != nil {
			return err
		}

		if !strings.EqualFold(result, "ok") {
			return fmt.Errorf("integrity check failed: %s", result)
		}
	}

	if err := rows.Err(); err != nil {
		return err
	}

	if !hasRows {
		return errors.New("integrity check returned no result")
	}

	return nil
}

func validateRequiredTables(ctx context.Context, candidateDB *sql.DB) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(requiredRestoreTables)), ",")
	query := "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN (" + placeholders + ")"

	args := make([]any, 0, len(requiredRestoreTables))
	for i := range requiredRestoreTables {
		args = append(args, requiredRestoreTables[i])
	}

	var tableCount int
	if err := candidateDB.QueryRowContext(ctx, query, args...).Scan(&tableCount); err != nil {
		return err
	}

	if tableCount != len(requiredRestoreTables) {
		return fmt.Errorf("backup is missing required tables: expected %d, found %d", len(requiredRestoreTables), tableCount)
	}

	return nil
}

func validateMigrationCompatibility(liveDB *sql.DB, candidateDB *sql.DB) error {
	liveVersion, liveDirty, err := GetMigrationVersion(liveDB)
	if err != nil {
		return fmt.Errorf("failed to read current migration version: %w", err)
	}

	if liveDirty {
		return errors.New("current database is in a dirty migration state")
	}

	candidateVersion, candidateDirty, err := GetMigrationVersion(candidateDB)
	if err != nil {
		return fmt.Errorf("failed to read uploaded migration version: %w", err)
	}

	if candidateDirty {
		return errors.New("uploaded backup is in a dirty migration state")
	}

	if candidateVersion != liveVersion {
		return fmt.Errorf("migration version mismatch: uploaded=%d current=%d", candidateVersion, liveVersion)
	}

	return nil
}
