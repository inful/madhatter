package database

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNew_EnablesWALJournalMode pins security review finding #4:
// the database must open in WAL (write-ahead log) mode. Without
// it, the read+maintenance workload causes SQLITE_BUSY on
// concurrent dashboard reads and the lockstep write pattern is
// fsync-heavy. WAL pairs with synchronous=NORMAL for safe
// crash recovery without the per-commit fsync cost.
//
// "wal" is the SQLite string for journal_mode=WAL; the pragma
// returns the new mode as a text row.
func TestNew_EnablesWALJournalMode(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	var mode string
	require.NoError(t, db.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode))
	assert.Equal(t, "wal", mode,
		"journal_mode must be set to WAL — default DELETE causes SQLITE_BUSY under concurrent reads")
}

// TestNew_SetsSynchronousNormal pairs with WAL mode: NORMAL is
// the documented safe-durability setting for WAL (full sync is
// overkill, OFF is unsafe). Returns as integer (1=NORMAL, 0=OFF,
// 2=FULL, 3=EXTRA).
func TestNew_SetsSynchronousNormal(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	var sync int
	require.NoError(t, db.db.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&sync))
	assert.Equal(t, 1, sync,
		"synchronous must be NORMAL (1) — FULL is fsync-heavy, OFF is unsafe")
}

// TestNew_EnablesSecureDelete pins the data-hygiene leg of the
// PRAGMA settings: secure_delete=ON overwrites deleted rows with
// zeros before reusing the page. Without it, deleted OAuth
// refresh tokens, sessions, and leave records can persist in
// SQLite pages indefinitely and be recovered from a leaked
// backup via filesystem forensics. Returns as integer (0=OFF,
// 1=ON).
func TestNew_EnablesSecureDelete(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	var secureDelete int
	require.NoError(t, db.db.QueryRowContext(ctx, "PRAGMA secure_delete").Scan(&secureDelete))
	assert.Equal(t, 1, secureDelete,
		"secure_delete must be ON so deleted rows are zeroed before page reuse")
}

// TestNew_EnablesTempStoreMemory pins the last PRAGMA: temp_store
// = MEMORY keeps intermediate result sets (sort buffers, large
// query scratch) in RAM instead of spilling to a temp file on
// disk. Returns as integer (0=DEFAULT, 1=FILE, 2=MEMORY).
// Defense-in-depth — temp tables spilling to disk means
// intermediate query results can outlive the query and end up
// in /tmp.
func TestNew_EnablesTempStoreMemory(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	var tempStore int
	require.NoError(t, db.db.QueryRowContext(ctx, "PRAGMA temp_store").Scan(&tempStore))
	assert.Equal(t, 2, tempStore,
		"temp_store must be MEMORY (2) — FILE spills intermediate results to /tmp")
}

// TestNew_PreservesForeignKeyEnablement is a regression guard:
// the security review's PRAGMA block is added to New() in
// addition to the existing foreign_keys pragma. This test pins
// that the FK pragma still fires (returning 1 = ON).
func TestNew_PreservesForeignKeyEnablement(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	var fk int
	require.NoError(t, db.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk))
	assert.Equal(t, 1, fk,
		"foreign_keys must remain ON — the new PRAGMA block must not regress existing behavior")
}

// TestNew_AllPragmasSet is a meta-test that exercises all the
// pragmas in one pass, so a regression in any one of them is
// reported as a single failure with a clear diff. Belongs at
// the bottom so it isn't split across the individual tests
// above when one of them fails. PRAGMA values are read directly
// (not via SELECT CAST, which SQLite's parser rejects for
// pragmas in this ncruces/go-sqlite3 build); the string form
// of journal_mode is treated literally while the integer
// pragmas are normalised to a decimal string for comparison.
func TestNew_AllPragmasSet(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	checks := []struct {
		name string
		want string
		read func() string
	}{
		{"journal_mode", "wal", func() string {
			var v string
			require.NoError(t, db.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&v))
			return v
		}},
		{"synchronous", "1", func() string {
			var v int
			require.NoError(t, db.db.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&v))
			return strconv.Itoa(v)
		}},
		{"secure_delete", "1", func() string {
			var v int
			require.NoError(t, db.db.QueryRowContext(ctx, "PRAGMA secure_delete").Scan(&v))
			return strconv.Itoa(v)
		}},
		{"temp_store", "2", func() string {
			var v int
			require.NoError(t, db.db.QueryRowContext(ctx, "PRAGMA temp_store").Scan(&v))
			return strconv.Itoa(v)
		}},
		{"foreign_keys", "1", func() string {
			var v int
			require.NoError(t, db.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&v))
			return strconv.Itoa(v)
		}},
	}
	for _, c := range checks {
		got := c.read()
		assert.Equal(t, c.want, got, "PRAGMA %s", c.name)
	}
}
