package cmd

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupBackupTestDB creates a temp DB at a unique path and sets
// MIGRATIONS_PATH so database.New finds the migrations directory.
// Mirrors setupSwapTestDB (cmd/swap_reconcile_test.go).
func setupBackupTestDB(t *testing.T) *database.DB {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := database.New(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestRunBackup_WritesSQLiteFile pins the happy path. The destination
// must exist, start with the SQLite magic header, and be readable as a
// file. Size is reported via the return so the operator sees how big
// the snapshot was.
func TestRunBackup_WritesSQLiteFile(t *testing.T) {
	db := setupBackupTestDB(t)

	ctx := context.Background()
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com", nil)
	require.NoError(t, err)

	targetPath := filepath.Join(t.TempDir(), "out.db")
	require.NoFileExists(t, targetPath, "precondition: target must not pre-exist")

	written, err := runBackup(ctx, db, targetPath, false)
	require.NoError(t, err)
	require.Positive(t, written)

	//nolint:gosec // targetPath is constructed from t.TempDir().
	gotBytes, err := os.ReadFile(targetPath)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(gotBytes), "SQLite format 3\x00"),
		"backup output must be a valid SQLite file")
}

// TestRunBackup_Applies0600Permissions pins the file mode. Backup files
// contain the full database — including OAuth tokens (encrypted) and
// session data — so they must be readable only by the running user.
func TestRunBackup_Applies0600Permissions(t *testing.T) {
	db := setupBackupTestDB(t)

	targetPath := filepath.Join(t.TempDir(), "out.db")
	_, err := runBackup(context.Background(), db, targetPath, false)
	require.NoError(t, err)

	info, err := os.Stat(targetPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"backup file mode must be 0600 (matches the ICS export convention)")
}

// TestRunBackup_RefusesToOverwrite pins the safety net: by default the
// command refuses to clobber an existing file. This protects an
// operator who fat-fingers the path and points at, say, their previous
// backup.
func TestRunBackup_RefusesToOverwrite(t *testing.T) {
	db := setupBackupTestDB(t)

	targetPath := filepath.Join(t.TempDir(), "out.db")
	// Seed the target with a sentinel so we can prove the failed
	// run didn't touch it.
	sentinel := "do-not-overwrite"
	require.NoError(t, os.WriteFile(targetPath, []byte(sentinel), 0o600))

	_, err := runBackup(context.Background(), db, targetPath, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	//nolint:gosec // targetPath is constructed from t.TempDir().
	gotBytes, err := os.ReadFile(targetPath)
	require.NoError(t, err)
	assert.Equal(t, sentinel, string(gotBytes),
		"refused run must leave the existing file untouched")
}

// TestRunBackup_ForceOverwrites pins the explicit opt-in. With force=true
// the existing file is replaced by a fresh SQLite snapshot.
func TestRunBackup_ForceOverwrites(t *testing.T) {
	db := setupBackupTestDB(t)

	targetPath := filepath.Join(t.TempDir(), "out.db")
	require.NoError(t, os.WriteFile(targetPath, []byte("not a sqlite file"), 0o600))

	written, err := runBackup(context.Background(), db, targetPath, true)
	require.NoError(t, err)
	require.Positive(t, written)

	//nolint:gosec // targetPath is constructed from t.TempDir().
	gotBytes, err := os.ReadFile(targetPath)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(gotBytes), "SQLite format 3\x00"),
		"force overwrite must produce a fresh SQLite snapshot")
}
