package rota

import (
	"context"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotLeaveCovers covers the cover-snapshot helper used by
// ReassignCovers to diff cover sets before and after a reassign. The
// helper's job is narrow: return a map[date]memberID of every cover
// assignment currently active in the leave's date range, with no
// non-cover rows and no rows outside the range. A regression here
// directly corrupts the CoversChanged counter that drives reassign
// idempotency, so the contract is worth pinning.
//
// Each subtest gets its own in-memory DB because setupTestDB constructs
// a fresh DB per call and t.Parallel on the parent would let subtests
// share the parent's DB — but the parent has no DB, only the subtests
// do.
func TestSnapshotLeaveCovers(t *testing.T) {
	t.Parallel()

	// 2024-01-15 (Mon) → 2024-01-19 (Fri), the leave window we'll
	// snapshot against. Pin the dates so the tests are deterministic
	// regardless of when the suite runs.
	start := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 19, 0, 0, 0, 0, time.UTC)

	t.Run("InvertedDateRange", func(t *testing.T) {
		db, cleanup := setupTestDB(t)
		defer cleanup()
		ctx := context.Background()

		// EndDate before StartDate is treated as a defensive no-op:
		// the snapshot returns empty rather than querying the DB with
		// a backward range. Pin this so a future refactor that drops
		// the guard doesn't silently query nonsense.
		l := &database.LeaveRecord{StartDate: end, EndDate: start}
		got := snapshotLeaveCovers(ctx, db, l)
		assert.Empty(t, got, "inverted range must produce an empty snapshot")
	})

	t.Run("NoAssignmentsInRange", func(t *testing.T) {
		db, cleanup := setupTestDB(t)
		defer cleanup()
		ctx := context.Background()

		aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
		require.NoError(t, err)
		_ = aliceID // not needed for this case, but kept symmetric with the other cases

		l := &database.LeaveRecord{StartDate: start, EndDate: end}
		got := snapshotLeaveCovers(ctx, db, l)
		assert.Empty(t, got, "no assignments means no covers")
	})

	t.Run("MixedOriginalsAndCovers", func(t *testing.T) {
		db, cleanup := setupTestDB(t)
		defer cleanup()
		ctx := context.Background()

		aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
		require.NoError(t, err)
		bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
		require.NoError(t, err)

		// Build a five-day schedule. Each day gets Alice as the
		// original HAT, then Bob as the cover. The snapshot must pick
		// out the cover rows only — originals are not "covers" of
		// anyone's leave.
		for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
			originalID, err := db.CreateRotaAssignment(ctx, d.Format("2006-01-02"), aliceID, false, nil)
			require.NoError(t, err)
			_, err = db.CreateRotaAssignment(ctx, d.Format("2006-01-02"), bobID, true, &originalID)
			require.NoError(t, err)
		}

		l := &database.LeaveRecord{StartDate: start, EndDate: end}
		got := snapshotLeaveCovers(ctx, db, l)

		// Every day in the window maps to Bob. The five original
		// assignments (Alice) are excluded because IsCover=false.
		require.Len(t, got, 5, "five cover rows expected across the five-day window")
		for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
			assert.Equal(t, bobID, got[d.Format("2006-01-02")], "cover on %s must be Bob", d.Format("2006-01-02"))
		}
	})

	t.Run("OutOfRangeAssignmentsExcluded", func(t *testing.T) {
		db, cleanup := setupTestDB(t)
		defer cleanup()
		ctx := context.Background()

		aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
		require.NoError(t, err)
		bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
		require.NoError(t, err)

		// One cover inside the window, one cover the day before, one
		// the day after. Only the middle one should appear in the
		// snapshot. Pin the inclusive boundaries — both endpoints of
		// the range are included, nothing outside is.
		before := start.AddDate(0, 0, -1)
		after := end.AddDate(0, 0, 1)

		origBefore, err := db.CreateRotaAssignment(ctx, before.Format("2006-01-02"), aliceID, false, nil)
		require.NoError(t, err)
		_, err = db.CreateRotaAssignment(ctx, before.Format("2006-01-02"), bobID, true, &origBefore)
		require.NoError(t, err)

		origMiddle, err := db.CreateRotaAssignment(ctx, start.Format("2006-01-02"), aliceID, false, nil)
		require.NoError(t, err)
		_, err = db.CreateRotaAssignment(ctx, start.Format("2006-01-02"), bobID, true, &origMiddle)
		require.NoError(t, err)

		origAfter, err := db.CreateRotaAssignment(ctx, after.Format("2006-01-02"), aliceID, false, nil)
		require.NoError(t, err)
		_, err = db.CreateRotaAssignment(ctx, after.Format("2006-01-02"), bobID, true, &origAfter)
		require.NoError(t, err)

		l := &database.LeaveRecord{StartDate: start, EndDate: end}
		got := snapshotLeaveCovers(ctx, db, l)

		require.Len(t, got, 1, "only the cover inside the window must appear")
		assert.Equal(t, bobID, got[start.Format("2006-01-02")])
		_, existsBefore := got[before.Format("2006-01-02")]
		_, existsAfter := got[after.Format("2006-01-02")]
		assert.False(t, existsBefore, "cover one day before window must be excluded")
		assert.False(t, existsAfter, "cover one day after window must be excluded")
	})

	t.Run("SingleDayLeave", func(t *testing.T) {
		db, cleanup := setupTestDB(t)
		defer cleanup()
		ctx := context.Background()

		aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
		require.NoError(t, err)
		bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
		require.NoError(t, err)

		// A single-day leave where start == end. The window still
		// snapshots correctly — pin that the helper handles the
		// degenerate range.
		origID, err := db.CreateRotaAssignment(ctx, start.Format("2006-01-02"), aliceID, false, nil)
		require.NoError(t, err)
		_, err = db.CreateRotaAssignment(ctx, start.Format("2006-01-02"), bobID, true, &origID)
		require.NoError(t, err)

		l := &database.LeaveRecord{StartDate: start, EndDate: start}
		got := snapshotLeaveCovers(ctx, db, l)
		require.Len(t, got, 1)
		assert.Equal(t, bobID, got[start.Format("2006-01-02")])
	})
}
