package wfh

import (
	"context"
	"math"
	"os"
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

func testConfig() Config {
	return Config{
		Enabled:             true,
		MinOnsitePercentage: 50,
		MinOnsiteAbsolute:   1,
		MaxDaysPerPeriod:    2,
		PeriodDays:          7,
		PeriodAnchor:        defaultPeriodAnchor,
		SettlementDays:      2,
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

	t.Setenv("WFH_ENABLED", "false")
	t.Setenv("WFH_MIN_ONSITE_PERCENTAGE", "60.5")
	t.Setenv("WFH_MIN_ONSITE_ABSOLUTE", "3")
	t.Setenv("WFH_MAX_DAYS_PER_PERIOD", "4")
	t.Setenv("WFH_PERIOD_DAYS", "14")
	t.Setenv("WFH_PERIOD_ANCHOR", "2026-02-02")
	t.Setenv("WFH_SETTLEMENT_DAYS", "5")
	t.Setenv("WFH_REQUEST_HORIZON_DAYS", "180")
	t.Setenv("WFH_PURGE_ENABLED", "false")

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

	t.Setenv("WFH_ENABLED", "not-a-bool")
	t.Setenv("WFH_MIN_ONSITE_PERCENTAGE", "bad")
	t.Setenv("WFH_MIN_ONSITE_ABSOLUTE", "bad")
	t.Setenv("WFH_REQUEST_HORIZON_DAYS", "bad")
	t.Setenv("WFH_PURGE_ENABLED", "not-a-bool")
	cfg = LoadConfigFromEnv()
	assert.True(t, cfg.Enabled)
	assert.LessOrEqual(t, math.Abs(cfg.MinOnsitePercentage-defaultMinOnsitePercentage), 0.0001)
	assert.Equal(t, defaultMinOnsiteAbsolute, cfg.MinOnsiteAbsolute)
	assert.Equal(t, defaultRequestHorizonDays, cfg.RequestHorizonDays)
	assert.True(t, cfg.PurgeEnabled, "unparseable bool must fall back to the default")
}

func TestComputePeriodBounds_AcrossAnchorBoundaries(t *testing.T) {
	svc := NewService(nil, testConfig())

	start, end, err := svc.ComputePeriodBounds(time.Date(2026, time.January, 7, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Equal(t, "2026-01-05", start.Format("2006-01-02"))
	assert.Equal(t, "2026-01-11", end.Format("2006-01-02"))

	start, end, err = svc.ComputePeriodBounds(time.Date(2026, time.January, 4, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Equal(t, "2025-12-29", start.Format("2006-01-02"))
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

	// Pick a Friday strictly in the future (next 14 days) and
	// materialize the same 14-day window. The check date and the
	// materialized recurring rows are guaranteed to land in the same
	// quota period, regardless of which day-of-week the test runs.
	today := time.Now().UTC()
	windowStart, windowEnd := today, today.AddDate(0, 0, 14)
	checkDate := nextWeekdayInRange(t, windowStart, windowEnd, time.Friday).Format("2006-01-02")

	// Without materialization, recurring days don't pre-consume budget —
	// they're just a definition. The member has full quota available.
	hasQuota, err := svc.CheckQuota(ctx, memberID, checkDate)
	require.NoError(t, err)
	assert.True(t, hasQuota)

	// After materialization, the recurring occurrences in the period become
	// approved rows, which the period-usage count picks up.
	_, err = svc.EnsureRecurringMaterializedForMember(ctx, memberID, windowStart, windowEnd)
	require.NoError(t, err)

	hasQuota, err = svc.CheckQuota(ctx, memberID, checkDate)
	require.NoError(t, err)
	assert.False(t, hasQuota)
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

	_, err = db.CreateLeaveRecord(ctx, daveID, targetDateStr, targetDateStr)
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
	_, err = db.CreateLeaveRecord(ctx, daveID, targetDateStr, targetDateStr)
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

// TestParseBoolEnv locks in the contract of the helper that will be
// deduped with notify/config.go::getEnvBool. The behavior must be:
//   - empty/unset → default
//   - "true"/"false"/"1"/"0"/etc → parsed value
//   - anything that strconv.ParseBool rejects → default (silent fallback)
func TestParseBoolEnv(t *testing.T) {
	t.Run("UnsetReturnsDefault", func(t *testing.T) {
		key := "WFH_TEST_UNSET_BOOL"
		require.NoError(t, os.Unsetenv(key))
		assert.True(t, parseBoolEnv(key, true))
		assert.False(t, parseBoolEnv(key, false))
	})

	t.Run("EmptyReturnsDefault", func(t *testing.T) {
		key := "WFH_TEST_EMPTY_BOOL"
		t.Setenv(key, "")
		assert.True(t, parseBoolEnv(key, true))
		assert.False(t, parseBoolEnv(key, false))
	})

	t.Run("ParsesTruthyValues", func(t *testing.T) {
		key := "WFH_TEST_TRUTHY_BOOL"
		for _, v := range []string{"true", "TRUE", "True", "1", "t", "T"} {
			t.Setenv(key, v)
			assert.True(t, parseBoolEnv(key, false), "value %q must parse true", v)
		}
	})

	t.Run("ParsesFalsyValues", func(t *testing.T) {
		key := "WFH_TEST_FALSY_BOOL"
		for _, v := range []string{"false", "FALSE", "False", "0", "f", "F"} {
			t.Setenv(key, v)
			assert.False(t, parseBoolEnv(key, true), "value %q must parse false", v)
		}
	})

	t.Run("GarbageFallsBackToDefault", func(t *testing.T) {
		key := "WFH_TEST_GARBAGE_BOOL"
		t.Setenv(key, "not-a-bool")
		assert.True(t, parseBoolEnv(key, true), "garbage must fall back to default true")
		assert.False(t, parseBoolEnv(key, false), "garbage must fall back to default false")
	})
}

// TestParseIntEnv covers the int helper used by LoadConfigFromEnv.
// Same contract as parseBoolEnv.
func TestParseIntEnv(t *testing.T) {
	t.Run("UnsetReturnsDefault", func(t *testing.T) {
		key := "WFH_TEST_UNSET_INT"
		require.NoError(t, os.Unsetenv(key))
		assert.Equal(t, 42, parseIntEnv(key, 42))
	})

	t.Run("ParsesValue", func(t *testing.T) {
		key := "WFH_TEST_PARSE_INT"
		t.Setenv(key, "7")
		assert.Equal(t, 7, parseIntEnv(key, 0))
	})

	t.Run("GarbageFallsBackToDefault", func(t *testing.T) {
		key := "WFH_TEST_GARBAGE_INT"
		t.Setenv(key, "not-an-int")
		assert.Equal(t, 99, parseIntEnv(key, 99))
	})
}

// TestParseFloat64Env covers the float helper.
func TestParseFloat64Env(t *testing.T) {
	t.Run("UnsetReturnsDefault", func(t *testing.T) {
		key := "WFH_TEST_UNSET_FLOAT"
		require.NoError(t, os.Unsetenv(key))
		assert.InDelta(t, 3.14, parseFloat64Env(key, 3.14), 0.0001)
	})

	t.Run("ParsesValue", func(t *testing.T) {
		key := "WFH_TEST_PARSE_FLOAT"
		t.Setenv(key, "2.5")
		assert.InDelta(t, 2.5, parseFloat64Env(key, 0), 0.0001)
	})

	t.Run("GarbageFallsBackToDefault", func(t *testing.T) {
		key := "WFH_TEST_GARBAGE_FLOAT"
		t.Setenv(key, "nope")
		assert.InDelta(t, 1.5, parseFloat64Env(key, 1.5), 0.0001)
	})
}

// TestParseStringEnv covers the string helper.
func TestParseStringEnv(t *testing.T) {
	t.Run("UnsetReturnsDefault", func(t *testing.T) {
		key := "WFH_TEST_UNSET_STR"
		require.NoError(t, os.Unsetenv(key))
		assert.Equal(t, "fallback", parseStringEnv(key, "fallback"))
	})

	t.Run("EmptyReturnsDefault", func(t *testing.T) {
		key := "WFH_TEST_EMPTY_STR"
		t.Setenv(key, "")
		assert.Equal(t, "fallback", parseStringEnv(key, "fallback"))
	})

	t.Run("ParsesValue", func(t *testing.T) {
		key := "WFH_TEST_PARSE_STR"
		t.Setenv(key, "value")
		assert.Equal(t, "value", parseStringEnv(key, "fallback"))
	})
}
