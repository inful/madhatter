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
	"github.com/inful/madhatter/internal/notify"
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
		WithdrawalHours:     24,
	}
}

func nextBusinessDay(from time.Time) time.Time {
	date := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	for date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
		date = date.AddDate(0, 0, 1)
	}
	return date
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
	assert.Equal(t, defaultWithdrawalHours, cfg.WithdrawalHours)

	t.Setenv("WFH_ENABLED", "false")
	t.Setenv("WFH_MIN_ONSITE_PERCENTAGE", "60.5")
	t.Setenv("WFH_MIN_ONSITE_ABSOLUTE", "3")
	t.Setenv("WFH_MAX_DAYS_PER_PERIOD", "4")
	t.Setenv("WFH_PERIOD_DAYS", "14")
	t.Setenv("WFH_PERIOD_ANCHOR", "2026-02-02")
	t.Setenv("WFH_SETTLEMENT_DAYS", "5")
	t.Setenv("WFH_WITHDRAWAL_HOURS", "72")

	cfg = LoadConfigFromEnv()
	assert.False(t, cfg.Enabled)
	assert.LessOrEqual(t, math.Abs(cfg.MinOnsitePercentage-60.5), 0.0001)
	assert.Equal(t, 3, cfg.MinOnsiteAbsolute)
	assert.Equal(t, 4, cfg.MaxDaysPerPeriod)
	assert.Equal(t, 14, cfg.PeriodDays)
	assert.Equal(t, "2026-02-02", cfg.PeriodAnchor)
	assert.Equal(t, 5, cfg.SettlementDays)
	assert.Equal(t, 72, cfg.WithdrawalHours)

	t.Setenv("WFH_ENABLED", "not-a-bool")
	t.Setenv("WFH_MIN_ONSITE_PERCENTAGE", "bad")
	t.Setenv("WFH_MIN_ONSITE_ABSOLUTE", "bad")
	cfg = LoadConfigFromEnv()
	assert.True(t, cfg.Enabled)
	assert.LessOrEqual(t, math.Abs(cfg.MinOnsitePercentage-defaultMinOnsitePercentage), 0.0001)
	assert.Equal(t, defaultMinOnsiteAbsolute, cfg.MinOnsiteAbsolute)
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

	today := nextBusinessDay(time.Now().UTC())
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

func TestCheckQuota_RecurringDaysReduceBudget(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Pick two weekdays that are guaranteed to be in the future of the
	// current period, so materialization inserts both. Wed+Thu work for
	// every day-of-week except Wednesday itself; the test runs at any time
	// and exercises the same code path.
	require.NoError(t, db.SetTeamMemberRecurringWFHDays(ctx, memberID, database.RecurringWFHDays{
		Wednesday: true,
		Thursday:  true,
	}))

	checkDate := nextWeekday(time.Now().UTC(), time.Friday).Format("2006-01-02")

	// Without materialization, recurring days don't pre-consume budget —
	// they're just a definition. The member has full quota available.
	hasQuota, err := svc.CheckQuota(ctx, memberID, checkDate)
	require.NoError(t, err)
	assert.True(t, hasQuota)

	// After materialization, the recurring occurrences in the period become
	// approved rows, which the period-usage count picks up.
	start, end, err := svc.ComputePeriodBounds(time.Now().UTC())
	require.NoError(t, err)
	_, err = svc.EnsureRecurringMaterializedForMember(ctx, memberID, start, end)
	require.NoError(t, err)

	hasQuota, err = svc.CheckQuota(ctx, memberID, checkDate)
	require.NoError(t, err)
	assert.False(t, hasQuota)
}

func nextWeekday(start time.Time, weekday time.Weekday) time.Time {
	date := start.AddDate(0, 0, 1)
	for date.Weekday() != weekday {
		date = date.AddDate(0, 0, 1)
	}
	return date
}

func TestPrioritisePending_SortsByUsageThenCreatedAt(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, testConfig())
	baseDate := nextBusinessDay(time.Now().UTC())
	date := nextBusinessDay(baseDate.AddDate(0, 0, 1))
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

	today := nextBusinessDay(time.Now().UTC())
	targetDate := nextBusinessDay(today.AddDate(0, 0, 1))
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

	targetDate := nextBusinessDay(time.Now().UTC().AddDate(0, 0, 1))
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

	today := nextBusinessDay(time.Now().UTC())
	targetDate := nextBusinessDay(today.AddDate(0, 0, 1))
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

	holidayDate := nextBusinessDay(time.Now().UTC().AddDate(0, 0, 5))
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

	holidayDate := nextBusinessDay(time.Now().UTC().AddDate(0, 0, 5))
	db.SetHolidayChecker(func(d time.Time) bool {
		return d.Format("2006-01-02") == holidayDate.Format("2006-01-02")
	})

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	nonHoliday := nextBusinessDay(holidayDate.AddDate(0, 0, 1))
	_, err = db.CreateWFHRequest(ctx, memberID, nonHoliday.Format("2006-01-02"))
	assert.NoError(t, err)
}

func TestCheckQuota_RejectsHoliday(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	t.Cleanup(cleanup)

	holidayDate := nextBusinessDay(time.Now().UTC().AddDate(0, 0, 5))
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

func TestSettlePendingRequests_FiresNotifierForEachTransition(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := testConfig()
	cfg.MinOnsitePercentage = 50
	cfg.MinOnsiteAbsolute = 1
	svc := NewService(db, cfg)

	today := nextBusinessDay(time.Now().UTC())
	targetDate := nextBusinessDay(today.AddDate(0, 0, 1))
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
