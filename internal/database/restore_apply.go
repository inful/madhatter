package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
)

const defaultRestoreTableCapacity = 16

// ApplyRestoreCandidate validates and applies an uploaded backup to the live database.
func (db *DB) ApplyRestoreCandidate(ctx context.Context, backupBytes []byte) error {
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

	if err := runRestoreCandidateChecks(ctx, db.db, candidateDB); err != nil {
		_ = candidateDB.Close()
		return err
	}

	if err := candidateDB.Close(); err != nil {
		return err
	}

	return db.applyRestoreFromFile(ctx, tmpPath)
}

func (db *DB) applyRestoreFromFile(ctx context.Context, restorePath string) error {
	conn, err := db.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = conn.Close()
	}()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	tableNames, err := prepareRestoreTransaction(ctx, tx, restorePath)
	if err != nil {
		return err
	}

	if err := copyTablesFromRestore(ctx, tx, tableNames); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true

	if err := detachRestoreDatabase(ctx, conn); err != nil {
		return err
	}

	if err := postRestoreIntegrityChecks(ctx, db.db); err != nil {
		return err
	}

	return nil
}

func prepareRestoreTransaction(ctx context.Context, tx *sql.Tx, restorePath string) ([]string, error) {
	if _, err := tx.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, "ATTACH DATABASE ? AS restore_db", restorePath); err != nil {
		return nil, err
	}

	tableNames, err := getRestoreTableNames(ctx, tx)
	if err != nil {
		return nil, err
	}

	if len(tableNames) == 0 {
		return nil, errors.New("uploaded backup contains no tables")
	}

	return tableNames, nil
}

func copyTablesFromRestore(ctx context.Context, tx *sql.Tx, tableNames []string) error {
	for i := range tableNames {
		table := quoteIdentifier(tableNames[i])
		//nolint:gosec // Table names come from sqlite_master and are safely quoted as identifiers.
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return err
		}

		//nolint:gosec // Table names come from sqlite_master and are safely quoted as identifiers.
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+" SELECT * FROM restore_db."+table); err != nil {
			return err
		}
	}

	return nil
}

func detachRestoreDatabase(ctx context.Context, conn *sql.Conn) error {
	_, err := conn.ExecContext(ctx, "DETACH DATABASE restore_db")
	if err == nil {
		return nil
	}

	if strings.Contains(strings.ToLower(err.Error()), "no such database") {
		return nil
	}

	return err
}

func getRestoreTableNames(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT name FROM restore_db.sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	tableNames := make([]string, 0, defaultRestoreTableCapacity)
	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			return nil, err
		}
		tableNames = append(tableNames, tableName)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return tableNames, nil
}

func quoteIdentifier(input string) string {
	return `"` + strings.ReplaceAll(input, `"`, `""`) + `"`
}

func postRestoreIntegrityChecks(ctx context.Context, db *sql.DB) error {
	if err := validateSQLiteIntegrity(ctx, db); err != nil {
		return fmt.Errorf("post-restore integrity check failed: %w", err)
	}

	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer func() {
		_ = rows.Close()
	}()

	if rows.Next() {
		var table string
		var rowID int64
		var parent string
		var fkID int64
		if err := rows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			return err
		}
		return fmt.Errorf("foreign key check failed: table=%s rowid=%d parent=%s fkid=%d", table, rowID, parent, fkID)
	}

	return rows.Err()
}
