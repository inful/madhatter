package wfh

import (
	"context"
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
