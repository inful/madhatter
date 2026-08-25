package web

import (
	"context"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/rota"
	"github.com/inful/madhatter/internal/wfh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadCurrentHAT_NormalCase asserts the loader sets the
// expected fields when the primary HAT is on duty today (no leave).
// The team-by-id query is what surfaces the primary's name as the
// banner's headline.
func TestLoadCurrentHAT_NormalCase(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupDashboardTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	today := time.Now().Format("2006-01-02")
	_, err = db.CreateRotaAssignment(ctx, today, aliceID, false, nil)
	require.NoError(t, err)

	data := map[string]any{}
	h.loadCurrentHAT(ctx, data)

	assert.Equal(t, "Alice", data["CurrentHATName"],
		"primary HAT name must be surfaced as the on-call person")
	assert.False(t, data["CurrentHATIsOnLeave"].(bool),
		"primary is not on leave, so the on-leave flag must be false")
	assert.Equal(t, "Alice", data["CurrentHATPrimaryName"],
		"primary name must be set so the on-leave status note can use it")
	_, coverNameSet := data["CurrentHATCoverName"]
	assert.False(t, coverNameSet,
		"no cover exists, so CurrentHATCoverName must not be set")
}

// TestLoadCurrentHAT_WithCoverOnLeave asserts the loader swaps the
// on-call name to the cover when the primary has an active leave
// record covering today. The on-leave flag stays true (the primary
// is still on leave) and the primary's name is surfaced alongside
// the cover's for the template's "X on leave" status note.
func TestLoadCurrentHAT_WithCoverOnLeave(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupDashboardTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	today := time.Now().Format("2006-01-02")
	_, err = db.CreateRotaAssignment(ctx, today, aliceID, false, nil)
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, today, bobID, true, nil)
	require.NoError(t, err)

	leaveID, err := db.CreateLeaveRecord(ctx, aliceID, today, today, database.LeaveTypeLeave)
	require.NoError(t, err)
	// 'assigned' is the canonical active state (the engine's
	// reassignment flow flips 'pending' → 'assigned' once the cover
	// has been created). Using 'approved' here would put the leave
	// in a stale state and the loader would treat the primary as
	// still on the rota.
	require.NoError(t, db.UpdateLeaveStatus(ctx, leaveID, "assigned"))

	data := map[string]any{}
	h.loadCurrentHAT(ctx, data)

	// The on-call name is the cover (role has been reassigned) — the
	// template uses this as the banner's main "HAT today: Bob" name.
	assert.Equal(t, "Bob", data["CurrentHATName"],
		"on-call name must be the cover when the primary is on leave")
	assert.True(t, data["CurrentHATIsOnLeave"].(bool),
		"primary is on leave, so the on-leave flag must be true")
	// The primary's name is rendered separately as the "(Alice) on
	// leave" status note next to the on-call name.
	assert.Equal(t, "Alice", data["CurrentHATPrimaryName"],
		"primary name must be set so the template's '(Alice) on leave' note can render")
}

// TestLoadCurrentHAT_IgnoresRejectedLeave asserts the loader treats
// rejected leaves as stale. Without this guard, a rejected leave
// would flip the banner into "on leave" mode and the cover would
// be displayed as the on-call hero — a confusing UX since the
// rejected leave meant the engine kept the primary on call.
func TestLoadCurrentHAT_IgnoresRejectedLeave(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupDashboardTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	today := time.Now().Format("2006-01-02")
	_, err = db.CreateRotaAssignment(ctx, today, aliceID, false, nil)
	require.NoError(t, err)

	leaveID, err := db.CreateLeaveRecord(ctx, aliceID, today, today, database.LeaveTypeLeave)
	require.NoError(t, err)
	require.NoError(t, db.UpdateLeaveStatus(ctx, leaveID, "rejected"))

	data := map[string]any{}
	h.loadCurrentHAT(ctx, data)

	assert.Equal(t, "Alice", data["CurrentHATName"],
		"rejected leave must not trigger the on-leave path")
	assert.False(t, data["CurrentHATIsOnLeave"].(bool),
		"rejected leave is stale data; on-leave flag must stay false")
}

// TestLoadCurrentHAT_NoPrimary asserts the loader is a no-op when
// no primary assignment exists for today. The dashboard template
// suppresses the banner when CurrentHATName is empty, so the
// loader just leaves the data map untouched.
func TestLoadCurrentHAT_NoPrimary(t *testing.T) {
	ctx := context.Background()
	_, h, cleanup := setupDashboardTestDB(t)
	defer cleanup()

	// No schedule assignments created — the table for today is empty.
	data := map[string]any{}
	h.loadCurrentHAT(ctx, data)

	_, nameSet := data["CurrentHATName"]
	assert.False(t, nameSet,
		"no primary assignment exists, so the loader must not set CurrentHATName")
}

// setupDashboardTestDB spins up a file-backed DB plus a Handler
// configured for dashboard tests. Mirrors setupLeaveTestDB but
// scoped to the dashboard helpers (loadCurrentHAT, loadDashboardData,
// etc.) — the maintenance chain is real so EnsureSchedule can be
// called if any test chooses to.
func setupDashboardTestDB(t *testing.T) (*database.DB, *Handler, func()) {
	t.Helper()
	db, err := database.New(t.TempDir() + "/dashboard_test.db")
	require.NoError(t, err)
	tmpl, err := parseTemplates()
	require.NoError(t, err)
	h := &Handler{
		db:          db,
		authManager: &auth.AuthManager{},
		tmpl:        tmpl,
		maintenance: rota.NewScheduleMaintenance(db),
	}
	return db, h, func() { _ = db.Close() }
}

// TestCanReportWFHToday_GatesOnServiceAndBusinessDay pins the
// conditions under which the dashboard's "WFH today" button
// renders. The handler must:
//   - return true when WFH is enabled and today is a business day
//   - return false when the service is missing or disabled
//   - return false when today is a weekend or holiday
func TestCanReportWFHToday_GatesOnServiceAndBusinessDay(t *testing.T) {
	ctx := context.Background()
	db, _, cleanup := setupDashboardTestDB(t)
	defer cleanup()

	enabled := wfh.NewService(db, wfh.Config{
		Enabled:             true,
		MinOnsitePercentage: 50,
		MinOnsiteAbsolute:   1,
		MaxDaysPerPeriod:    2,
		PeriodDays:          7,
		PeriodAnchor:        "2026-01-05",
		SettlementDays:      2,
		RequestHorizonDays:  90,
	})
	disabled := wfh.NewService(db, wfh.Config{Enabled: false})

	monday := time.Date(2026, time.January, 5, 0, 0, 0, 0, time.UTC) // known Monday
	holiday := func(time.Time) bool { return false }
	holidayToday := func(t time.Time) bool { return t.Equal(monday) }

	type tc struct {
		name    string
		svc     *wfh.Service
		today   time.Time
		holiday func(time.Time) bool
		want    bool
	}
	cases := []tc{
		{
			name:    "enabled on a business day",
			svc:     enabled,
			today:   monday,
			holiday: holiday,
			want:    true,
		},
		{
			name:    "disabled regardless of date",
			svc:     disabled,
			today:   monday,
			holiday: holiday,
			want:    false,
		},
		{
			name:    "missing service",
			svc:     nil,
			today:   monday,
			holiday: holiday,
			want:    false,
		},
		{
			name:    "today is a holiday",
			svc:     enabled,
			today:   monday,
			holiday: holidayToday,
			want:    false,
		},
		{
			name:    "today is a Saturday",
			svc:     enabled,
			today:   time.Date(2026, time.January, 3, 0, 0, 0, 0, time.UTC),
			holiday: holiday,
			want:    false,
		},
		{
			name:    "today is a Sunday",
			svc:     enabled,
			today:   time.Date(2026, time.January, 4, 0, 0, 0, 0, time.UTC),
			holiday: holiday,
			want:    false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmpl, err := parseTemplates()
			require.NoError(t, err)
			h := &Handler{
				db:             db,
				tmpl:           tmpl,
				wfhService:     c.svc,
				holidayChecker: c.holiday,
			}
			got2 := h.canReportWFHTodayAt(ctx, c.today)
			assert.Equal(t, c.want, got2)
		})
	}
}
