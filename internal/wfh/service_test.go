package wfh

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/database/sqlc"
	"github.com/inful/madhatter/internal/notify"
	"github.com/inful/madhatter/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedAdminUser inserts a user row that the admin-marked WFH
// rows can reference via the marked_by foreign key. The seed
// inserts directly via SQL to set is_active=1 (CreateUser is the
// pending path that always sets is_active=0) and the user id is
// whatever the caller asks for. Returns the user id.
func seedAdminUser(t *testing.T, ctx context.Context, db *database.DB, id, name, email string) string {
	t.Helper()
	_, err := db.ExecContext(ctx, `INSERT INTO users (id, email, name, provider, provider_id, is_admin, is_active) VALUES (?, ?, ?, ?, ?, 1, 1)`,
		id, email, name, "test", id)
	require.NoError(t, err)
	return id
}

func setupWFHTestDB(t *testing.T) (*database.DB, func()) {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	dbPath := filepath.Join(t.TempDir(), "wfh.db")
	db, err := database.New(dbPath)
	require.NoError(t, err)

	cleanup := func() {
		_ = db.Close()
	}

	return db, cleanup
}

// testConfig returns the test fixture Config used by the bulk of
// the wfh-package tests. The PeriodDays is bumped to 14 (vs. the
// production default of 7) so quota-period math survives the
// Friday edge case: with a 7-day period ending on Saturday,
// Friday has no future weekday inside the period — every
// test that picks "next business day" as a future target date
// would land in the NEXT period (where today's existing WFH rows
// don't count toward the new period's quota math). 14-day
// periods keep today and the next business day reliably in the
// same period across every weekday.
func testConfig() Config {
	return Config{
		Enabled:             true,
		MinOnsitePercentage: 50,
		MinOnsiteAbsolute:   1,
		MaxDaysPerPeriod:    2,
		PeriodDays:          14,
		PeriodAnchor:        defaultPeriodAnchor,
		SettlementDays:      defaultSettlementDays,
		RequestHorizonDays:  defaultRequestHorizonDays,
		PurgeEnabled:        defaultPurgeEnabled,
	}
}

func TestLoadConfigFromEnv_DefaultsAndOverrides(t *testing.T) {
	cfg := LoadConfigFromEnv()
	assert.True(t, cfg.Enabled)
	assert.LessOrEqual(t, math.Abs(cfg.MinOnsitePercentage-defaultMinOnsitePercentage), 0.0001)
	assert.Equal(t, defaultMinOnsiteAbsolute, cfg.MinOnsiteAbsolute)
	assert.Equal(t, defaultMaxDaysPerPeriod, cfg.MaxDaysPerPeriod)
	assert.Equal(t, defaultPeriodDays, cfg.PeriodDays)
	assert.Equal(t, defaultPeriodAnchor, cfg.PeriodAnchor)
	assert.Equal(t, defaultSettlementDays, cfg.SettlementDays)
	assert.Equal(t, defaultRequestHorizonDays, cfg.RequestHorizonDays)
	assert.Equal(t, defaultPurgeEnabled, cfg.PurgeEnabled)
	// Seat-cap picker knobs (Phase 2 of
	// plans/assigned-wfh-plan.md) default to "picker is a
	// no-op" so existing deployments see no behavior change
	// until an operator explicitly opts in.
	assert.Equal(t, 0, cfg.SeatCap)
	assert.True(t, cfg.AssignmentEnabled)
	assert.True(t, cfg.CoPresenceEnabled)
	assert.Equal(t, defaultCoPresenceHorizonDays, cfg.CoPresenceHorizonDays)
	assert.Equal(t, defaultCoPresenceRetentionDays, cfg.CoPresenceRetentionDays)

	t.Setenv("WFH_ENABLED", "false")
	t.Setenv("WFH_MIN_ONSITE_PERCENTAGE", "60.5")
	t.Setenv("WFH_MIN_ONSITE_ABSOLUTE", "3")
	t.Setenv("WFH_MAX_DAYS_PER_PERIOD", "4")
	t.Setenv("WFH_PERIOD_DAYS", "14")
	t.Setenv("WFH_PERIOD_ANCHOR", "2026-02-02")
	t.Setenv("WFH_SETTLEMENT_DAYS", "5")
	t.Setenv("WFH_REQUEST_HORIZON_DAYS", "180")
	t.Setenv("WFH_PURGE_ENABLED", "false")
	t.Setenv("WFH_SEAT_CAP", "5")
	t.Setenv("WFH_ASSIGNMENT_ENABLED", "false")
	t.Setenv("WFH_COPRESENCE_ENABLED", "false")
	t.Setenv("WFH_COPRESENCE_HORIZON_DAYS", "21")
	t.Setenv("WFH_COPRESENCE_RETENTION_DAYS", "60")

	cfg = LoadConfigFromEnv()
	assert.False(t, cfg.Enabled)
	assert.LessOrEqual(t, math.Abs(cfg.MinOnsitePercentage-60.5), 0.0001)
	assert.Equal(t, 3, cfg.MinOnsiteAbsolute)
	assert.Equal(t, 4, cfg.MaxDaysPerPeriod)
	assert.Equal(t, 14, cfg.PeriodDays)
	assert.Equal(t, "2026-02-02", cfg.PeriodAnchor)
	assert.Equal(t, 5, cfg.SettlementDays)
	assert.Equal(t, 180, cfg.RequestHorizonDays)
	assert.False(t, cfg.PurgeEnabled)
	assert.Equal(t, 5, cfg.SeatCap)
	assert.False(t, cfg.AssignmentEnabled)
	assert.False(t, cfg.CoPresenceEnabled)
	assert.Equal(t, 21, cfg.CoPresenceHorizonDays)
	assert.Equal(t, 60, cfg.CoPresenceRetentionDays)

	t.Setenv("WFH_ENABLED", "not-a-bool")
	t.Setenv("WFH_MIN_ONSITE_PERCENTAGE", "bad")
	t.Setenv("WFH_MIN_ONSITE_ABSOLUTE", "bad")
	t.Setenv("WFH_REQUEST_HORIZON_DAYS", "bad")
	t.Setenv("WFH_PURGE_ENABLED", "not-a-bool")
	t.Setenv("WFH_SEAT_CAP", "bad")
	t.Setenv("WFH_ASSIGNMENT_ENABLED", "not-a-bool")
	t.Setenv("WFH_COPRESENCE_ENABLED", "not-a-bool")
	t.Setenv("WFH_COPRESENCE_HORIZON_DAYS", "bad")
	t.Setenv("WFH_COPRESENCE_RETENTION_DAYS", "bad")
	cfg = LoadConfigFromEnv()
	assert.True(t, cfg.Enabled)
	assert.LessOrEqual(t, math.Abs(cfg.MinOnsitePercentage-defaultMinOnsitePercentage), 0.0001)
	assert.Equal(t, defaultMinOnsiteAbsolute, cfg.MinOnsiteAbsolute)
	assert.Equal(t, defaultRequestHorizonDays, cfg.RequestHorizonDays)
	assert.True(t, cfg.PurgeEnabled, "unparseable bool must fall back to the default")
	assert.Equal(t, 0, cfg.SeatCap, "unparseable SeatCap must fall back to default (0)")
	assert.True(t, cfg.AssignmentEnabled)
	assert.True(t, cfg.CoPresenceEnabled)
	assert.Equal(t, defaultCoPresenceHorizonDays, cfg.CoPresenceHorizonDays)
	assert.Equal(t, defaultCoPresenceRetentionDays, cfg.CoPresenceRetentionDays)
}

// TestConfigValidate_RetentionMustBeAtLeastHorizon pins the
// boot-time fail-fast in Config.Validate. The picker (Phase 2 of
// plans/assigned-wfh-plan.md) reads rows up to horizon days
// back; if retention is shorter, those rows have been pruned
// and every candidate scores the history-clamp sentinel. The
// operator sees a picker that "works" but always picks the
// same members — silently broken. Validate catches this at
// boot.
func TestConfigValidate_RetentionMustBeAtLeastHorizon(t *testing.T) {
	t.Run("valid: retention > horizon", func(t *testing.T) {
		cfg := Config{CoPresenceHorizonDays: 14, CoPresenceRetentionDays: 30}
		require.NoError(t, cfg.Validate())
	})
	t.Run("valid: retention == horizon (edge)", func(t *testing.T) {
		cfg := Config{CoPresenceHorizonDays: 14, CoPresenceRetentionDays: 14}
		require.NoError(t, cfg.Validate())
	})
	t.Run("invalid: retention < horizon", func(t *testing.T) {
		cfg := Config{CoPresenceHorizonDays: 14, CoPresenceRetentionDays: 7}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "CoPresenceRetentionDays")
		assert.Contains(t, err.Error(), "CoPresenceHorizonDays")
	})
}

func TestComputePeriodBounds_AcrossAnchorBoundaries(t *testing.T) {
	svc := NewService(nil, testConfig())

	// Anchor is 2026-01-05 (Monday); PeriodDays is 14. So the first
	// period covers 2026-01-05 .. 2026-01-18, and the period
	// immediately before it covers 2025-12-22 .. 2026-01-03. The
	// dates 2026-01-07 (inside the first period) and 2026-01-04
	// (inside the previous period) pin the boundary arithmetic.
	start, end, err := svc.ComputePeriodBounds(time.Date(2026, time.January, 7, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Equal(t, "2026-01-05", start.Format("2006-01-02"))
	assert.Equal(t, "2026-01-18", end.Format("2006-01-02"))

	start, end, err = svc.ComputePeriodBounds(time.Date(2026, time.January, 4, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Equal(t, "2025-12-22", start.Format("2006-01-02"))
	assert.Equal(t, "2026-01-04", end.Format("2006-01-02"))
}

func TestCheckQuota_UsesCurrentPeriodLimit(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	today := testutil.NextBusinessDay(time.Now().UTC())
	date1 := today.Format("2006-01-02")
	date2 := today.AddDate(0, 0, 1).Format("2006-01-02")
	date3 := today.AddDate(0, 0, 2).Format("2006-01-02")

	_, err = db.CreateWFHRequest(ctx, memberID, date1)
	require.NoError(t, err)
	_, err = db.CreateWFHRequest(ctx, memberID, date2)
	require.NoError(t, err)

	hasQuota, err := svc.CheckQuota(ctx, memberID, date3)
	require.NoError(t, err)
	assert.False(t, hasQuota)
}

// TestGetQuotaStatus_DateAffinityBoundaryFix is the regression
// test for the date-comparison bug surfaced via the WFH
// request form: rows stored with YYYY-MM-DD TEXT (e.g. via the
// sqlite3 CLI, or via the legacy column-typed sqlc binding)
// used to fall out of `date >= ?` / `date <= ?` / `date = ?`
// queries when the parameter was a Go time.Time, because the
// ncruces driver encodes time.Time as RFC3339 ("YYYY-MM-DDTHH:MM:SSZ")
// and the DATE column stored the literal "YYYY-MM-DD" (10 chars).
// Lexicographic comparison fails. The fix uses julianday() on
// both sides so the comparison is numeric.
//
// Two rows on consecutive business days are enough: the lower
// row (== periodStart) used to be silently excluded from the
// range query. After the fix it counts.
func TestGetQuotaStatus_DateAffinityBoundaryFix(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	svc := NewService(db, testConfig())

	// Burn MaxDaysPerPeriod voluntary WFHs at consecutive
	// business days inside the current period, starting on the
	// period start itself (the boundary row the old comparison
	// The request-date validator rejects past dates
	// and weekends, so skip past dates and Sat/Sun.
	maxDays := svc.Config().MaxDaysPerPeriod
	require.GreaterOrEqual(t, maxDays, 2,
		"this test needs at least 2 days to burn so the off-by-one matters")

	now := time.Now().UTC()
	periodStart, _, perr := svc.ComputePeriodBounds(now)
	require.NoError(t, perr)

	cursor := periodStart
	inserted := 0
	for inserted < maxDays {
		if cursor.Before(now) ||
			cursor.Weekday() == time.Saturday ||
			cursor.Weekday() == time.Sunday {
			cursor = cursor.AddDate(0, 0, 1)
			continue
		}
		req, cErr := db.CreateWFHRequest(ctx, memberID, cursor.Format("2006-01-02"))
		require.NoError(t, cErr)
		require.NoError(t, db.UpdateWFHRequestStatus(ctx, req.ID, database.WFHStatusApproved))
		inserted++
		cursor = cursor.AddDate(0, 0, 1)
	}

	require.GreaterOrEqual(t, inserted, maxDays,
		"need to have burned at least %d days to assert the boundary fix; got %d (periodStart=%s, now=%s, periodDays=%d)",
		maxDays, inserted, periodStart.Format("2006-01-02"), now.Format("2006-01-02"), testConfig().PeriodDays)

	// The quota lookup runs through GetWFHRequestsVoluntaryInPeriod.
	// Before the fix this returned len(requests)-1 because the
	// first business day (periodStart) was excluded by the broken
	// date comparison. After the fix it returns the full count.
	status, err := svc.GetQuotaStatus(ctx, memberID)
	require.NoError(t, err)
	assert.Equal(t, maxDays, status.Used,
		"quota used must count every approved voluntary WFH in the period. Before the fix this was maxDays-1 because the boundary row was excluded by the broken date comparison.")
	assert.Equal(t, 0, status.Remaining,
		"after burning maxDays in the period, remaining must be 0 (button correctly disables)")
}

func TestService_MaxRequestDate(t *testing.T) {
	svc := NewService(nil, Config{RequestHorizonDays: 90})
	maxDate := svc.MaxRequestDate()
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	expected := today.AddDate(0, 0, 90)
	assert.Equal(t, expected, maxDate)
}

func TestService_ValidateRequestDate(t *testing.T) {
	svc := NewService(nil, Config{RequestHorizonDays: 90})
	maxDate := svc.MaxRequestDate()

	// Within horizon — today.
	within := maxDate.Format("2006-01-02")
	err := svc.ValidateRequestDate(within)
	require.NoError(t, err)

	// Within horizon — a few days before max.
	within = maxDate.AddDate(0, 0, -5).Format("2006-01-02")
	err = svc.ValidateRequestDate(within)
	require.NoError(t, err)

	// Beyond horizon.
	beyond := maxDate.AddDate(0, 0, 1).Format("2006-01-02")
	err = svc.ValidateRequestDate(beyond)
	require.ErrorIs(t, err, database.ErrWFHDateTooFar)

	// Invalid date.
	err = svc.ValidateRequestDate("not-a-date")
	require.ErrorIs(t, err, database.ErrWFHInvalidDate)
}

func TestCheckQuota_FarFutureDate_ComputesPeriodForThatDate(t *testing.T) {
	// Regression test: a date 60 days out falls in a future period that does
	// not yet exist as "current". The period bounds must be computed correctly
	// and the quota check must succeed when no days are used in that period.
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	farFuture := testutil.NextBusinessDay(time.Now().UTC().AddDate(0, 0, 60))
	farFutureStr := farFuture.Format("2006-01-02")

	hasQuota, err := svc.CheckQuota(ctx, memberID, farFutureStr)
	require.NoError(t, err, "CheckQuota must not fail for a far-future date within horizon")
	assert.True(t, hasQuota, "Fresh member should have quota available for a far-future period")
}

func TestCheckQuota_RecurringDaysReduceBudget(t *testing.T) {
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

	// Pick a check date inside a window that — regardless of which
	// day-of-week the test happens to run — is guaranteed to contain
	// at least one of the recurring days after materialization.
	// We march forward a fixed 30 days and use the next Wednesday as
	// the check date. With weekly periods and the WFH service
	// materializing Wed+Thu, the Wednesday we pick is guaranteed to be
	// in the same period as the following Thursday (Thursday is
	// always 6 days after Wednesday in the same period).
	today := time.Now().UTC()
	thirtyDaysOut := today.AddDate(0, 0, 30)
	checkDate := nextWeekdayInRange(t, today, thirtyDaysOut, time.Wednesday).Format("2006-01-02")

	// Materialize a window that includes the check date and the next
	// Thursday (one day after Wednesday in the same period — at most
	// 6 days forward).
	checkTime, _ := time.Parse("2006-01-02", checkDate)
	windowEnd := checkTime.AddDate(0, 0, 7) // safely into the next period too
	_, err = svc.EnsureRecurringMaterializedForMember(ctx, memberID, checkTime, windowEnd)
	require.NoError(t, err)

	// Without a query against the pre-materialization state, the
	// recurring rows consume at least 1 day of quota here. With
	// MaxDaysPerPeriod=2, spending 2 days on Wed+Thu means quota
	// is fully exhausted for the period.
	hasQuota, err := svc.CheckQuota(ctx, memberID, checkDate)
	require.NoError(t, err)
	assert.False(t, hasQuota, "Both Wed and Thu recurring rows in the same period must exhaust MaxDaysPerPeriod=2")
}

func TestPrioritisePending_SortsByUsageThenCreatedAt(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	baseDate := testutil.NextBusinessDay(time.Now().UTC())
	date := testutil.NextBusinessDay(baseDate.AddDate(0, 0, 1))
	dateStr := date.Format("2006-01-02")
	previousDateStr := baseDate.Format("2006-01-02")

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	carolID, err := db.AddTeamMember(ctx, "Carol", "carol@example.com")
	require.NoError(t, err)

	carolPrior, err := db.CreateWFHRequest(ctx, carolID, previousDateStr)
	require.NoError(t, err)
	require.NoError(t, db.UpdateWFHRequestStatus(ctx, carolPrior.ID, database.WFHStatusApproved))

	aliceReq, err := db.CreateWFHRequest(ctx, aliceID, dateStr)
	require.NoError(t, err)
	bobReq, err := db.CreateWFHRequest(ctx, bobID, dateStr)
	require.NoError(t, err)
	carolReq, err := db.CreateWFHRequest(ctx, carolID, dateStr)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, "UPDATE wfh_requests SET created_at = ? WHERE id = ?", "2026-01-01 09:00:00", aliceReq.ID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "UPDATE wfh_requests SET created_at = ? WHERE id = ?", "2026-01-01 10:00:00", bobReq.ID)
	require.NoError(t, err)

	pending, err := db.GetPendingForSettlement(ctx, dateStr)
	require.NoError(t, err)
	ordered, err := svc.prioritisePending(ctx, dateStr, pending)
	require.NoError(t, err)
	require.Len(t, ordered, 3)

	assert.Equal(t, aliceReq.ID, ordered[0].ID)
	assert.Equal(t, bobReq.ID, ordered[1].ID)
	assert.Equal(t, carolReq.ID, ordered[2].ID)
}

func TestSettlePendingRequests_ApprovesHighestPriorityWithinSlots(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := testConfig()
	cfg.MinOnsitePercentage = 50
	cfg.MinOnsiteAbsolute = 1
	svc := NewService(db, cfg)

	today := testutil.NextBusinessDay(time.Now().UTC())
	targetDate := testutil.NextBusinessDay(today.AddDate(0, 0, 1))
	targetDateStr := targetDate.Format("2006-01-02")
	todayStr := today.Format("2006-01-02")

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	carolID, err := db.AddTeamMember(ctx, "Carol", "carol@example.com")
	require.NoError(t, err)
	daveID, err := db.AddTeamMember(ctx, "Dave", "dave@example.com")
	require.NoError(t, err)

	_, err = db.CreateLeaveRecord(ctx, daveID, targetDateStr, targetDateStr, database.LeaveTypeLeave)
	require.NoError(t, err)

	bobUsed, err := db.CreateWFHRequest(ctx, bobID, todayStr)
	require.NoError(t, err)
	require.NoError(t, db.UpdateWFHRequestStatus(ctx, bobUsed.ID, database.WFHStatusApproved))

	bobPending, err := db.CreateWFHRequest(ctx, bobID, targetDateStr)
	require.NoError(t, err)
	carolPending, err := db.CreateWFHRequest(ctx, carolID, targetDateStr)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "UPDATE wfh_requests SET created_at = ? WHERE id = ?", "2026-01-01 08:00:00", bobPending.ID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "UPDATE wfh_requests SET created_at = ? WHERE id = ?", "2026-01-01 09:00:00", carolPending.ID)
	require.NoError(t, err)

	require.NoError(t, svc.SettlePendingRequests(ctx))

	bobReq, err := db.GetWFHRequestByID(ctx, bobPending.ID)
	require.NoError(t, err)
	carolReq, err := db.GetWFHRequestByID(ctx, carolPending.ID)
	require.NoError(t, err)

	assert.Equal(t, database.WFHStatusDenied, bobReq.Status)
	assert.Equal(t, database.WFHStatusApproved, carolReq.Status)
	assert.NotNil(t, bobReq.SettledAt)
	assert.NotNil(t, carolReq.SettledAt)

	_ = aliceID
}

func TestSettlePendingRequests_RecurringWFHConsumesRemoteCapacity(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := testConfig()
	cfg.MinOnsitePercentage = 50
	cfg.MinOnsiteAbsolute = 1
	svc := NewService(db, cfg)

	targetDate := testutil.NextBusinessDay(time.Now().UTC().AddDate(0, 0, 1))
	targetDateStr := targetDate.Format("2006-01-02")

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	carolID, err := db.AddTeamMember(ctx, "Carol", "carol@example.com")
	require.NoError(t, err)
	daveID, err := db.AddTeamMember(ctx, "Dave", "dave@example.com")
	require.NoError(t, err)

	require.NoError(t, db.SetTeamMemberRecurringWFHDays(ctx, daveID, database.RecurringWFHDays{Monday: true, Tuesday: true, Wednesday: true, Thursday: true, Friday: true}))

	bobPending, err := db.CreateWFHRequest(ctx, bobID, targetDateStr)
	require.NoError(t, err)
	carolPending, err := db.CreateWFHRequest(ctx, carolID, targetDateStr)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, "UPDATE wfh_requests SET created_at = ? WHERE id = ?", "2026-01-01 08:00:00", bobPending.ID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "UPDATE wfh_requests SET created_at = ? WHERE id = ?", "2026-01-01 09:00:00", carolPending.ID)
	require.NoError(t, err)

	require.NoError(t, svc.SettlePendingRequests(ctx))

	bobReq, err := db.GetWFHRequestByID(ctx, bobPending.ID)
	require.NoError(t, err)
	carolReq, err := db.GetWFHRequestByID(ctx, carolPending.ID)
	require.NoError(t, err)

	assert.Equal(t, database.WFHStatusApproved, bobReq.Status)
	assert.Equal(t, database.WFHStatusDenied, carolReq.Status)
	assert.NotNil(t, bobReq.SettledAt)
	assert.NotNil(t, carolReq.SettledAt)

	_ = aliceID
}

func TestSettlePendingRequests_NoDoubleCountForApprovedRecurringWFH(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := testConfig()
	cfg.MinOnsitePercentage = 50
	cfg.MinOnsiteAbsolute = 1
	svc := NewService(db, cfg)

	today := testutil.NextBusinessDay(time.Now().UTC())
	targetDate := testutil.NextBusinessDay(today.AddDate(0, 0, 1))
	targetDateStr := targetDate.Format("2006-01-02")

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	carolID, err := db.AddTeamMember(ctx, "Carol", "carol@example.com")
	require.NoError(t, err)
	daveID, err := db.AddTeamMember(ctx, "Dave", "dave@example.com")
	require.NoError(t, err)

	require.NoError(t, db.SetTeamMemberRecurringWFHDays(ctx, daveID, database.RecurringWFHDays{Monday: true, Tuesday: true, Wednesday: true, Thursday: true, Friday: true}))

	// Seed approved request for permanent-WFH member (legacy/existing data case) and ensure no double counting.
	daveApproved, err := db.ExecContext(
		ctx,
		"INSERT INTO wfh_requests (id, member_id, date, status) VALUES (?, ?, ?, ?)",
		"dave-approved-1", daveID, targetDateStr, database.WFHStatusApproved,
	)
	require.NoError(t, err)
	rows, err := daveApproved.RowsAffected()
	require.NoError(t, err)
	require.EqualValues(t, 1, rows)

	bobPending, err := db.CreateWFHRequest(ctx, bobID, targetDateStr)
	require.NoError(t, err)
	carolPending, err := db.CreateWFHRequest(ctx, carolID, targetDateStr)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, "UPDATE wfh_requests SET created_at = ? WHERE id = ?", "2026-01-01 08:00:00", bobPending.ID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "UPDATE wfh_requests SET created_at = ? WHERE id = ?", "2026-01-01 09:00:00", carolPending.ID)
	require.NoError(t, err)

	require.NoError(t, svc.SettlePendingRequests(ctx))

	bobReq, err := db.GetWFHRequestByID(ctx, bobPending.ID)
	require.NoError(t, err)
	carolReq, err := db.GetWFHRequestByID(ctx, carolPending.ID)
	require.NoError(t, err)

	// Exactly one slot remains in this setup, so first-priority request is approved.
	assert.Equal(t, database.WFHStatusApproved, bobReq.Status)
	assert.Equal(t, database.WFHStatusDenied, carolReq.Status)
}

func TestCreateWFHRequest_RejectsHoliday(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	t.Cleanup(cleanup)

	holidayDate := testutil.NextBusinessDay(time.Now().UTC().AddDate(0, 0, 5))
	db.SetHolidayChecker(func(d time.Time) bool {
		return d.Format("2006-01-02") == holidayDate.Format("2006-01-02")
	})

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	_, err = db.CreateWFHRequest(ctx, memberID, holidayDate.Format("2006-01-02"))
	require.ErrorIs(t, err, database.ErrWFHOnHoliday)
}

func TestCreateWFHRequest_AllowsNonHolidayWhenCheckerSet(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	t.Cleanup(cleanup)

	holidayDate := testutil.NextBusinessDay(time.Now().UTC().AddDate(0, 0, 5))
	db.SetHolidayChecker(func(d time.Time) bool {
		return d.Format("2006-01-02") == holidayDate.Format("2006-01-02")
	})

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	nonHoliday := testutil.NextBusinessDay(holidayDate.AddDate(0, 0, 1))
	_, err = db.CreateWFHRequest(ctx, memberID, nonHoliday.Format("2006-01-02"))
	assert.NoError(t, err)
}

func TestCheckQuota_RejectsHoliday(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	t.Cleanup(cleanup)

	holidayDate := testutil.NextBusinessDay(time.Now().UTC().AddDate(0, 0, 5))
	db.SetHolidayChecker(func(d time.Time) bool {
		return d.Format("2006-01-02") == holidayDate.Format("2006-01-02")
	})

	svc := NewService(db, testConfig())
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	hasQuota, err := svc.CheckQuota(ctx, memberID, holidayDate.Format("2006-01-02"))
	require.ErrorIs(t, err, database.ErrWFHOnHoliday)
	assert.False(t, hasQuota)
}

// recordingNotifier is a notify.Notifier that records every
// WFHStateChanged call. Used to assert settlement fires notifications
// for each settled request.
type recordingNotifier struct {
	mu     sync.Mutex
	events []notify.WFHEvent
}

func (r *recordingNotifier) WFHStateChanged(_ context.Context, e notify.WFHEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}
func (r *recordingNotifier) SwapRequested(_ context.Context, _ notify.SwapEvent)  {}
func (r *recordingNotifier) SwapAccepted(_ context.Context, _ notify.SwapEvent)   {}
func (r *recordingNotifier) SwapRejected(_ context.Context, _ notify.SwapEvent)   {}
func (r *recordingNotifier) SwapCancelled(_ context.Context, _ notify.SwapEvent)  {}
func (r *recordingNotifier) CoverAssigned(_ context.Context, _ notify.CoverEvent) {}
func (r *recordingNotifier) UserPendingApproval(_ context.Context, _ notify.UserPendingApprovalEvent) {
}

func TestSettlePendingRequests_FiresNotifierForEachTransition(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := testConfig()
	cfg.MinOnsitePercentage = 50
	cfg.MinOnsiteAbsolute = 1
	svc := NewService(db, cfg)

	today := testutil.NextBusinessDay(time.Now().UTC())
	targetDate := testutil.NextBusinessDay(today.AddDate(0, 0, 1))
	targetDateStr := targetDate.Format("2006-01-02")
	todayStr := today.Format("2006-01-02")

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	carolID, err := db.AddTeamMember(ctx, "Carol", "carol@example.com")
	require.NoError(t, err)
	daveID, err := db.AddTeamMember(ctx, "Dave", "dave@example.com")
	require.NoError(t, err)

	// Dave on leave so on-site capacity is reduced.
	_, err = db.CreateLeaveRecord(ctx, daveID, targetDateStr, targetDateStr, database.LeaveTypeLeave)
	require.NoError(t, err)

	// Bob's earlier WFH today reduces available slots for tomorrow.
	bobUsed, err := db.CreateWFHRequest(ctx, bobID, todayStr)
	require.NoError(t, err)
	require.NoError(t, db.UpdateWFHRequestStatus(ctx, bobUsed.ID, database.WFHStatusApproved))

	// Bob and Carol request WFH for tomorrow; only one will be approved.
	bobPending, err := db.CreateWFHRequest(ctx, bobID, targetDateStr)
	require.NoError(t, err)
	carolPending, err := db.CreateWFHRequest(ctx, carolID, targetDateStr)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "UPDATE wfh_requests SET created_at = ? WHERE id = ?", "2026-01-01 08:00:00", bobPending.ID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "UPDATE wfh_requests SET created_at = ? WHERE id = ?", "2026-01-01 09:00:00", carolPending.ID)
	require.NoError(t, err)

	notifier := &recordingNotifier{}
	svc.SetNotifier(notifier)

	require.NoError(t, svc.SettlePendingRequests(ctx))

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	require.Len(t, notifier.events, 2)

	statuses := []string{notifier.events[0].NewStatus, notifier.events[1].NewStatus}
	assert.ElementsMatch(t, []string{database.WFHStatusApproved, database.WFHStatusDenied}, statuses)

	actors := []string{notifier.events[0].ActorName, notifier.events[1].ActorName}
	for _, a := range actors {
		assert.Equal(t, "system", a)
	}
}

// seedPastPeriodRows inserts a wfh_requests row directly via the SQLC
// layer to bypass CreateWFHRequest's past-date guard. Used by the
// past-period purge tests to seed rows in the strictly-past band.
func seedPastPeriodRows(t *testing.T, ctx context.Context, db *database.DB, memberID, date string) {
	t.Helper()
	id := "seed-" + date + "-" + memberID
	_, err := db.GetQueries().CreateWFHRequest(ctx, sqlc.CreateWFHRequestParams{
		ID:       id,
		MemberID: memberID,
		Date:     parsePastDate(t, date),
	})
	require.NoError(t, err)
}

// parsePastDate is the service-test counterpart of the database package's
// parseDate helper. Lives here to avoid exporting internal test helpers.
func parsePastDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	require.NoError(t, err)
	return d
}

// TestService_PurgePastPeriods verifies the cutoff math, the row
// deletion, and the disabled-feature short-circuit. Uses the default
// 7-day period and 2026-01-05 anchor so the expected cutoff is
// deterministic regardless of the calendar date the test runs on.
func TestService_PurgePastPeriods(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	require.True(t, svc.IsPurgeEnabled(), "precondition: default config enables purge")

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Anchor is 2026-01-05 (Monday), PeriodDays=7. The current period
	// bounds are computed against today's date — we'll assert against
	// the cutoff returned by the call rather than hard-coding it.
	today := time.Now().UTC()
	currentStart, _, err := svc.ComputePeriodBounds(today)
	require.NoError(t, err)
	expectedCutoff := currentStart.AddDate(0, 0, -svc.Config().PeriodDays)

	// Seed: two rows strictly before the cutoff, one at the cutoff
	// itself, one inside the current period.
	beforeCutoff1 := expectedCutoff.AddDate(0, 0, -10).Format("2006-01-02")
	beforeCutoff2 := expectedCutoff.AddDate(0, 0, -1).Format("2006-01-02")
	atCutoff := expectedCutoff.Format("2006-01-02")
	inCurrent := currentStart.AddDate(0, 0, 1).Format("2006-01-02")

	seedPastPeriodRows(t, ctx, db, memberID, beforeCutoff1)
	seedPastPeriodRows(t, ctx, db, memberID, beforeCutoff2)
	seedPastPeriodRows(t, ctx, db, memberID, atCutoff)
	seedPastPeriodRows(t, ctx, db, memberID, inCurrent)

	cutoff, deleted, err := svc.PurgePastPeriods(ctx)
	require.NoError(t, err)
	assert.Equal(t, expectedCutoff.Format("2006-01-02"), cutoff)
	assert.Equal(t, int64(2), deleted, "two rows before cutoff must be deleted")

	// Survivors: cutoff and current-period rows still readable.
	allRows, err := db.GetWFHRequestsByMember(ctx, memberID)
	require.NoError(t, err)
	require.Len(t, allRows, 2, "exactly the cutoff and current-period rows must survive")
	dates := []string{allRows[0].Date, allRows[1].Date}
	assert.ElementsMatch(t, []string{atCutoff, inCurrent}, dates)
}

func TestService_PurgePastPeriods_DryRunDoesNotDelete(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Seed rows across periods so the dry-run has something to count.
	today := time.Now().UTC()
	currentStart, _, err := svc.ComputePeriodBounds(today)
	require.NoError(t, err)
	cutoff := currentStart.AddDate(0, 0, -svc.Config().PeriodDays)

	seedPastPeriodRows(t, ctx, db, memberID, cutoff.AddDate(0, 0, -5).Format("2006-01-02"))
	seedPastPeriodRows(t, ctx, db, memberID, cutoff.AddDate(0, 0, -1).Format("2006-01-02"))
	seedPastPeriodRows(t, ctx, db, memberID, currentStart.Format("2006-01-02"))

	gotCutoff, wouldDelete, err := svc.PurgePastPeriodsDryRun(ctx)
	require.NoError(t, err)
	assert.Equal(t, cutoff.Format("2006-01-02"), gotCutoff)
	assert.Equal(t, int64(2), wouldDelete, "two past-period rows would be deleted")

	// All three rows must still exist after dry-run.
	rows, err := db.GetWFHRequestsByMember(ctx, memberID)
	require.NoError(t, err)
	assert.Len(t, rows, 3, "dry-run must not modify the table")
}

func TestService_PurgePastPeriods_PurgeFlagOff(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := testConfig()
	cfg.PurgeEnabled = false
	svc := NewService(db, cfg)

	assert.False(t, svc.IsPurgeEnabled(), "purge flag off must disable purge even when WFH is on")

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	today := time.Now().UTC()
	currentStart, _, err := svc.ComputePeriodBounds(today)
	require.NoError(t, err)
	cutoff := currentStart.AddDate(0, 0, -svc.Config().PeriodDays)
	seedPastPeriodRows(t, ctx, db, memberID, cutoff.AddDate(0, 0, -5).Format("2006-01-02"))

	cutoffStr, deleted, err := svc.PurgePastPeriods(ctx)
	require.NoError(t, err)
	assert.Empty(t, cutoffStr)
	assert.Equal(t, int64(0), deleted)

	dryCutoff, wouldDelete, err := svc.PurgePastPeriodsDryRun(ctx)
	require.NoError(t, err)
	assert.Empty(t, dryCutoff)
	assert.Equal(t, int64(0), wouldDelete)

	rows, err := db.GetWFHRequestsByMember(ctx, memberID)
	require.NoError(t, err)
	assert.Len(t, rows, 1, "no rows must be touched when purge is disabled")
}

func TestService_PurgePastPeriods_WFHDisabled(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := testConfig()
	cfg.Enabled = false
	// PurgeEnabled left true — IsPurgeEnabled must still report false
	// because the feature itself is off.
	svc := NewService(db, cfg)

	assert.False(t, svc.IsPurgeEnabled(), "feature off must disable purge regardless of PurgeEnabled")

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	today := time.Now().UTC()
	currentStart, _, err := svc.ComputePeriodBounds(today)
	require.NoError(t, err)
	cutoff := currentStart.AddDate(0, 0, -svc.Config().PeriodDays)
	seedPastPeriodRows(t, ctx, db, memberID, cutoff.AddDate(0, 0, -5).Format("2006-01-02"))

	_, deleted, err := svc.PurgePastPeriods(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted)

	rows, err := db.GetWFHRequestsByMember(ctx, memberID)
	require.NoError(t, err)
	assert.Len(t, rows, 1, "WFH-disabled service must not purge")
}

// TestLoadConfigFromEnv_DefaultSettlementWindowCoversAPeriod locks
// the contract that the default SettlementDays is at least one
// full quota period. The previous default of 2 left a five-day
// gap in coverage — a request submitted on Thursday for the
// following Monday would stay pending over the weekend, then be
// settled only after the Saturday scheduler tick crossed the
// 2-day horizon. With PeriodDays=7, the default SettlementDays
// must be at least 7 to cover the typical "plan next week" workflow.
func TestLoadConfigFromEnv_DefaultSettlementWindowCoversAPeriod(t *testing.T) {
	// Make sure no env override is in play.
	t.Setenv("WFH_SETTLEMENT_DAYS", "")

	cfg := LoadConfigFromEnv()
	assert.GreaterOrEqual(t, cfg.SettlementDays, cfg.PeriodDays,
		"the default settlement window (%d days) must cover at least one full period (%d days)",
		cfg.SettlementDays, cfg.PeriodDays)
}

// TestLoadConfigFromEnv_DefaultSettlementIntervalIsSubHour locks the
// contract that the default settlement scheduler tick is faster
// than once per hour. A 24-hour tick is fine for back-office
// processing but means a request submitted at 4:55pm waits until
// the next tick at midnight, leaving the user looking at a
// pending status for hours. Sub-hour ticks keep the perceived
// latency under typical request windows.
func TestLoadConfigFromEnv_DefaultSettlementIntervalIsSubHour(t *testing.T) {
	t.Setenv("WFH_SETTLEMENT_INTERVAL", "")

	interval := defaultSettlementIntervalFromEnv()
	assert.Less(t, interval, time.Hour,
		"the default settlement interval (%s) must be sub-hour", interval)
}

// TestSettlePendingRequests_CoversNextWeekMonday reproduces the
// user-reported scenario: a member submits a WFH request for the
// Monday of the next period (today+5 in the default 7-day
// configuration). The settlement must approve or deny the request
// in a single tick, not leave it pending for the weekend.
func TestSettlePendingRequests_CoversNextWeekMonday(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	// Use the production defaults so the test catches regressions
	// if someone narrows the window again.
	t.Setenv("WFH_SETTLEMENT_DAYS", "")
	t.Setenv("WFH_MIN_ONSITE_PERCENTAGE", "0")
	t.Setenv("WFH_MIN_ONSITE_ABSOLUTE", "0")
	svc := NewService(db, LoadConfigFromEnv())

	// Sanity: the contract this test is locking in.
	require.GreaterOrEqual(t, svc.Config().SettlementDays, 5,
		"this test depends on the settlement window being at least 5 days")

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// today + 5 business days. testutil.NextBusinessDay handles the
	// weekend-skip so the request lands on a workday.
	today := time.Now().UTC()
	date := testutil.NextBusinessDay(today.AddDate(0, 0, 5)).Format("2006-01-02")

	_, err = db.CreateWFHRequest(ctx, memberID, date)
	require.NoError(t, err)

	require.NoError(t, svc.SettlePendingRequests(ctx))

	reqs, err := db.GetWFHRequestsByMember(ctx, memberID)
	require.NoError(t, err)
	require.Len(t, reqs, 1, "exactly one request should exist")
	assert.NotEqual(t, database.WFHStatusPending, reqs[0].Status,
		"a request %s in the future must not be stuck in pending after one settle tick", date)
}

// TestSchedulerInterval_OverrideFromEnv locks in the env override
// for the scheduler interval. The default is sub-hour; ops can
// lower it (1m) or raise it (4h) via WFH_SETTLEMENT_INTERVAL.
func TestSchedulerInterval_OverrideFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want time.Duration
	}{
		{"default sub-hour", "", 15 * time.Minute},
		{"one minute", "1m", time.Minute},
		{"two hours", "2h", 2 * time.Hour},
		{"half hour", "30m", 30 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv("WFH_SETTLEMENT_INTERVAL", tc.env)
			} else {
				t.Setenv("WFH_SETTLEMENT_INTERVAL", "")
			}
			got := defaultSettlementIntervalFromEnv()
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestReportToday_HappyPath_Approves is the regression test for the
// "WFH today" feature: when there's room at the on-site floor, the
// request is created AND synchronously settled to approved in the
// same call. Returning the settled row means the caller (web/API/CLI)
// can surface the outcome immediately without a separate query.
func TestReportToday_HappyPath_Approves(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := testConfig()
	svc := NewService(db, cfg)

	today := testutil.NextBusinessDay(time.Now().UTC())
	todayStr := today.Format("2006-01-02")

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	got, err := svc.ReportToday(ctx, aliceID)
	require.NoError(t, err)
	assert.Equal(t, database.WFHStatusApproved, got.Status,
		"with capacity available, ReportToday must settle the row to approved inline")
	assert.Equal(t, todayStr, got.Date)
	assert.Equal(t, aliceID, got.MemberID)
	assert.NotEmpty(t, got.ID)
}

// TestReportToday_AtFloor_Denies pins the policy decision: same-day
// WFH reports respect the capacity floor. If the at-work count has
// reached the floor already, the request is created but settled to
// denied — the dashboard reads it as On-site (no approved row) and
// the user sees a flash banner.
func TestReportToday_AtFloor_Denies(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := testConfig()
	cfg.MinOnsiteAbsolute = 1 // tiny team → any extra WFH tips the floor
	cfg.MinOnsitePercentage = 50
	svc := NewService(db, cfg)

	today := testutil.NextBusinessDay(time.Now().UTC())
	todayStr := today.Format("2006-01-02")

	// 1-member team: floor is 1, so any approved WFH today means
	// the floor is at capacity.
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	got, err := svc.ReportToday(ctx, aliceID)
	require.NoError(t, err, "ReportToday must always return a row, even when denied")
	assert.Equal(t, database.WFHStatusDenied, got.Status,
		"with the entire team at the floor, the request must be denied rather than auto-approved")
	assert.Equal(t, todayStr, got.Date)
}

// TestReportToday_DuplicateRefuses pins the duplicate guard: if the
// user already has any row for today (pending, approved, withdrawn),
// ReportToday must not insert a second row. A withdrawn row from a
// quick change-of-mind counts the same as any other state — the
// user should resurrect via the regular request path instead.
func TestReportToday_DuplicateRefuses(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())

	today := testutil.NextBusinessDay(time.Now().UTC())
	todayStr := today.Format("2006-01-02")

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Seed a recurring (approved) row for today — same UNIQUE(member, date)
	// invariant a self-report would have to satisfy.
	require.NoError(t, db.CreateApprovedRecurringWFHRequest(ctx, aliceID, todayStr, time.Now().UTC()))

	got, err := svc.ReportToday(ctx, aliceID)
	require.ErrorIs(t, err, database.ErrWFHDuplicateRequest,
		"ReportToday must refuse when any row exists for the (member, today) pair")
	assert.Empty(t, got.ID, "no row should be returned on duplicate")

	// Count rows on disk to make sure nothing extra was inserted.
	all, err := db.GetWFHRequestsByDate(ctx, todayStr)
	require.NoError(t, err)
	count := 0
	for _, r := range all {
		if r.MemberID == aliceID {
			count++
		}
	}
	assert.Equal(t, 1, count, "ReportToday must not insert a duplicate row")
}

// TestReportToday_QuotaExhausted pins the quota branch: a member who
// has used MaxDaysPerPeriod must not be able to add another same-day
// WFH even if the floor has room. The error is a new sentinel so the
// web/API/CLI can map it to a user-friendly flash banner without
// string-matching the message.
func TestReportToday_QuotaExhausted(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := testConfig()
	cfg.MaxDaysPerPeriod = 2
	svc := NewService(db, cfg)

	// Burn the quota on two dates that are NOT today but ARE in
	// today's period. Today is the third (date3) which ReportToday
	// will see as its target — both dates must land in the period
	// containing date3 for the quota check to count them.
	today := testutil.NextBusinessDay(time.Now().UTC())
	date1 := today.AddDate(0, 0, 1).Format("2006-01-02")
	date2 := today.AddDate(0, 0, 2).Format("2006-01-02")

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	for _, d := range []string{date1, date2} {
		req, cErr := db.CreateWFHRequest(ctx, aliceID, d)
		require.NoError(t, cErr)
		require.NoError(t, db.UpdateWFHRequestStatus(ctx, req.ID, database.WFHStatusApproved))
	}

	got, err := svc.ReportToday(ctx, aliceID)
	require.ErrorIs(t, err, database.ErrWFHQuotaExhausted,
		"ReportToday must refuse when the member has used their full quota")
	assert.Empty(t, got.ID)
}

// TestReportToday_FeatureDisabled pins the kill-switch path: when
// WFH_ENABLED=false, ReportToday is a no-op error rather than a
// 500. This matches how the existing wfh request handlers already
// gate on the service being present.
func TestReportToday_FeatureDisabled(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := testConfig()
	cfg.Enabled = false
	svc := NewService(db, cfg)

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	got, err := svc.ReportToday(ctx, aliceID)
	require.ErrorIs(t, err, database.ErrWFHDisabled,
		"ReportToday must refuse when the WFH feature is disabled")
	assert.Empty(t, got.ID)
}

// TestReportToday_HolidayFails ensures the holiday guard fires for
// the same-day path too — a holiday is a non-working day and has no
// on-site capacity to consume.
func TestReportToday_HolidayFails(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	today := testutil.NextBusinessDay(time.Now().UTC())
	db.SetHolidayChecker(func(d time.Time) bool {
		return d.Equal(today)
	})

	svc := NewService(db, testConfig())

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	got, err := svc.ReportToday(ctx, aliceID)
	require.ErrorIs(t, err, database.ErrWFHOnHoliday,
		"ReportToday must refuse when today is a holiday")
	assert.Empty(t, got.ID)
}

// TestMarkWFH_CreatesAdminMarkedRow is the happy path: an admin marks
// a member as WFH for today, the row is created with is_admin_marked=1
// and the audit columns (marked_by, marked_at) populated. The mark
// is approved so every downstream query (quota, floor, ICS, dashboard
// presence) picks it up unchanged.
func TestMarkWFH_CreatesAdminMarkedRow(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	notifier := &recordingNotifier{}
	svc.SetNotifier(notifier)

	today := testutil.NextBusinessDay(time.Now().UTC())
	todayStr := today.Format("2006-01-02")

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	const adminID = "admin-1"
	const adminName = "Admin"
	seedAdminUser(t, ctx, db, adminID, adminName, adminName+"@example.com")

	got, err := svc.MarkWFH(ctx, aliceID, todayStr, adminID, adminName)
	require.NoError(t, err)
	assert.Equal(t, database.WFHStatusApproved, got.Status)
	assert.True(t, got.IsAdminMarked, "MarkWFH must set IsAdminMarked=true")
	require.NotNil(t, got.MarkedBy, "MarkedBy must be set so the audit trail is complete")
	assert.Equal(t, adminID, *got.MarkedBy)
	require.NotNil(t, got.MarkedAt, "MarkedAt must be set so the audit trail is complete")

	// The row is readable by id and the provenance travels with it.
	reread, err := db.GetWFHRequestByID(ctx, got.ID)
	require.NoError(t, err)
	assert.True(t, reread.IsAdminMarked)
	require.NotNil(t, reread.MarkedBy)
	assert.Equal(t, adminID, *reread.MarkedBy)
	assert.Equal(t, aliceID, reread.MemberID)
	assert.Equal(t, todayStr, reread.Date)

	// The notification fires with the admin's name as the actor.
	require.Len(t, notifier.events, 1)
	assert.Equal(t, aliceID, notifier.events[0].MemberID)
	assert.Equal(t, todayStr, notifier.events[0].Date)
	assert.Equal(t, adminName, notifier.events[0].ActorName)
	assert.Equal(t, database.WFHStatusApproved, notifier.events[0].NewStatus)
}

// TestMarkWFH_AllowedWhenQuotaExhausted pins the override: the mark
// must succeed even when the member is at the WFH_MAX_DAYS_PER_PERIOD
// quota. The admin is taking responsibility; the safety rail does
// not block. The mark is still counted in the quota (so the
// dashboard shows the actual state), and the quota display surfaces
// the overage via the new OverQuotaBy field.
func TestMarkWFH_AllowedWhenQuotaExhausted(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := testConfig()
	cfg.MaxDaysPerPeriod = 2
	svc := NewService(db, cfg)

	today := testutil.NextBusinessDay(time.Now().UTC())
	todayStr := today.Format("2006-01-02")
	// Two future business days in the current period (so the
	// rows count toward GetQuotaStatus's current-period lookup).
	// Inserted directly via SQL because CreateWFHRequest's
	// past-date guard refuses past dates even inside the
	// current period — the test cares about quota math, not
	// the date validator.
	day1 := testutil.NextBusinessDay(today)
	day2 := testutil.NextBusinessDay(day1.AddDate(0, 0, 1))
	nowUTC := time.Now().UTC()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Fill the quota with two already-approved WFH rows in the same period.
	for i, day := range []time.Time{day1, day2} {
		_, insertErr := db.ExecContext(ctx,
			`INSERT INTO wfh_requests (id, member_id, date, status, is_recurring, settled_at) VALUES (?, ?, ?, 'approved', 0, ?)`,
			fmt.Sprintf("quota-exhaust-%d", i), aliceID, day.Format("2006-01-02"), nowUTC)
		require.NoError(t, insertErr)
		_ = i
	}

	// Sanity: Alice is at 2/2 before the mark.
	pre, err := svc.GetQuotaStatus(ctx, aliceID)
	require.NoError(t, err)
	assert.Equal(t, 2, pre.Used)
	assert.Equal(t, 0, pre.OverQuotaBy)

	// Mark Alice for today. This would normally fail with
	// ErrWFHQuotaExhausted; the override path must succeed.
	adminID := seedAdminUser(t, ctx, db, "admin-1", "Admin", "admin@example.com")
	got, err := svc.MarkWFH(ctx, aliceID, todayStr, adminID, "Admin")
	require.NoError(t, err, "MarkWFH must succeed even when the quota is exhausted — it is an admin override")
	assert.True(t, got.IsAdminMarked)

	// The quota display shows the overage.
	post, err := svc.GetQuotaStatus(ctx, aliceID)
	require.NoError(t, err)
	assert.Equal(t, 3, post.Used, "the mark counts toward the quota")
	assert.Equal(t, 1, post.OverQuotaBy, "the dashboard surfaces the overage")
}

// TestMarkWFH_AllowedWhenFloorWouldBeViolated pins the second
// override: marking every active member as WFH today drops the
// on-site count to 0, below the floor. The mark must still succeed.
// This is the over-the-floor override: a normal ReportToday would
// be denied at settlement time, but the admin's mark bypasses the
// check entirely.
func TestMarkWFH_AllowedWhenFloorWouldBeViolated(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := testConfig()
	cfg.MinOnsitePercentage = 50
	cfg.MinOnsiteAbsolute = 1
	svc := NewService(db, cfg)

	today := testutil.NextBusinessDay(time.Now().UTC())
	todayStr := today.Format("2006-01-02")

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	carolID, err := db.AddTeamMember(ctx, "Carol", "carol@example.com")
	require.NoError(t, err)

	adminID := seedAdminUser(t, ctx, db, "admin-1", "Admin", "admin@example.com")

	// Mark every active member. Three WFH rows, zero on-site,
	// which is below the floor of max(50% of 3 = 2, 1) = 2.
	for _, id := range []string{aliceID, bobID, carolID} {
		got, markErr := svc.MarkWFH(ctx, id, todayStr, adminID, "Admin")
		require.NoError(t, markErr, "MarkWFH must succeed even when the floor would be violated")
		assert.True(t, got.IsAdminMarked)
	}

	// The dashboard sees all three as WFH today (the mark counts
	// in the math), so the on-site count is 0 and the floor
	// would be violated — but the override is already recorded.
	approved, err := db.GetWFHRequestsByDateAndStatus(ctx, todayStr, database.WFHStatusApproved)
	require.NoError(t, err)
	assert.Len(t, approved, 3)
}

// TestMarkWFH_DuplicateReturnsExisting pins idempotency: a second
// mark for the same (member, date) does not create a second row.
// The service translates the UNIQUE-constraint error into
// ErrWFHDuplicateRequest so the handler can render an "already
// marked" flash instead of a 500.
func TestMarkWFH_DuplicateReturnsExisting(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	today := testutil.NextBusinessDay(time.Now().UTC())
	todayStr := today.Format("2006-01-02")

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	adminID := seedAdminUser(t, ctx, db, "admin-1", "Admin", "admin@example.com")
	seedAdminUser(t, ctx, db, "admin-2", "Other Admin", "admin2@example.com")

	first, err := svc.MarkWFH(ctx, aliceID, todayStr, adminID, "Admin")
	require.NoError(t, err)

	_, err = svc.MarkWFH(ctx, aliceID, todayStr, "admin-2", "Other Admin")
	require.ErrorIs(t, err, database.ErrWFHDuplicateRequest,
		"second mark for the same (member, date) must surface as a duplicate")

	// No second row was created.
	rows, err := db.GetWFHRequestsByDate(ctx, todayStr)
	require.NoError(t, err)
	var count int
	for _, r := range rows {
		if r.MemberID == aliceID {
			count++
		}
	}
	assert.Equal(t, 1, count, "exactly one row should exist for (alice, today)")
	_ = first
}

// TestMarkWFH_RejectsNonTodayDate pins the today-only scope: a
// crafted body asking for a future or past date is rejected at the
// service layer. The form hides the date input but the service is
// the authoritative gate.
func TestMarkWFH_RejectsNonTodayDate(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	_, err = svc.MarkWFH(ctx, aliceID, tomorrow, "admin-1", "Admin")
	require.ErrorIs(t, err, database.ErrWFHInvalidDate,
		"MarkWFH must refuse a non-today date")
}

// TestMarkWFH_RejectsHoliday ensures the holiday gate fires for
// the mark path too — a holiday is a non-working day and the
// override should not plant a phantom WFH row on it.
func TestMarkWFH_RejectsHoliday(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	today := testutil.NextBusinessDay(time.Now().UTC())
	todayStr := today.Format("2006-01-02")
	db.SetHolidayChecker(func(d time.Time) bool { return d.Equal(today) })

	svc := NewService(db, testConfig())
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	_, err = svc.MarkWFH(ctx, aliceID, todayStr, "admin-1", "Admin")
	require.ErrorIs(t, err, database.ErrWFHOnHoliday,
		"MarkWFH must refuse today when today is a holiday")
}

// TestWithdrawAdminMark_RefundsQuota verifies the unmark path:
// deleting the admin-marked row frees the quota slot, so a
// subsequent quota check returns the pre-mark count.
func TestWithdrawAdminMark_RefundsQuota(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	today := testutil.NextBusinessDay(time.Now().UTC())
	todayStr := today.Format("2006-01-02")

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	adminID := seedAdminUser(t, ctx, db, "admin-1", "Admin", "admin@example.com")

	// Mark Alice.
	mark, err := svc.MarkWFH(ctx, aliceID, todayStr, adminID, "Admin")
	require.NoError(t, err)

	// Quota is 1/2 after the mark.
	post, err := svc.GetQuotaStatus(ctx, aliceID)
	require.NoError(t, err)
	assert.Equal(t, 1, post.Used)
	assert.Equal(t, 0, post.OverQuotaBy)

	// Unmark via the standard withdraw path. This is the same
	// storage path admin-withdraw uses, so the audit trail
	// (withdrawn_by, withdrawn_at) is preserved and the row
	// disappears — freeing the quota slot.
	require.NoError(t, db.WithdrawWFHRequest(ctx, mark.ID, adminID))

	final, err := svc.GetQuotaStatus(ctx, aliceID)
	require.NoError(t, err)
	assert.Equal(t, 0, final.Used, "withdrawing the mark must free the quota slot")
	assert.Equal(t, 0, final.OverQuotaBy)
}

// TestQuotaStatus_ReportsOverQuotaBy pins the new OverQuotaBy
// field: when used <= MaxDaysPerPeriod, it's 0; when used >
// MaxDaysPerPeriod, it surfaces the overage.
func TestQuotaStatus_ReportsOverQuotaBy(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := testConfig()
	cfg.MaxDaysPerPeriod = 1
	svc := NewService(db, cfg)

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Under quota: OverQuotaBy is 0.
	pre, err := svc.GetQuotaStatus(ctx, aliceID)
	require.NoError(t, err)
	assert.Equal(t, 0, pre.Used)
	assert.Equal(t, 0, pre.OverQuotaBy)

	// Two WFH rows in the current period (so the quota counter
	// sees them). Inserted directly via SQL because
	// CreateWFHRequest's date guard refuses past dates even
	// inside the current period — the test cares about quota
	// math, not the date validator.
	nowUTC := time.Now().UTC()
	today := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	day1 := testutil.NextBusinessDay(today)
	day2 := testutil.NextBusinessDay(day1.AddDate(0, 0, 1))
	for i, day := range []time.Time{day1, day2} {
		_, insertErr := db.ExecContext(ctx,
			`INSERT INTO wfh_requests (id, member_id, date, status, is_recurring, settled_at) VALUES (?, ?, ?, 'approved', 0, ?)`,
			fmt.Sprintf("quota-seed-%d", i), aliceID, day.Format("2006-01-02"), nowUTC)
		require.NoError(t, insertErr)
		_ = i
	}

	post, err := svc.GetQuotaStatus(ctx, aliceID)
	require.NoError(t, err)
	assert.Equal(t, 2, post.Used)
	assert.Equal(t, 1, post.OverQuotaBy, "the dashboard surfaces the overage")
}

// TestSettlePendingRequests_DenialReasonIsRecorded pins the
// user-facing surface of a denial: when the settlement path denies
// a request, the human-readable reason lands in the
// wfh_requests.denial_reason column (and rides the row to the
// WFH list page, the admin manage page, and the email
// notification). Without this, the user sees a bare "Denied" tag
// and has to guess why. The test exercises the same floor
// scenario as the existing settlement tests, so the reason
// computation is exercised end-to-end.
func TestSettlePendingRequests_DenialReasonIsRecorded(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := testConfig()
	cfg.MinOnsitePercentage = 50
	cfg.MinOnsiteAbsolute = 1
	svc := NewService(db, cfg)
	notifier := &recordingNotifier{}
	svc.SetNotifier(notifier)

	targetDate := testutil.NextBusinessDay(time.Now().UTC().AddDate(0, 0, 1))
	targetDateStr := targetDate.Format("2006-01-02")

	// Two-person team (50% of 2 = 1, min-absolute 1, floor 1).
	// Two pending requests. Approve the first, deny the second.
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	aliceReq, err := db.CreateWFHRequest(ctx, aliceID, targetDateStr)
	require.NoError(t, err)
	bobReq, err := db.CreateWFHRequest(ctx, bobID, targetDateStr)
	require.NoError(t, err)

	require.NoError(t, svc.SettlePendingRequests(ctx))

	// Alice wins the slot (created first), Bob is denied.
	aliceRow, err := db.GetWFHRequestByID(ctx, aliceReq.ID)
	require.NoError(t, err)
	assert.Equal(t, database.WFHStatusApproved, aliceRow.Status)
	assert.Nil(t, aliceRow.DenialReason, "approved rows must not carry a denial reason")

	bobRow, err := db.GetWFHRequestByID(ctx, bobReq.ID)
	require.NoError(t, err)
	assert.Equal(t, database.WFHStatusDenied, bobRow.Status)
	require.NotNil(t, bobRow.DenialReason, "denied rows must carry a denial reason so the user knows why")
	assert.Contains(t, *bobRow.DenialReason, "On-site coverage would drop below the minimum",
		"the reason must explain the floor-driven denial in plain language")
	assert.NotEmpty(t, *bobRow.SettledAt, "denial records settled_at so the audit trail is complete")

	// The notifier sees the same reason so the email template can
	// render it. Two events fire: one approve for Alice, one
	// deny for Bob. We assert on Bob's event specifically.
	var denyEvent *notify.WFHEvent
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	for i := range notifier.events {
		if notifier.events[i].NewStatus == database.WFHStatusDenied {
			denyEvent = &notifier.events[i]
			break
		}
	}
	require.NotNil(t, denyEvent, "the deny path must fire a notification")
	assert.Equal(t, *bobRow.DenialReason, denyEvent.Reason,
		"the email event's Reason must match the on-disk denial_reason")
}

// TestSettlePendingRequests_DenialReasonCarriesFloorValue pins the
// user-facing wording: the reason names the floor value (1 in
// this test) so the user can correlate the message with the
// dashboard. A generic "on-site count would drop below the
// minimum" without the number would force the user to dig
// through the help page; including the value keeps the
// explanation self-contained.
func TestSettlePendingRequests_DenialReasonCarriesFloorValue(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := testConfig()
	cfg.MinOnsitePercentage = 50
	cfg.MinOnsiteAbsolute = 1
	svc := NewService(db, cfg)

	targetDate := testutil.NextBusinessDay(time.Now().UTC().AddDate(0, 0, 1))
	targetDateStr := targetDate.Format("2006-01-02")

	// Three-person team. Two pending requests; one approval, one
	// denial (floor is 2 of 3, so only one slot opens up).
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	carolID, err := db.AddTeamMember(ctx, "Carol", "carol@example.com")
	require.NoError(t, err)

	aliceReq, err := db.CreateWFHRequest(ctx, aliceID, targetDateStr)
	require.NoError(t, err)
	bobReq, err := db.CreateWFHRequest(ctx, bobID, targetDateStr)
	require.NoError(t, err)
	_, _ = carolID, bobReq // suppress unused

	require.NoError(t, svc.SettlePendingRequests(ctx))

	bobRow, err := db.GetWFHRequestByID(ctx, bobReq.ID)
	require.NoError(t, err)
	require.NotNil(t, bobRow.DenialReason, "denial must carry a reason")
	// With 3 members and a 50% floor (rounded up = 2, abs min 1),
	// the floor is 2. The reason must name that number so the
	// user can correlate the message with the dashboard.
	assert.Contains(t, *bobRow.DenialReason, "2",
		"the reason must surface the floor value (2) so the user can correlate with the dashboard")
	_ = aliceReq
}

// -- Per-period quota + holiday helpers -------------------------------------

// TestGetQuotaStatusForDate_MirrorsCheckQuotaAcrossPeriods pins the
// per-period quota helper used by the request form to surface a
// period-aware banner. Two key properties:
//
//  1. The helper returns the same answer CheckQuota would for the
//     *same* date — they share the period-computation logic.
//  2. A fresh member with 0 days used in the *next* period has
//     Remaining=2 there even when the *current* period is
//     exhausted. The form must show this so a user with "no tokens
//     this month" can still request for next month.
func TestGetQuotaStatusForDate_MirrorsCheckQuotaAcrossPeriods(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	today := time.Now().UTC()

	// (1) Fresh member, today: helper and CheckQuota agree.
	status, err := svc.GetQuotaStatusForDate(ctx, memberID, today)
	require.NoError(t, err)
	assert.Equal(t, 0, status.Used)
	assert.Equal(t, 2, status.Remaining)
	hasQuota, err := svc.CheckQuota(ctx, memberID, today.Format("2006-01-02"))
	require.NoError(t, err)
	assert.True(t, hasQuota, "fresh member must have quota today")

	// (2) Fill the current period to the max so current Remaining=0.
	// Pick the last two days of the current period so the rows are
	// in the future and CreateWFHRequest's date guard accepts them.
	_, currentEnd, err := svc.ComputePeriodBounds(today)
	require.NoError(t, err)
	_, err = db.CreateWFHRequest(ctx, memberID,
		currentEnd.AddDate(0, 0, -2).Format("2006-01-02"))
	require.NoError(t, err)
	_, err = db.CreateWFHRequest(ctx, memberID,
		currentEnd.AddDate(0, 0, -1).Format("2006-01-02"))
	require.NoError(t, err)

	// (2a) Today: Remaining=0, CheckQuota refuses.
	status, err = svc.GetQuotaStatusForDate(ctx, memberID, today)
	require.NoError(t, err)
	assert.Equal(t, 2, status.Used)
	assert.Equal(t, 0, status.Remaining, "current period must be exhausted")
	hasQuota, err = svc.CheckQuota(ctx, memberID, today.Format("2006-01-02"))
	require.NoError(t, err)
	assert.False(t, hasQuota, "CheckQuota must refuse when current period is exhausted")

	// (2b) A date 14 days out (next period, PeriodDays=7): the
	// helper must report Remaining=2 — the quota resets at the
	// period boundary, not at the end of the horizon.
	farFuture := today.AddDate(0, 0, 14)
	farFutureStr := farFuture.Format("2006-01-02")
	status, err = svc.GetQuotaStatusForDate(ctx, memberID, farFuture)
	require.NoError(t, err)
	assert.Equal(t, 0, status.Used,
		"next-period quota must NOT carry over the current period's usage")
	assert.Equal(t, 2, status.Remaining,
		"next-period quota must be full (MaxDaysPerPeriod)")
	hasQuota, err = svc.CheckQuota(ctx, memberID, farFutureStr)
	require.NoError(t, err)
	assert.True(t, hasQuota,
		"CheckQuota must allow a request in a future period even when the current period is exhausted")

	// (2c) The two helpers must agree on the period bounds for the
	// far-future date — the form's banner data must match what
	// CheckQuota would compute on submit.
	futureStart, futureEnd, err := svc.ComputePeriodBounds(farFuture)
	require.NoError(t, err)
	assert.Equal(t, futureStart.Format("2006-01-02"), status.PeriodStart)
	assert.Equal(t, futureEnd.Format("2006-01-02"), status.PeriodEnd)
}

// TestService_IsHoliday_DelegatesToDB pins the holiday short-circuit
// used by the form to disable the submit button when the user picks a
// date that's a public holiday. Mirrors the CheckQuota
// ErrWFHOnHoliday guard at the form layer so the user doesn't have
// to round-trip to learn the date is invalid.
func TestService_IsHoliday_DelegatesToDB(t *testing.T) {
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())

	holiday := time.Now().UTC().AddDate(0, 0, 3).Format("2006-01-02")
	db.SetHolidayChecker(func(t time.Time) bool {
		return t.Format("2006-01-02") == holiday
	})
	t.Cleanup(func() { db.SetHolidayChecker(nil) })

	assert.True(t, svc.IsHoliday(holiday),
		"a registered holiday must be reported as such")
	assert.False(t, svc.IsHoliday(time.Now().UTC().Format("2006-01-02")),
		"a non-holiday must be reported as such")
	// Unparseable input must be a no-op rather than a panic —
	// the form layer treats it as a separate error.
	assert.False(t, svc.IsHoliday("not-a-date"),
		"an unparseable date must not be reported as a holiday")
}

// TestSettlePendingRequests_PickerRunsOverSettlementWindow pins
// the picker integration from step 8 of
// plans/assigned-wfh-plan.md. The picker must run over every
// working day in the settlement window [today, today+SettlementDays],
// independent of byDate (dates with pending requests). This is
// the case the plan explicitly calls out: "a date can be over
// cap even with zero pending WFH requests."
//
// Setup: 5 members, cap=2, no pending WFH requests. After
// SettlePendingRequests, the picker must have assigned 3
// members across the settlement window's working days. With
// the default SettlementDays=7, that's at least 4 working
// days (today, today+1, today+2, today+3 if today is Monday;
// fewer if today is later in the week — see
// TestSettlePendingRequests_PickerRunsOverSettlementWindow for
// the calendar-aware variant).
func TestSettlePendingRequests_PickerRunsOverSettlementWindow(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := pickerTestConfig()
	// Pin to 7 days so the test's cap arithmetic is stable
	// across runs regardless of today's weekday.
	cfg.SettlementDays = 7
	svc := NewService(db, cfg)

	for _, name := range []string{"Alice", "Bob", "Carol", "Dave", "Erin"} {
		seedPickerMember(t, ctx, db, name, name+"@example.com")
	}

	// No pending requests — but the picker still runs and
	// caps the on-site count.
	require.NoError(t, svc.SettlePendingRequests(ctx))

	// The picker inserts origin='assigned' rows. Count them
	// across the whole settlement window by iterating over
	// each working day and tallying. Each working day that
	// has on-site > cap produces 3 assigned rows
	// (5 members - 2 cap = 3 excess).

	// Count assigned rows across the settlement window.
	assignedCount := 0
	for d := time.Now().UTC(); !d.After(time.Now().UTC().AddDate(0, 0, 7)); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		dateStr := d.Format("2006-01-02")
		dayRows, err := db.GetWFHRequestsByDate(ctx, dateStr)
		require.NoError(t, err)
		for _, r := range dayRows {
			if r.Origin == "assigned" {
				assignedCount++
			}
		}
	}

	// With 5 working days in the next 7 days (worst case
	// Sunday/Monday overlap is 5, best case is 5), each day
	// produces 3 assigned rows. Total: 5 * 3 = 15. Allow some
	// flexibility for the rare 4-working-day window.
	require.GreaterOrEqual(t, assignedCount, 12,
		"picker must have assigned at least 4 working days * 3 members = 12 rows; got %d", assignedCount)
}

// TestSettlePendingRequests_PickerRunsOverCapWithNoPending pins
// the specific case from the plan: a date is over cap with
// zero pending requests. The picker still picks. This test
// doesn't depend on SettlementDays — it focuses on a single
// future date via AssignWFHForDate directly (which is what
// SettlePendingRequests calls).
func TestSettlePendingRequests_PickerRunsOverCapWithNoPending(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, pickerTestConfig())
	for _, name := range []string{"Alice", "Bob", "Carol", "Dave", "Erin"} {
		seedPickerMember(t, ctx, db, name, name+"@example.com")
	}

	// No pending requests.
	require.NoError(t, svc.SettlePendingRequests(ctx))

	// The full sweep must have assigned members across all
	// working days in the window. At minimum, today must
	// have assigned rows (the picker's first iteration).
	today := time.Now().UTC()
	for !isWorkingDay(today) {
		today = today.AddDate(0, 0, 1)
	}
	todayRows, err := db.GetWFHRequestsByDate(ctx, today.Format("2006-01-02"))
	require.NoError(t, err)
	assigned := 0
	for _, r := range todayRows {
		if r.Origin == "assigned" {
			assigned++
		}
	}
	require.Equal(t, 3, assigned,
		"5 members with cap=2 on a day with no other WFHs must assign 3 members")
}

// isWorkingDay reports whether the given time falls on a Mon-Fri
// weekday. Saturday and Sunday are weekends; the picker is a
// no-op on weekends so the settlement sweep skips them.
func isWorkingDay(t time.Time) bool {
	return t.Weekday() != time.Saturday && t.Weekday() != time.Sunday
}
