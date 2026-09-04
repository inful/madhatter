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

// setupDashboardTestDBWithCap spins up the dashboard test fixture
// and wires a wfh.Service whose seat cap is seatCap. Tests that
// exercise the chairs row need a configured service; tests that
// exercise the no-cap path use the plain setupDashboardTestDB
// instead.
func setupDashboardTestDBWithCap(t *testing.T, seatCap int) (*database.DB, *Handler, func()) {
	t.Helper()
	db, h, cleanup := setupDashboardTestDB(t)
	h.wfhService = wfh.NewService(db, wfh.Config{
		Enabled:             true,
		SeatCap:             seatCap,
		MinOnsitePercentage: 50,
		MinOnsiteAbsolute:   1,
		MaxDaysPerPeriod:    2,
		PeriodDays:          7,
		PeriodAnchor:        "2026-01-05",
		SettlementDays:      2,
		RequestHorizonDays:  90,
	})
	return db, h, cleanup
}

// ---------------------------------------------------------------------------
// Step 5 — Ass/chair ratio on the Today card.
//
// loadChairsData populates data["ChairsOnSite"], ["ChairsTotal"],
// ["ChairsPercent"], and ["ChairsColor"] when WFH_SEAT_CAP is set.
// The template hides the row entirely when the cap is unset.
// ---------------------------------------------------------------------------

// TestLoadChairsData_NoCap_OmitsRow pins the off-switch: when
// WFH_SEAT_CAP is unset (or zero), the chairs row must not
// appear in the data context. The template renders nothing
// without these fields, so the user sees the existing Today card
// unchanged — no extra "Office" label, no misleading ratio
// against an empty denominator.
func TestLoadChairsData_NoCap_OmitsRow(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupDashboardTestDB(t)
	defer cleanup()

	// No wfhService wired — cap is implicitly zero.
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	data := map[string]any{}
	today := time.Now().Format("2006-01-02")
	h.loadChairsData(ctx, data, today)

	for _, key := range []string{"ChairsOnSite", "ChairsTotal", "ChairsPercent", "ChairsColor"} {
		_, present := data[key]
		assert.False(t, present,
			"field %q must NOT be set when no cap is configured (so the template suppresses the row)", key)
	}
}

// TestLoadChairsData_ServiceDisabled_StillRendersWhenCapSet pins
// the second off-switch boundary. When the wfhService exists
// with WFH_ENABLED=false but a cap is set, the chair ratio is
// still meaningful as information — "how full is the office
// against the configured cap" — even though the picker won't run.
// The loader shows the ratio whenever a cap is configured; the
// picker state is a separate question handled elsewhere.
func TestLoadChairsData_ServiceDisabled_StillRendersWhenCapSet(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupDashboardTestDB(t)
	defer cleanup()

	h.wfhService = wfh.NewService(db, wfh.Config{
		Enabled: false,
		SeatCap: 7,
	})

	_, err := db.AddTeamMember(ctx, "alice", "alice@example.com")
	require.NoError(t, err)

	data := map[string]any{}
	today := time.Now().Format("2006-01-02")
	h.loadChairsData(ctx, data, today)

	assert.Equal(t, 1, data["ChairsOnSite"], "1 active member")
	assert.Equal(t, 7, data["ChairsTotal"], "cap=7 even with service disabled")
	assert.Equal(t, 14, data["ChairsPercent"])
	assert.Equal(t, "is-success", data["ChairsColor"])
}

// TestLoadChairsData_CapZeroWithServiceStillOmitsRow covers the
// "cap explicitly set to zero" case — same outcome as unset,
// because the picker gates on `SeatCap > 0`.
func TestLoadChairsData_CapZeroWithServiceStillOmitsRow(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupDashboardTestDB(t)
	defer cleanup()

	h.wfhService = wfh.NewService(db, wfh.Config{
		Enabled: true,
		SeatCap: 0, // explicit zero — picker disabled
	})

	_, err := db.AddTeamMember(ctx, "alice", "alice@example.com")
	require.NoError(t, err)

	data := map[string]any{}
	today := time.Now().Format("2006-01-02")
	h.loadChairsData(ctx, data, today)

	_, present := data["ChairsTotal"]
	assert.False(t, present,
		"explicit cap=0 is the same as unset for the picker — row must not render")
}

// TestLoadChairsData_AllOnSite_RendersSuccess pins the green band:
// 3 of 5 chairs → 60% → is-success tag. No leave, no WFH, just
// everyone in the office.
func TestLoadChairsData_AllOnSite_RendersSuccess(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupDashboardTestDBWithCap(t, 5)
	defer cleanup()

	for _, name := range []string{"Alice", "Bob", "Carol"} {
		_, err := db.AddTeamMember(ctx, name, name+"@example.com")
		require.NoError(t, err)
	}

	data := map[string]any{}
	today := time.Now().Format("2006-01-02")
	h.loadChairsData(ctx, data, today)

	assert.Equal(t, 3, data["ChairsOnSite"],
		"all 3 active members are on-site today")
	assert.Equal(t, 5, data["ChairsTotal"], "cap=5 from the test fixture")
	assert.Equal(t, 60, data["ChairsPercent"], "3/5 = 60%%")
	assert.Equal(t, "is-success", data["ChairsColor"],
		"under cap → success band")
}

// TestLoadChairsData_SubtractsLeaveAndWFH pins the subtraction
// math: 5 active members, 1 on leave, 1 WFH → 3 on-site.
func TestLoadChairsData_SubtractsLeaveAndWFH(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupDashboardTestDBWithCap(t, 7)
	defer cleanup()

	for _, name := range []string{"alice", "bob", "carol", "dave", "eve"} {
		_, err := db.AddTeamMember(ctx, name, name+"@example.com")
		require.NoError(t, err)
	}

	// Carol is on leave today.
	carolID, err := db.GetMemberByEmail(ctx, "carol@example.com")
	require.NoError(t, err)
	leaveID, err := db.CreateLeaveRecord(ctx, carolID.ID, time.Now().Format("2006-01-02"), time.Now().Format("2006-01-02"), database.LeaveTypeLeave)
	require.NoError(t, err)
	require.NoError(t, db.UpdateLeaveStatus(ctx, leaveID, "assigned"))

	// Dave is WFH today (approved).
	daveID, err := db.GetMemberByEmail(ctx, "dave@example.com")
	require.NoError(t, err)
	daveWFH, err := db.CreateWFHRequest(ctx, daveID.ID, time.Now().Format("2006-01-02"))
	require.NoError(t, err)
	require.NoError(t, db.UpdateWFHRequestStatus(ctx, daveWFH.ID, database.WFHStatusApproved))

	data := map[string]any{}
	today := time.Now().Format("2006-01-02")
	h.loadChairsData(ctx, data, today)

	assert.Equal(t, 3, data["ChairsOnSite"],
		"5 active - 1 leave - 1 WFH = 3 on-site")
	assert.Equal(t, 7, data["ChairsTotal"])
	// 3/7 = 42% (integer division).
	assert.Equal(t, 42, data["ChairsPercent"])
	assert.Equal(t, "is-success", data["ChairsColor"])
}

// TestLoadChairsData_AtCap_RendersWarning pins the orange band:
// the picker has run and filled the room exactly to the cap.
func TestLoadChairsData_AtCap_RendersWarning(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupDashboardTestDBWithCap(t, 3)
	defer cleanup()

	for _, name := range []string{"Alice", "Bob", "Carol"} {
		_, err := db.AddTeamMember(ctx, name, name+"@example.com")
		require.NoError(t, err)
	}

	data := map[string]any{}
	today := time.Now().Format("2006-01-02")
	h.loadChairsData(ctx, data, today)

	assert.Equal(t, 3, data["ChairsOnSite"])
	assert.Equal(t, 3, data["ChairsTotal"])
	assert.Equal(t, 100, data["ChairsPercent"])
	assert.Equal(t, "is-warning", data["ChairsColor"],
		"exactly at the cap → the picker has run, surface as warning (orange)")
}

// TestLoadChairsData_OverCap_RendersDanger pins the red band:
// 4 of 3 chairs → 133% → is-danger. Transient state during
// settlement (e.g. a member just came in while the picker hasn't
// re-run yet). The user should see this so they can poke the
// admin.
func TestLoadChairsData_OverCap_RendersDanger(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupDashboardTestDBWithCap(t, 3)
	defer cleanup()

	for _, name := range []string{"Alice", "Bob", "Carol", "Dave"} {
		_, err := db.AddTeamMember(ctx, name, name+"@example.com")
		require.NoError(t, err)
	}

	data := map[string]any{}
	today := time.Now().Format("2006-01-02")
	h.loadChairsData(ctx, data, today)

	assert.Equal(t, 4, data["ChairsOnSite"],
		"4 active, no leave/WFH → all 4 are on-site")
	assert.Equal(t, 3, data["ChairsTotal"], "cap=3 from the test fixture")
	assert.Equal(t, 133, data["ChairsPercent"], "4/3 = 133%%")
	assert.Equal(t, "is-danger", data["ChairsColor"],
		"over the cap → transient state, surface as danger (red)")
}

// TestLoadChairsData_StaleLeaveIgnored pins the leave-status
// filter: a rejected leave must NOT count as "on leave" today.
// Without this guard, a stale rejected leave would inflate the
// on-site count by subtracting a member who is in fact in the
// office.
func TestLoadChairsData_StaleLeaveIgnored(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupDashboardTestDBWithCap(t, 5)
	defer cleanup()

	for _, name := range []string{"alice", "bob", "carol"} {
		_, err := db.AddTeamMember(ctx, name, name+"@example.com")
		require.NoError(t, err)
	}

	// Carol's leave was rejected — she's in the office today.
	carolID, err := db.GetMemberByEmail(ctx, "carol@example.com")
	require.NoError(t, err)
	leaveID, err := db.CreateLeaveRecord(ctx, carolID.ID, time.Now().Format("2006-01-02"), time.Now().Format("2006-01-02"), database.LeaveTypeLeave)
	require.NoError(t, err)
	require.NoError(t, db.UpdateLeaveStatus(ctx, leaveID, "rejected"))

	data := map[string]any{}
	today := time.Now().Format("2006-01-02")
	h.loadChairsData(ctx, data, today)

	assert.Equal(t, 3, data["ChairsOnSite"],
		"rejected leave is stale; all 3 members are on-site")
}

// TestLoadChairsData_PendingWFHIgnored pins the WFH-status filter:
// a pending WFH request hasn't settled yet, so the member isn't
// actually WFH today and must NOT be subtracted. The dashboard
// shows the honest on-site count pre-settlement.
func TestLoadChairsData_PendingWFHIgnored(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupDashboardTestDBWithCap(t, 5)
	defer cleanup()

	for _, name := range []string{"alice", "bob", "carol"} {
		_, err := db.AddTeamMember(ctx, name, name+"@example.com")
		require.NoError(t, err)
	}

	// Carol's WFH request is still pending — she may or may not
	// be approved by the next settlement tick. The chairs row
	// is honest about the current state.
	carolID, err := db.GetMemberByEmail(ctx, "carol@example.com")
	require.NoError(t, err)
	_, err = db.CreateWFHRequest(ctx, carolID.ID, time.Now().Format("2006-01-02"))
	require.NoError(t, err)

	data := map[string]any{}
	today := time.Now().Format("2006-01-02")
	h.loadChairsData(ctx, data, today)

	assert.Equal(t, 3, data["ChairsOnSite"],
		"pending WFH is not yet settled; member is still on-site")
}

// TestLoadChairsData_NegativeOnSiteClampsToZero pins the lower
// clamp. A transient state during settlement (e.g. a leave was
// just created before the matching approved WFH row landed) can
// produce a negative on-site count if we naively subtract. The
// bar must render at zero, not at a negative value, so the user
// doesn't see a backwards progress bar.
func TestLoadChairsData_NegativeOnSiteClampsToZero(t *testing.T) {
	ctx := context.Background()
	db, h, cleanup := setupDashboardTestDBWithCap(t, 3)
	defer cleanup()

	// Two members: both on leave (engine-induced scenario).
	for _, name := range []string{"alice", "bob"} {
		_, err := db.AddTeamMember(ctx, name, name+"@example.com")
		require.NoError(t, err)
	}

	today := time.Now().Format("2006-01-02")
	for _, name := range []string{"alice", "bob"} {
		member, err := db.GetMemberByEmail(ctx, name+"@example.com")
		require.NoError(t, err)
		leaveID, err := db.CreateLeaveRecord(ctx, member.ID, today, today, database.LeaveTypeLeave)
		require.NoError(t, err)
		require.NoError(t, db.UpdateLeaveStatus(ctx, leaveID, "assigned"))
	}

	data := map[string]any{}
	h.loadChairsData(ctx, data, today)

	assert.Equal(t, 0, data["ChairsOnSite"],
		"negative on-site must clamp to 0 (transient settlement state)")
	assert.Equal(t, 0, data["ChairsPercent"], "0/3 = 0%%")
	assert.Equal(t, "is-success", data["ChairsColor"])
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
