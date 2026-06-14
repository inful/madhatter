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

	// Materialize a 14-day forward window so the test is independent
	// of the current day-of-week. The window always contains at least
	// one occurrence of each recurring weekday, but the count varies
	// by which day of the week the test runs on — so we count directly
	// in the period where the rows landed rather than going through
	// GetQuotaStatus (which is anchored to today and may report a
	// different period when today is Fri–Sun).
	today := time.Now().UTC()
	start, end := today, today.AddDate(0, 0, 14)
	_, err = svc.EnsureRecurringMaterializedForMember(ctx, memberID, start, end)
	require.NoError(t, err)

	rows, err := db.GetWFHRequestsByMember(ctx, memberID)
	require.NoError(t, err)
	require.NotEmpty(t, rows, "materializer must insert at least one row")

	// Find the period of the first materialized row and count rows in it.
	firstDate, err := time.Parse("2006-01-02", rows[0].Date)
	require.NoError(t, err)
	periodStart, periodEnd, err := svc.ComputePeriodBounds(firstDate)
	require.NoError(t, err)

	used, err := db.GetWFHRequestsUsedInPeriod(ctx, memberID,
		periodStart.Format("2006-01-02"), periodEnd.Format("2006-01-02"))
	require.NoError(t, err)
	// 14 days contains 1-2 occurrences of each of Wed and Thu, so
	// 1-2 rows per period.
	assert.GreaterOrEqual(t, len(used), 1)
	assert.LessOrEqual(t, len(used), 2)
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
