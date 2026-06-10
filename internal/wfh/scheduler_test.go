package wfh

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/inful/madhatter/internal/database"
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

		request2, err := db.CreateWFHRequest(ctx, memberID, nextBusinessDay(time.Now().UTC().AddDate(0, 0, 1)).Format("2006-01-02"))
		require.NoError(t, err)

		require.NoError(t, scheduler.Start())
		synctest.Wait()

		updated2, err := db.GetWFHRequestByID(ctx, request2.ID)
		require.NoError(t, err)
		assert.Equal(t, database.WFHStatusApproved, updated2.Status)

		scheduler.Stop()
	})
}
