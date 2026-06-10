package wfh

import (
	"context"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetQuotaStatus_RecurringDaysCountAfterMaterialization(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	require.NoError(t, db.SetTeamMemberRecurringWFHDays(ctx, memberID, database.RecurringWFHDays{
		Wednesday: true,
		Thursday:  true,
	}))

	// Before materialization: quota is fully available.
	status, err := svc.GetQuotaStatus(ctx, memberID)
	require.NoError(t, err)
	assert.Equal(t, 0, status.Used)
	assert.Equal(t, 2, status.Remaining)

	// After materialization: two rows consumed in the current period.
	today := time.Now().UTC()
	start, end, err := svc.ComputePeriodBounds(today)
	require.NoError(t, err)
	_, err = svc.EnsureRecurringMaterializedForMember(ctx, memberID, start, end)
	require.NoError(t, err)

	status, err = svc.GetQuotaStatus(ctx, memberID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, status.Used, 1)
	assert.Equal(t, 0, status.Remaining)
}

func TestWithdrawRecurringDayFreesQuotaForDifferentDay(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	require.NoError(t, db.SetTeamMemberRecurringWFHDays(ctx, memberID, database.RecurringWFHDays{Thursday: true}))

	// Materialize the next 14 days, not just the current period, so
	// the test can pick a Thursday with a withdrawal deadline that's
	// comfortably in the future.
	materializeEnd := time.Now().UTC().AddDate(0, 0, 14)
	_, err = svc.EnsureRecurringMaterializedForMember(ctx, memberID, time.Now().UTC(), materializeEnd)
	require.NoError(t, err)

	// Find a Thursday at least 2 days out so the 24h deadline is well
	// in the future regardless of when the test runs.
	rows, err := db.GetWFHRequestsByMember(ctx, memberID)
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	minDate := time.Now().UTC().AddDate(0, 0, 2).Format("2006-01-02")
	var thursdayRow database.WFHRequest
	for _, r := range rows {
		if r.Date >= minDate && r.Date > thursdayRow.Date {
			thursdayRow = r
		}
	}
	require.NotEmpty(t, thursdayRow.ID, "no Thursday with date >= %s found", minDate)
	require.NoError(t, db.WithdrawOwnWFHRequest(ctx, thursdayRow.ID, memberID, 24))

	// A request for a different day in the period now has quota.
	otherDay := futureWeekday(time.Now().UTC(), time.Friday).Format("2006-01-02")
	hasQuota, err := svc.CheckQuota(ctx, memberID, otherDay)
	require.NoError(t, err)
	assert.True(t, hasQuota, "after withdrawing the recurring Thursday, Friday should have quota")
}
