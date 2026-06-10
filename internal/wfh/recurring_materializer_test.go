package wfh

import (
	"context"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// futureWeekday returns the next occurrence of weekday strictly after from.
// If from is already weekday, returns from + 7 days.
func futureWeekday(from time.Time, weekday time.Weekday) time.Time {
	d := from.AddDate(0, 0, 1)
	for d.Weekday() != weekday {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// nextWeekdayInRange returns the next occurrence of weekday in the
// inclusive [start, end] range. Used by tests that need a
// deadline-friendly date (3+ days out from now is preferred for
// 24h-deadline tests).
func nextWeekdayInRange(t *testing.T, start, end time.Time, weekday time.Weekday) time.Time {
	t.Helper()
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == weekday {
			return d
		}
	}
	t.Fatalf("no %s in [%s, %s]", weekday, start.Format("2006-01-02"), end.Format("2006-01-02"))
	return time.Time{}
}

func TestEnsureRecurringMaterialized_FillsGaps(t *testing.T) {
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

	today := time.Now().UTC()
	start, end, err := svc.ComputePeriodBounds(today)
	require.NoError(t, err)

	inserted, err := svc.EnsureRecurringMaterializedForMember(ctx, memberID, start, end)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, inserted, 1, "expected at least one future recurring occurrence to be materialized")

	rows, err := db.GetWFHRequestsUsedInPeriod(ctx, memberID,
		start.Format("2006-01-02"), end.Format("2006-01-02"))
	require.NoError(t, err)
	for _, r := range rows {
		assert.Equal(t, database.WFHStatusApproved, r.Status)
		assert.True(t, r.IsRecurring, "materialized rows must carry is_recurring=1")
	}
}

func TestEnsureRecurringMaterialized_IsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	require.NoError(t, db.SetTeamMemberRecurringWFHDays(ctx, memberID, database.RecurringWFHDays{Thursday: true}))

	today := time.Now().UTC()
	start, end, err := svc.ComputePeriodBounds(today)
	require.NoError(t, err)

	first, err := svc.EnsureRecurringMaterializedForMember(ctx, memberID, start, end)
	require.NoError(t, err)
	second, err := svc.EnsureRecurringMaterializedForMember(ctx, memberID, start, end)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, first, 1)
	assert.Equal(t, 0, second, "second call must not insert duplicates")
}

func TestEnsureRecurringMaterialized_SkipsPastDates(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	require.NoError(t, db.SetTeamMemberRecurringWFHDays(ctx, memberID, database.RecurringWFHDays{Monday: true}))

	// Range entirely in the past — materializer must insert 0 rows.
	pastStart := time.Now().UTC().AddDate(0, 0, -30)
	pastEnd := time.Now().UTC().AddDate(0, 0, -1)
	inserted, err := svc.EnsureRecurringMaterializedForMember(ctx, memberID, pastStart, pastEnd)
	require.NoError(t, err)
	assert.Equal(t, 0, inserted)
}

func TestEnsureRecurringMaterialized_SkipsMembersWithoutRecurring(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	// No SetTeamMemberRecurringWFHDays call.

	today := time.Now().UTC()
	start, end, err := svc.ComputePeriodBounds(today)
	require.NoError(t, err)

	inserted, err := svc.EnsureRecurringMaterializedForMember(ctx, memberID, start, end)
	require.NoError(t, err)
	assert.Equal(t, 0, inserted)
}

func TestEnsureRecurringMaterialized_PreservesWithdrawnRow(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	require.NoError(t, db.SetTeamMemberRecurringWFHDays(ctx, memberID, database.RecurringWFHDays{Thursday: true}))

	// User explicitly withdraws the Thursday occurrence. We insert
	// the row directly into the *current* period and flip its status
	// to "withdrawn" via SQL so the 24h withdrawal deadline check
	// (which is time-of-day sensitive at the UTC date boundary) is
	// not exercised. The materializer's job is to skip existing rows
	// regardless of status, and that's what this test verifies.
	today := time.Now().UTC()
	start, end, err := svc.ComputePeriodBounds(today)
	require.NoError(t, err)
	thursday := nextWeekdayInRange(t, start, end, time.Thursday)
	thursdayStr := thursday.Format("2006-01-02")
	now := time.Now().UTC()
	require.NoError(t, db.CreateApprovedRecurringWFHRequest(ctx, memberID, thursdayStr, now))
	rows, err := db.GetWFHRequestsByMember(ctx, memberID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	_, err = db.ExecContext(ctx, "UPDATE wfh_requests SET status = ?, withdrawn_at = ? WHERE id = ?",
		database.WFHStatusWithdrawn, now, rows[0].ID)
	require.NoError(t, err)

	// Materializer must not re-insert the withdrawn row.
	start, end, err = svc.ComputePeriodBounds(now)
	require.NoError(t, err)
	inserted, err := svc.EnsureRecurringMaterializedForMember(ctx, memberID, start, end)
	require.NoError(t, err)
	assert.Equal(t, 0, inserted, "withdrawn row must suppress re-materialization")

	final, err := db.GetWFHRequestsByMember(ctx, memberID)
	require.NoError(t, err)
	require.Len(t, final, 1, "still exactly one row for Thursday")
	assert.Equal(t, database.WFHStatusWithdrawn, final[0].Status)
}

func TestEnsureRecurringMaterialized_AllMembers(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	require.NoError(t, db.SetTeamMemberRecurringWFHDays(ctx, aliceID, database.RecurringWFHDays{Wednesday: true}))
	// Bob has no recurring schedule.

	today := time.Now().UTC()
	start, end, err := svc.ComputePeriodBounds(today)
	require.NoError(t, err)

	inserted, err := svc.EnsureRecurringMaterialized(ctx, start, end)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, inserted, 1, "Alice's Wed should be materialized")

	aliceRows, err := db.GetWFHRequestsUsedInPeriod(ctx, aliceID,
		start.Format("2006-01-02"), end.Format("2006-01-02"))
	require.NoError(t, err)
	bobRows, err := db.GetWFHRequestsUsedInPeriod(ctx, bobID,
		start.Format("2006-01-02"), end.Format("2006-01-02"))
	require.NoError(t, err)
	assert.NotEmpty(t, aliceRows)
	assert.Empty(t, bobRows, "Bob has no recurring schedule, must not be materialized")
}
