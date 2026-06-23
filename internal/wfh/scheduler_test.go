package wfh

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/database/sqlc"
	"github.com/inful/madhatter/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduler_StartStopAndSettle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		db, cleanup := setupWFHTestDB(t)
		t.Cleanup(cleanup)

		cfg := testConfig()
		cfg.MinOnsitePercentage = 0
		cfg.MinOnsiteAbsolute = 0
		cfg.SettlementDays = 14
		svc := NewService(db, cfg)

		memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
		require.NoError(t, err)
		date := time.Now().UTC().Format("2006-01-02")
		request, err := db.CreateWFHRequest(ctx, memberID, date)
		require.NoError(t, err)

		scheduler := NewScheduler(svc)
		scheduler.interval = time.Millisecond

		require.NoError(t, scheduler.Start())
		require.Error(t, scheduler.Start())

		synctest.Wait()

		updated, err := db.GetWFHRequestByID(ctx, request.ID)
		require.NoError(t, err)
		assert.Equal(t, database.WFHStatusApproved, updated.Status)

		scheduler.Stop()
		assert.False(t, scheduler.running)

		request2, err := db.CreateWFHRequest(ctx, memberID, testutil.NextBusinessDay(time.Now().UTC().AddDate(0, 0, 1)).Format("2006-01-02"))
		require.NoError(t, err)

		require.NoError(t, scheduler.Start())
		synctest.Wait()

		updated2, err := db.GetWFHRequestByID(ctx, request2.ID)
		require.NoError(t, err)
		assert.Equal(t, database.WFHStatusApproved, updated2.Status)

		scheduler.Stop()
	})
}

// TestScheduler_PurgesPastPeriodsWhenEnabled verifies that the scheduler
// runs the past-period purge after settle when both WFH and PurgeEnabled
// are on. Uses synctest so the ticker-driven tick happens deterministically.
func TestScheduler_PurgesPastPeriodsWhenEnabled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		db, cleanup := setupWFHTestDB(t)
		t.Cleanup(cleanup)

		cfg := testConfig()
		cfg.SettlementDays = 14
		svc := NewService(db, cfg)
		require.True(t, svc.IsPurgeEnabled(), "precondition: default config enables purge")

		memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
		require.NoError(t, err)

		// Compute the cutoff from the bubble clock so the test is
		// independent of when synctest starts. Anything earlier is
		// guaranteed to be in the strictly-past band.
		cutoff, err := svc.previousPeriodStart(time.Now().UTC())
		require.NoError(t, err)
		pastDate := cutoff.AddDate(0, 0, -5)
		_, err = db.GetQueries().CreateWFHRequest(ctx, sqlc.CreateWFHRequestParams{
			ID:       "scheduler-purge-target",
			MemberID: memberID,
			Date:     pastDate,
		})
		require.NoError(t, err)

		scheduler := NewScheduler(svc)
		scheduler.interval = time.Millisecond

		require.NoError(t, scheduler.Start())
		synctest.Wait()
		scheduler.Stop()

		// The past row must be gone after one settle+purge tick.
		_, err = db.GetWFHRequestByID(ctx, "scheduler-purge-target")
		require.ErrorIs(t, err, database.ErrWFHNotFound,
			"scheduler must purge the past row on the first tick")
	})
}

// TestScheduler_SkipsPurgeWhenPurgeDisabled verifies the scheduler
// honors the PurgeEnabled=false opt-out and leaves historical rows in
// place even though settlement still runs.
func TestScheduler_SkipsPurgeWhenPurgeDisabled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		db, cleanup := setupWFHTestDB(t)
		t.Cleanup(cleanup)

		cfg := testConfig()
		cfg.PurgeEnabled = false
		svc := NewService(db, cfg)
		require.False(t, svc.IsPurgeEnabled())

		memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
		require.NoError(t, err)

		cutoff, err := svc.previousPeriodStart(time.Now().UTC())
		require.NoError(t, err)
		pastDate := cutoff.AddDate(0, 0, -5)
		_, err = db.GetQueries().CreateWFHRequest(ctx, sqlc.CreateWFHRequestParams{
			ID:       "scheduler-purge-skip",
			MemberID: memberID,
			Date:     pastDate,
		})
		require.NoError(t, err)

		scheduler := NewScheduler(svc)
		scheduler.interval = time.Millisecond

		require.NoError(t, scheduler.Start())
		synctest.Wait()
		scheduler.Stop()

		// Past row must still exist — purge is opted out.
		_, err = db.GetWFHRequestByID(ctx, "scheduler-purge-skip")
		require.NoError(t, err, "purge-disabled scheduler must not delete past rows")
	})
}

// TestScheduler_PeriodicTickFires verifies the periodic path — not just
// the immediate-on-Start tick. It uses synctest to advance the bubble
// clock past one ticker interval and asserts that runSettle was called
// at least twice. Without this test, the periodicSettle goroutine could
// silently break (e.g. a refactor that swallows the ticker.C case)
// without any test failing.
func TestScheduler_PeriodicTickFires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db, cleanup := setupWFHTestDB(t)
		t.Cleanup(cleanup)

		cfg := testConfig()
		cfg.MinOnsitePercentage = 0
		cfg.MinOnsiteAbsolute = 0
		cfg.SettlementDays = 14
		svc := NewService(db, cfg)

		scheduler := NewScheduler(svc)
		// 1h interval — long enough that synctest.Wait() after Start
		// does not advance the clock past the periodic tick on its
		// own, but short enough that the test stays fast under
		// time.Sleep.
		scheduler.interval = time.Hour

		require.NoError(t, scheduler.Start())
		synctest.Wait()

		// Immediate tick ran once. Any subsequent ticks are periodic.
		require.Equal(t, int64(1), scheduler.ticks.Load(),
			"start must trigger exactly one immediate runSettle")

		// Advance the bubble clock past the first ticker interval.
		// time.Sleep in a synctest bubble advances the fake clock and
		// waits for the ticker goroutine to reach a stable point, so
		// the periodic runSettle has executed by the time we read.
		time.Sleep(time.Hour + time.Millisecond)
		synctest.Wait()
		require.GreaterOrEqual(t, scheduler.ticks.Load(), int64(2),
			"periodic ticker must fire runSettle after the interval elapses")

		// One more interval — verify the loop keeps firing rather than
		// exiting after the first tick.
		time.Sleep(time.Hour + time.Millisecond)
		synctest.Wait()
		require.GreaterOrEqual(t, scheduler.ticks.Load(), int64(3),
			"ticker must keep firing across multiple intervals")

		scheduler.Stop()

		// No further ticks after Stop.
		finalTicks := scheduler.ticks.Load()
		time.Sleep(time.Hour + time.Millisecond)
		synctest.Wait()
		require.Equal(t, finalTicks, scheduler.ticks.Load(),
			"no ticks must fire after Stop closes stopChan")
	})
}
