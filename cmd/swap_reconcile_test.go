package cmd

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupSwapTestDB creates a temp DB at a unique path and sets
// MIGRATIONS_PATH so database.New finds the migrations directory.
// Mirrors the same dance the calendar/ical_token_test.go tests use.
func setupSwapTestDB(t *testing.T) *database.DB {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	// Test file lives at <repo>/cmd/swap_reconcile_test.go. repoRoot
	// is one level up.
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := database.New(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seedDriftedSwap creates the production-anomaly state the CLI is
// designed to repair: Alice ↔ Bob swap exists with status='accepted',
// both rows have is_swapped=1, but a cover-scheduler stomp has
// reverted Bob's row back to Bob (instead of Alice). Returns the
// IDs the test needs to assert on.
//
// Post-ExecuteSwap with the v0.32.5 captured-pair semantics, the
// assignments were:
//
//	aliceAssignment.member_id = bobID  (target_member_id)
//	bobAssignment.member_id   = aliceID (requester_member_id)
//
// The stomp reverts bobAssignment back to bobID; aliceAssignment
// is left at bobID (already drifted, but consistent with the
// captured pair — no drift there).
func seedDriftedSwap(t *testing.T, db *database.DB) (aliceAssignmentID, bobAssignmentID, swapID, aliceID, bobID string) {
	t.Helper()
	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	baseDate := time.Now().UTC().AddDate(0, 0, 7)
	aliceAssignmentID, err = db.CreateRotaAssignment(ctx, baseDate.Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)
	bobAssignmentID, err = db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 1).Format("2006-01-02"), bobID, false, nil)
	require.NoError(t, err)

	swapID, err = db.CreateHatSwap(ctx, aliceAssignmentID, bobAssignmentID, aliceID, bobID)
	require.NoError(t, err)
	require.NoError(t, db.ExecuteSwap(ctx, swapID))

	// Drift: stomp bob's row back to Bob. Mirrors the pre-v0.32.3
	// cover-scheduler bug.
	_, err = db.ExecContext(ctx,
		`UPDATE rota_assignments SET member_id = ? WHERE id = ?`,
		bobID, bobAssignmentID)
	require.NoError(t, err)

	return aliceAssignmentID, bobAssignmentID, swapID, aliceID, bobID
}

// TestRunSwapReconcile_MissingTarget pins the CLI usage guard:
// neither --id nor --all produces a typed usage error that the
// CLI maps to exit 2. The test asserts on the error identity rather
// than the exit code so it doesn't have to intercept os.Exit.
func TestRunSwapReconcile_MissingTarget(t *testing.T) {
	db := setupSwapTestDB(t)

	_, err := runSwapReconcile(context.Background(), db, "", false, false)
	require.ErrorIs(t, err, errSwapReconcileMissingTarget)
}

// TestRunSwapReconcile_MutuallyExclusive pins the second CLI
// usage guard: passing both --id and --all is a misuse.
func TestRunSwapReconcile_MutuallyExclusive(t *testing.T) {
	db := setupSwapTestDB(t)

	_, err := runSwapReconcile(context.Background(), db, "some-id", true, false)
	require.ErrorIs(t, err, errSwapReconcileMutuallyExclusive)
}

// TestRunSwapReconcile_NotFound pins the unknown-id path. The
// underlying DB returns ErrSwapNotFound; the helper surfaces it
// unchanged so the CLI can render a clear stderr message.
func TestRunSwapReconcile_NotFound(t *testing.T) {
	db := setupSwapTestDB(t)

	_, err := runSwapReconcile(context.Background(), db, "no-such-swap", false, false)
	require.Error(t, err)
	assert.ErrorIs(t, err, database.ErrSwapNotFound)
}

// TestRunSwapReconcile_DryRunDoesNotMutate pins the dry-run safety:
// with apply=false the helper returns the drift list but leaves
// the rows on disk untouched. The operator can scan the report
// without committing anything.
func TestRunSwapReconcile_DryRunDoesNotMutate(t *testing.T) {
	db := setupSwapTestDB(t)
	aliceAssignmentID, bobAssignmentID, swapID, _, bobID := seedDriftedSwap(t, db)

	results, err := runSwapReconcile(context.Background(), db, swapID, false, false)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.NotEmpty(t, results[0].TargetDrift, "drifted swap must surface as drift in dry-run")

	// Bob's row is still drifted.
	got, err := db.GetAssignmentByID(context.Background(), bobAssignmentID)
	require.NoError(t, err)
	assert.Equal(t, bobID, got.MemberID, "dry-run must not flip member_id back to aliceID")

	// Alice's row is unchanged too (still at bobID, the captured
	// target_member_id).
	gotAlice, err := db.GetAssignmentByID(context.Background(), aliceAssignmentID)
	require.NoError(t, err)
	assert.Equal(t, bobID, gotAlice.MemberID, "Alice's row must not be flipped in dry-run")
}

// TestRunSwapReconcile_ApplyMutates pins the apply path: the
// helper rewrites the rows to match the captured pair. This is
// the end-to-end repair path operators run to fix the production
// anomaly.
func TestRunSwapReconcile_ApplyMutates(t *testing.T) {
	db := setupSwapTestDB(t)
	aliceAssignmentID, bobAssignmentID, swapID, aliceID, bobID := seedDriftedSwap(t, db)

	results, err := runSwapReconcile(context.Background(), db, swapID, false, true)
	require.NoError(t, err)
	require.Len(t, results, 1)

	gotAlice, err := db.GetAssignmentByID(context.Background(), aliceAssignmentID)
	require.NoError(t, err)
	assert.Equal(t, bobID, gotAlice.MemberID, "Alice's row should now be bobID (captured target_member)")
	assert.True(t, gotAlice.IsSwapped)

	gotBob, err := db.GetAssignmentByID(context.Background(), bobAssignmentID)
	require.NoError(t, err)
	assert.Equal(t, aliceID, gotBob.MemberID, "Bob's row should now be aliceID (captured requester_member)")
	assert.True(t, gotBob.IsSwapped)
}

// TestRunSwapReconcile_AllBulk covers the --all path. The helper
// walks every accepted swap; one drifted and one healthy. The
// healthy swap returns an empty drift list (already-reconciled
// case), and the drifted swap is repaired when apply=true.
func TestRunSwapReconcile_AllBulk(t *testing.T) {
	db := setupSwapTestDB(t)
	aliceAssignmentID, bobAssignmentID, _, aliceID, bobID := seedDriftedSwap(t, db)

	ctx := context.Background()
	carolID, err := db.AddTeamMember(ctx, "Carol", "carol@example.com")
	require.NoError(t, err)
	daveID, err := db.AddTeamMember(ctx, "Dave", "dave@example.com")
	require.NoError(t, err)
	baseDate := time.Now().UTC().AddDate(0, 0, 7)
	carolAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 2).Format("2006-01-02"), carolID, false, nil)
	require.NoError(t, err)
	daveAssignmentID, err := db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 3).Format("2006-01-02"), daveID, false, nil)
	require.NoError(t, err)
	swapID2, err := db.CreateHatSwap(ctx, carolAssignmentID, daveAssignmentID, carolID, daveID)
	require.NoError(t, err)
	require.NoError(t, db.ExecuteSwap(ctx, swapID2))

	// Apply both at once.
	results, err := runSwapReconcile(ctx, db, "", true, true)
	require.NoError(t, err)
	require.Len(t, results, 2)

	// Swap 1 rows are repaired.
	gotAlice, err := db.GetAssignmentByID(ctx, aliceAssignmentID)
	require.NoError(t, err)
	assert.Equal(t, bobID, gotAlice.MemberID)

	gotBob, err := db.GetAssignmentByID(ctx, bobAssignmentID)
	require.NoError(t, err)
	assert.Equal(t, aliceID, gotBob.MemberID)

	// Swap 2 rows match the captured pair (no drift, no
	// mutation needed).
	gotCarol, err := db.GetAssignmentByID(ctx, carolAssignmentID)
	require.NoError(t, err)
	assert.Equal(t, daveID, gotCarol.MemberID)
	gotDave, err := db.GetAssignmentByID(ctx, daveAssignmentID)
	require.NoError(t, err)
	assert.Equal(t, carolID, gotDave.MemberID)

	// Idempotency: re-running on the now-reconciled swaps reports
	// no drift.
	postApply, err := runSwapReconcile(ctx, db, "", true, false)
	require.NoError(t, err)
	for _, r := range postApply {
		assert.Empty(t, r.RequesterDrift)
		assert.Empty(t, r.TargetDrift)
	}

	_ = swapID2
}
