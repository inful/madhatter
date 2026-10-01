package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeBackupFromCurrentDB takes a SQLite snapshot of db at its
// current state and writes it to a fresh temp file, returning the
// file path. The test fixture seeds the database, snapshots, mutates,
// then restores — so each restore test gets an input that pre-dates
// the post-snapshot mutations.
func writeBackupFromCurrentDB(t *testing.T, db *database.DB) string {
	t.Helper()
	ctx := context.Background()
	backupBytes, err := db.CreateBackup(ctx)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "candidate.db")
	require.NoError(t, os.WriteFile(path, backupBytes, 0o600))
	return path
}

// TestRunRestore_DryRun_DoesNotMutate pins the safety net. Without
// --apply the command validates the candidate file and exits
// without touching the live database. This is the default behavior
// the operator expects — "show me what would happen, then I'll
// commit explicitly".
func TestRunRestore_DryRun_DoesNotMutate(t *testing.T) {
	db := setupBackupTestDB(t)
	ctx := context.Background()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com", nil)
	require.NoError(t, err)
	inputPath := writeBackupFromCurrentDB(t, db)

	// Add another member after the snapshot.
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com", nil)
	require.NoError(t, err)

	mode, err := runRestore(ctx, db, inputPath, false)
	require.NoError(t, err)
	require.Equal(t, "validate", mode)

	members, err := db.GetActiveTeamMembers(ctx)
	require.NoError(t, err)
	require.Len(t, members, 2,
		"dry-run must leave the post-snapshot Bob row in place")
}

// TestRunRestore_Apply_MutatesDatabase pins the commit path. With
// --apply the live state matches the candidate after the operation:
// the post-snapshot Bob row is gone.
func TestRunRestore_Apply_MutatesDatabase(t *testing.T) {
	db := setupBackupTestDB(t)
	ctx := context.Background()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com", nil)
	require.NoError(t, err)
	inputPath := writeBackupFromCurrentDB(t, db)

	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com", nil)
	require.NoError(t, err)

	mode, err := runRestore(ctx, db, inputPath, true)
	require.NoError(t, err)
	require.Equal(t, "apply", mode)

	members, err := db.GetActiveTeamMembers(ctx)
	require.NoError(t, err)
	require.Len(t, members, 1)
	require.Equal(t, "Alice", members[0].Name)
}

// TestRunRestore_InputFileNotFound pins the input-validation
// path. The CLI must refuse with a clear error rather than letting
// the lower-level read fail with a generic os.PathError.
func TestRunRestore_InputFileNotFound(t *testing.T) {
	db := setupBackupTestDB(t)

	missing := filepath.Join(t.TempDir(), "does-not-exist.db")
	_, err := runRestore(context.Background(), db, missing, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found",
		"error should clearly identify the missing file")
}

// TestRunRestore_InputFileNotSQLite pins the validation path. A
// non-SQLite input must fail with a clear error in dry-run and
// apply modes — and crucially the database must remain unchanged.
func TestRunRestore_InputFileNotSQLite(t *testing.T) {
	db := setupBackupTestDB(t)

	garbagePath := filepath.Join(t.TempDir(), "garbage.db")
	require.NoError(t, os.WriteFile(garbagePath, []byte("not a sqlite file"), 0o600))

	_, err := runRestore(context.Background(), db, garbagePath, false)
	require.Error(t, err)

	// Apply mode must also fail — and crucially must leave the
	// database untouched.
	_, err = db.AddTeamMember(context.Background(), "Alice", "alice@example.com", nil)
	require.NoError(t, err)
	_, err = runRestore(context.Background(), db, garbagePath, true)
	require.Error(t, err)

	members, err := db.GetActiveTeamMembers(context.Background())
	require.NoError(t, err)
	require.Len(t, members, 1,
		"failed apply must not mutate the live database")
}

// TestRunRestore_RefusesEmptyInput pins the empty-file guard. An
// empty backup applied against the live database would issue
// DELETE FROM <every-table> with no follow-up INSERTs, silently wiping
// every row. The CLI catches this up front — before either validate
// or apply is dispatched — so the operator gets a clear "backup file
// is empty" error rather than a destructive success.
func TestRunRestore_RefusesEmptyInput(t *testing.T) {
	db := setupBackupTestDB(t)
	ctx := context.Background()

	// Pre-mutation marker — if the empty guard fails and apply
	// silently wipes the DB, this row disappears.
	_, err := db.AddTeamMember(ctx, "Pre", "pre@example.com", nil)
	require.NoError(t, err)

	emptyPath := filepath.Join(t.TempDir(), "empty.db")
	require.NoError(t, os.WriteFile(emptyPath, []byte(""), 0o600))

	for _, apply := range []bool{false, true} {
		_, runErr := runRestore(ctx, db, emptyPath, apply)
		require.Error(t, runErr, "apply=%v must refuse empty input", apply)
		assert.Contains(t, runErr.Error(), "empty",
			"error must clearly identify the empty-file cause")
	}

	members, err := db.GetActiveTeamMembers(ctx)
	require.NoError(t, err)
	require.Len(t, members, 1,
		"both dry-run and apply must leave the pre-mutation row in place")
	require.Equal(t, "Pre", members[0].Name)
}
