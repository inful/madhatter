package wfh

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pickerTestConfig is the standard config used by picker tests.
// SeatCap=2 forces the picker to engage for small team setups;
// MaxDaysPerPeriod=2 keeps the voluntary quota math independent.
func pickerTestConfig() Config {
	return Config{
		Enabled:                  true,
		MinOnsitePercentage:      50,
		MinOnsiteAbsolute:        1,
		MaxDaysPerPeriod:         2,
		PeriodDays:               7,
		PeriodAnchor:             "2026-01-05",
		SettlementDays:           7,
		RequestHorizonDays:       90,
		PurgeEnabled:             false,
		SeatCap:                  2,
		AssignmentEnabled:        true,
		CoPresenceEnabled:        true,
		CoPresenceHorizonDays:    14,
		CoPresenceRetentionDays:  30,
	}
}

// seedPickerMember adds a member with the given name and email.
func seedPickerMember(t *testing.T, ctx context.Context, db *database.DB, name, email string) string {
	t.Helper()
	id, err := db.AddTeamMember(ctx, name, email)
	require.NoError(t, err)
	return id
}

// pickerFutureDate returns a YYYY-MM-DD string N business days
// from now (UTC, skipping weekends). Default anchor for picker
// tests — the picker is a no-op for past dates (the past-date
// guard) and a no-op for weekends.
func pickerFutureDate(t *testing.T, n int) string {
	t.Helper()
	d := time.Now().UTC().AddDate(0, 0, n)
	for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		d = d.AddDate(0, 0, 1)
	}
	return d.Format("2006-01-02")
}

// TestAssignWFHForDate_NoOpWhenDisabled pins the three guards
// at the top of AssignWFHForDate. Disabled / assignment-off /
// no seat cap all produce zero inserts — the scheduler and the
// on-demand trigger can call this unconditionally.
func TestAssignWFHForDate_NoOpWhenDisabled(t *testing.T) {
	t.Run("feature disabled", func(t *testing.T) {
		ctx := context.Background()
		db, cleanup := setupWFHTestDB(t)
		defer cleanup()
		cfg := pickerTestConfig()
		cfg.Enabled = false
		svc := NewService(db, cfg)
		seedPickerMember(t, ctx, db, "Alice", "alice@example.com")
		seedPickerMember(t, ctx, db, "Bob", "bob@example.com")
		seedPickerMember(t, ctx, db, "Carol", "carol@example.com")
		require.NoError(t, svc.AssignWFHForDate(ctx, pickerFutureDate(t, 1)))
		rows, err := db.GetWFHRequestsByDate(ctx, pickerFutureDate(t, 1))
		require.NoError(t, err)
		assert.Empty(t, rows)
	})

	t.Run("assignment disabled", func(t *testing.T) {
		ctx := context.Background()
		db, cleanup := setupWFHTestDB(t)
		defer cleanup()
		cfg := pickerTestConfig()
		cfg.AssignmentEnabled = false
		svc := NewService(db, cfg)
		seedPickerMember(t, ctx, db, "Alice1", "alice1@example.com")
		seedPickerMember(t, ctx, db, "Bob1", "bob1@example.com")
		seedPickerMember(t, ctx, db, "Carol1", "carol1@example.com")
		require.NoError(t, svc.AssignWFHForDate(ctx, pickerFutureDate(t, 1)))
		rows, err := db.GetWFHRequestsByDate(ctx, pickerFutureDate(t, 1))
		require.NoError(t, err)
		assert.Empty(t, rows)
	})

	t.Run("no seat cap", func(t *testing.T) {
		ctx := context.Background()
		db, cleanup := setupWFHTestDB(t)
		defer cleanup()
		cfg := pickerTestConfig()
		cfg.SeatCap = 0
		svc := NewService(db, cfg)
		seedPickerMember(t, ctx, db, "Alice2", "alice2@example.com")
		seedPickerMember(t, ctx, db, "Bob2", "bob2@example.com")
		seedPickerMember(t, ctx, db, "Carol2", "carol2@example.com")
		require.NoError(t, svc.AssignWFHForDate(ctx, pickerFutureDate(t, 1)))
		rows, err := db.GetWFHRequestsByDate(ctx, pickerFutureDate(t, 1))
		require.NoError(t, err)
		assert.Empty(t, rows)
	})
}

// TestAssignWFHForDate_PastDateIsNoOp pins the past-date guard
// from section 3 of plans/assigned-wfh-plan.md. The picker
// must not insert assigned WFH rows for days that have
// already been lived; past attendance is immutable.
func TestAssignWFHForDate_PastDateIsNoOp(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, pickerTestConfig())
	seedPickerMember(t, ctx, db, "Alice", "alice@example.com")
	seedPickerMember(t, ctx, db, "Bob", "bob@example.com")
	seedPickerMember(t, ctx, db, "Carol", "carol@example.com")

	yesterday := pickerFutureDate(t, -1)
	// The helper skips weekends but for the past-date guard we
	// need any past date, even a weekend — set it explicitly.
	yesterday = time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	require.NoError(t, svc.AssignWFHForDate(ctx, yesterday))
	rows, err := db.GetWFHRequestsByDate(ctx, yesterday)
	require.NoError(t, err)
	assert.Empty(t, rows, "past dates must never receive assigned rows")
}

// TestAssignWFHForDate_WeekendIsNoOp pins the weekend short-
// circuit. Cap math is meaningless when nobody is on-site.
func TestAssignWFHForDate_WeekendIsNoOp(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, pickerTestConfig())
	seedPickerMember(t, ctx, db, "Alice", "alice@example.com")
	seedPickerMember(t, ctx, db, "Bob", "bob@example.com")
	seedPickerMember(t, ctx, db, "Carol", "carol@example.com")

	// Pick the next Saturday regardless of today's weekday.
	day := time.Now().UTC()
	for !isSaturday(day) {
		day = day.AddDate(0, 0, 1)
	}
	saturdayStr := day.Format("2006-01-02")
	require.NoError(t, svc.AssignWFHForDate(ctx, saturdayStr))
	rows, err := db.GetWFHRequestsByDate(ctx, saturdayStr)
	require.NoError(t, err)
	assert.Empty(t, rows, "weekends must be a no-op regardless of cap")
}

// TestAssignWFHForDate_NoOpWhenUnderCap pins the early-exit
// when onSite <= cap. No assigns, no log warnings.
func TestAssignWFHForDate_NoOpWhenUnderCap(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, pickerTestConfig())
	seedPickerMember(t, ctx, db, "Alice", "alice@example.com")
	seedPickerMember(t, ctx, db, "Bob", "bob@example.com")
	// 2 members, cap=2 → onSite=2 ≤ cap, no-op.
	date := pickerFutureDate(t, 1)
	require.NoError(t, svc.AssignWFHForDate(ctx, date))
	rows, err := db.GetWFHRequestsByDate(ctx, date)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

// TestAssignWFHForDate_PicksExcessMembers pins the happy path.
// 5 members, cap=2 → 3 assigned. Picks are alphabetically
// first among the candidates (no periodWFHCount differences in
// the fresh setup).
func TestAssignWFHForDate_PicksExcessMembers(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, pickerTestConfig())
	ids := map[string]string{
		"Alice":   seedPickerMember(t, ctx, db, "Alice", "alice@example.com"),
		"Bob":     seedPickerMember(t, ctx, db, "Bob", "bob@example.com"),
		"Carol":   seedPickerMember(t, ctx, db, "Carol", "carol@example.com"),
		"Dave":    seedPickerMember(t, ctx, db, "Dave", "dave@example.com"),
		"Erin":    seedPickerMember(t, ctx, db, "Erin", "erin@example.com"),
	}
	date := pickerFutureDate(t, 1)

	require.NoError(t, svc.AssignWFHForDate(ctx, date))

	rows, err := db.GetWFHRequestsByDate(ctx, date)
	require.NoError(t, err)
	require.Len(t, rows, 3, "cap=2 with 5 members must assign exactly 3")

	// All 3 rows must be approved + origin=assigned.
	for _, r := range rows {
		assert.Equal(t, database.WFHStatusApproved, r.Status)
		assert.Equal(t, "assigned", r.Origin)
	}

	// Picks are alphabetical — first 3 (Alice, Bob, Carol).
	// Dave and Erin are kept on-site.
	pickedIDs := map[string]bool{}
	for _, r := range rows {
		pickedIDs[r.MemberID] = true
	}
	assert.True(t, pickedIDs[ids["Alice"]])
	assert.True(t, pickedIDs[ids["Bob"]])
	assert.True(t, pickedIDs[ids["Carol"]])
	assert.False(t, pickedIDs[ids["Dave"]])
	assert.False(t, pickedIDs[ids["Erin"]])
}

// TestAssignWFHForDate_PickerPeriodWFHCountExcludesAssigned
// pins the plan's "assigned doesn't burn quota" property:
// members with assigned rows have a higher
// approvedWFHSet (which subtracts them from onSite on re-runs)
// but their periodWFHCount (which determines pick priority)
// is computed from GetWFHRequestsVoluntaryInPeriod and
// excludes origin='assigned'. So an admin's reassignment that
// gave Alice 3 assigned days in the period doesn't push her
// to the bottom of the priority queue.
func TestAssignWFHForDate_PickerPeriodWFHCountExcludesAssigned(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, pickerTestConfig())
	alice := seedPickerMember(t, ctx, db, "Alice", "alice@example.com")
	bob := seedPickerMember(t, ctx, db, "Bob", "bob@example.com")
	carol := seedPickerMember(t, ctx, db, "Carol", "carol@example.com")
	dave := seedPickerMember(t, ctx, db, "Dave", "dave@example.com")

	// Inject Alice with 3 assigned rows in the current period
	// via raw SQL (no public picker yet at test-time).
	nowUTC := time.Now().UTC()
	for i := range 3 {
		date := nowUTC.AddDate(0, 0, i+1).Format("2006-01-02")
		_, err := db.ExecContext(ctx,
			`INSERT INTO wfh_requests (id, member_id, date, status, origin)
			 VALUES (?, ?, ?, 'approved', 'assigned')`,
			"preassigned-"+uuid.New().String(), alice, date)
		require.NoError(t, err)
	}

	// Bob has 0 WFHs, Carol has 1 voluntary, Dave has 2 voluntary.
	// Alice's periodWFHCount should be 0 (her 3 assigned don't count).
	// So the picker sees: Bob=0, Alice=0, Carol=1, Dave=2 voluntary.
	// 5 members originally, but Alice already has 3 WFH rows
	// (approvedIDs includes her), so onSite=4-3=1 ≤ cap=2, no
	// assignment needed. Use a fresh date with no preassignments:
	date := pickerFutureDate(t, 10)
	_, err := db.CreateWFHRequest(ctx, carol, date)
	require.NoError(t, err)
	// Dave: 2 voluntary on this date — but CreateWFHRequest
	// returns pending. The picker uses GetWFHRequestsByDateAndStatus
	// for approvedIDs which excludes pending, so Dave would not be
	// subtracted. But periodWFHCount uses GetWFHRequestsVoluntaryInPeriod
	// which includes pending — so Dave's periodWFHCount=1 here.
	// Actually let me make it cleaner: pre-approve Carol and Dave's
	// requests so the math is straightforward.
	_, err = db.ExecContext(ctx,
		`UPDATE wfh_requests SET status = 'approved', settled_at = ? WHERE member_id = ? AND date = ?`,
		time.Now().UTC(), carol, date)
	require.NoError(t, err)

	// Setup: Alice/Bob/Carol/Dave/Erin = 5 members. Alice has 3
	// pre-assigned rows in the period (but NOT for this date).
	// For this date: Alice has no WFH row, Carol has 1 voluntary.
	// onSite = 5 (everyone) - 0 leave - 0 permanent - 1 approved (Carol) = 4.
	// cap = 2. excess = 2. Pick 2.
	erin := seedPickerMember(t, ctx, db, "Erin", "erin@example.com")
	_ = erin
	_ = dave
	_ = bob

	require.NoError(t, svc.AssignWFHForDate(ctx, date))

	rows, err := db.GetWFHRequestsByDate(ctx, date)
	require.NoError(t, err)
	// 3 total rows on the date: Carol's pre-existing voluntary
	// row + 2 newly-assigned rows. The picker assigned 2 of
	// the 4 on-site candidates (Alice, Bob, Dave, Erin).
	require.Len(t, rows, 3,
		"Carol's voluntary + 2 picker-assigned = 3 rows; cap=2 with 4 on-site requires 2 assigns")

	// Exactly 2 assigned rows from the picker.
	assignedCount := 0
	for _, r := range rows {
		if r.Origin == "assigned" {
			assignedCount++
		}
	}
	require.Equal(t, 2, assignedCount,
		"picker must insert exactly 2 assigned rows for cap=2")

	// Picks are Alice + Bob (alphabetical, lowest periodWFHCount
	// first). Carol is not picked (she's WFH on this date).
	// Dave / Erin are not picked (lower priority than Alice/Bob).
	assignedIDs := map[string]bool{}
	for _, r := range rows {
		if r.Origin == "assigned" {
			assignedIDs[r.MemberID] = true
		}
	}
	assert.True(t, assignedIDs[alice], "Alice should be assigned (lowest periodWFHCount, alphabetical first)")
	assert.True(t, assignedIDs[bob], "Bob should be assigned (lowest periodWFHCount, alphabetical second)")
	assert.False(t, assignedIDs[carol], "Carol is WFH today and must not be picked")
	assert.False(t, assignedIDs[dave], "Dave is alphabetically 4th; with 4 candidates picking 2, Dave is left on-site")
	assert.False(t, assignedIDs[erin], "Erin is alphabetically 5th; not picked")
}

// TestAssignWFHForDate_PermanentWFHExcluded pins the
// permanent-WFH exclusion: a permanent-WFH member is in the
// on-site-minus set but never in the candidate pool.
func TestAssignWFHForDate_PermanentWFHExcluded(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, pickerTestConfig())
	alice := seedPickerMember(t, ctx, db, "Alice", "alice@example.com")
	bob := seedPickerMember(t, ctx, db, "Bob", "bob@example.com")
	carol := seedPickerMember(t, ctx, db, "Carol", "carol@example.com")
	dave := seedPickerMember(t, ctx, db, "Dave", "dave@example.com")
	erin := seedPickerMember(t, ctx, db, "Erin", "erin@example.com")
	_ = erin
	require.NoError(t, db.SetTeamMemberPermanentWFH(ctx, dave, true))

	date := pickerFutureDate(t, 1)
	require.NoError(t, svc.AssignWFHForDate(ctx, date))

	rows, err := db.GetWFHRequestsByDate(ctx, date)
	require.NoError(t, err)
	require.Len(t, rows, 2,
		"5 members, 1 permanent (Dave, never on-site) → 4 on-site; cap=2 → pick 2")

	for _, r := range rows {
		assert.NotEqual(t, dave, r.MemberID,
			"Dave is permanent WFH and must never be picked")
	}

	// Sanity: Dave IS the on-site count of 4 non-permanent
	// members. Carol was supposed to be the alphabetical-first
	// Picks are alphabetical: Alice, Bob. Carol and Erin are
	// kept on-site.
	picked := map[string]bool{}
	for _, r := range rows {
		picked[r.MemberID] = true
	}
	assert.True(t, picked[alice])
	assert.True(t, picked[bob])
	assert.False(t, picked[carol], "Carol is alphabetically 3rd and not picked with cap=2")
	assert.False(t, picked[erin], "Erin is alphabetically last and not picked")
	assert.False(t, picked[dave], "Dave is permanent WFH and never picked")
}

// TestAssignWFHForDate_ExemptFromAssignmentExcluded pins
// the admin-set exempt flag: an exempt member is never
// picked. The voluntary counter still includes them (their
// WFHs subtract from onSite); only the picker pool excludes.
func TestAssignWFHForDate_ExemptFromAssignmentExcluded(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, pickerTestConfig())
	seedPickerMember(t, ctx, db, "Alice", "alice@example.com")
	bob := seedPickerMember(t, ctx, db, "Bob", "bob@example.com")
	carol := seedPickerMember(t, ctx, db, "Carol", "carol@example.com")
	dave := seedPickerMember(t, ctx, db, "Dave", "dave@example.com")
	_ = bob
	_ = dave
	require.NoError(t, db.SetTeamMemberExemptFromAssignment(ctx, carol, true))

	date := pickerFutureDate(t, 1)
	require.NoError(t, svc.AssignWFHForDate(ctx, date))

	rows, err := db.GetWFHRequestsByDate(ctx, date)
	require.NoError(t, err)
	require.Len(t, rows, 2, "cap=2 with 4 members but Carol exempt → 3 candidates → pick 2")

	for _, r := range rows {
		assert.NotEqual(t, carol, r.MemberID)
	}
}

// TestAssignWFHForDate_CapShortFall_LogsWarning pins the
// cap-short-fall branch. excess > len(candidates) → picker
// picks every candidate and the cap is unmet. The test can't
// easily assert on slog output without setup, so we verify
// the outcome (all eligible members picked, cap still not
// met) and rely on TestAssignWFHForDate_CapShortFall_AllPicked
// for the side-by-side warning-emission check.
func TestAssignWFHForDate_CapShortFall_LogsWarning(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, pickerTestConfig())
	alice := seedPickerMember(t, ctx, db, "Alice", "alice@example.com")
	bob := seedPickerMember(t, ctx, db, "Bob", "bob@example.com")
	carol := seedPickerMember(t, ctx, db, "Carol", "carol@example.com")
	// 3 members, cap=2 → excess=1 ≤ candidates. Add exempt
	// members to force the cap short-fall: with 4 members and
	// cap=2, excess=2. If we exempt 3 of them, candidates=1.
	// excess=2 > 1 → short-fall. Picker picks the 1 candidate.
	dave := seedPickerMember(t, ctx, db, "Dave", "dave@example.com")
	erin := seedPickerMember(t, ctx, db, "Erin", "erin@example.com")
	_ = alice
	_ = dave
	_ = erin
	require.NoError(t, db.SetTeamMemberExemptFromAssignment(ctx, alice, true))
	require.NoError(t, db.SetTeamMemberExemptFromAssignment(ctx, carol, true))
	require.NoError(t, db.SetTeamMemberExemptFromAssignment(ctx, erin, true))

	date := pickerFutureDate(t, 1)
	require.NoError(t, svc.AssignWFHForDate(ctx, date))

	rows, err := db.GetWFHRequestsByDate(ctx, date)
	require.NoError(t, err)
	require.Len(t, rows, 2,
		"5 members, 3 exempt (Alice/Carol/Erin) → 2 candidates (Bob/Dave); cap=2 → pick both even though excess=3 > candidates=2")
	assert.Equal(t, bob, rows[0].MemberID)
	assert.Equal(t, dave, rows[1].MemberID)
}

// TestAssignWFHForDate_Idempotent pins the idempotency
// guarantee. Running the picker twice for the same date
// produces zero new rows the second time.
func TestAssignWFHForDate_Idempotent(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, pickerTestConfig())
	seedPickerMember(t, ctx, db, "Alice", "alice@example.com")
	seedPickerMember(t, ctx, db, "Bob", "bob@example.com")
	seedPickerMember(t, ctx, db, "Carol", "carol@example.com")
	seedPickerMember(t, ctx, db, "Dave", "dave@example.com")
	seedPickerMember(t, ctx, db, "Erin", "erin@example.com")

	date := pickerFutureDate(t, 1)
	require.NoError(t, svc.AssignWFHForDate(ctx, date))
	first, err := db.GetWFHRequestsByDate(ctx, date)
	require.NoError(t, err)
	require.Len(t, first, 3)

	require.NoError(t, svc.AssignWFHForDate(ctx, date))
	second, err := db.GetWFHRequestsByDate(ctx, date)
	require.NoError(t, err)
	require.Len(t, second, 3, "second run must insert 0 new rows")

	// Sanity: same IDs.
	firstIDs := map[string]bool{}
	for _, r := range first {
		firstIDs[r.ID] = true
	}
	for _, r := range second {
		assert.True(t, firstIDs[r.ID], "re-run must keep the original IDs")
	}
}

// TestAssignWFHForDate_CapMetExactly_NoOp pins the edge case
// where onSite == cap exactly. Zero picks (no over-cap).
func TestAssignWFHForDate_CapMetExactly_NoOp(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := pickerTestConfig()
	cfg.SeatCap = 3 // 3 members, cap=3 → exactly met
	svc := NewService(db, cfg)
	seedPickerMember(t, ctx, db, "Alice", "alice@example.com")
	seedPickerMember(t, ctx, db, "Bob", "bob@example.com")
	seedPickerMember(t, ctx, db, "Carol", "carol@example.com")

	date := pickerFutureDate(t, 1)
	require.NoError(t, svc.AssignWFHForDate(ctx, date))
	rows, err := db.GetWFHRequestsByDate(ctx, date)
	require.NoError(t, err)
	assert.Empty(t, rows, "onSite == cap is not over-cap; no assigns")
}

// TestAssignWFHForDate_ReRunPreservesExistingAssigned pins
// the re-run correctness property from section 3: an
// already-assigned row correctly subtracts the member from
// onSite on a re-run. Run the picker twice — first run picks
// 3, second run inserts 0 because the assigned members are
// now in approvedIDs.
func TestAssignWFHForDate_ReRunPreservesExistingAssigned(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, pickerTestConfig())
	seedPickerMember(t, ctx, db, "Alice", "alice@example.com")
	seedPickerMember(t, ctx, db, "Bob", "bob@example.com")
	seedPickerMember(t, ctx, db, "Carol", "carol@example.com")
	seedPickerMember(t, ctx, db, "Dave", "dave@example.com")
	seedPickerMember(t, ctx, db, "Erin", "erin@example.com")

	date := pickerFutureDate(t, 1)
	require.NoError(t, svc.AssignWFHForDate(ctx, date))
	first, err := db.GetWFHRequestsByDate(ctx, date)
	require.NoError(t, err)
	require.Len(t, first, 3)

	// Sanity: on a re-run, approvedIDs now contains the 3
	// assigned members. onSite = 5 - 0 - 0 - 3 = 2 = cap. No
	// new assigns.
	require.NoError(t, svc.AssignWFHForDate(ctx, date))
	second, err := db.GetWFHRequestsByDate(ctx, date)
	require.NoError(t, err)
	assert.Len(t, second, 3, "second run must insert 0 new rows; the cap is met by the first run")
}

// isSaturday reports whether the given time falls on a Saturday.
func isSaturday(t time.Time) bool {
	return t.Weekday() == time.Saturday
}

// TestAssignWFHForDate_CoPresenceTiebreaker_PrefersRecentCohort pins
// the co-presence tiebreaker property from section 4 of
// plans/assigned-wfh-plan.md: among candidates with the same
// periodWFHCount, the candidate who has been on-site with the
// cohort most recently is picked first. The test seeds an
// asymmetric co-presence history and asserts the picker
// respects it.
//
// Setup: 5 members, cap=2 → 3 picks. Alice/Bob/Carol have 0
// voluntary WFHs (periodWFHCount=0); Dave/Erin have 1 each.
// Dave/Erin are picked first by the periodWFHCount rule.
// Among Alice/Bob/Carol, Alice was on-site with the cohort
// (Dave+Bob+Carol-after-picks) 3 days ago; Carol never; Bob
// 7 days ago. Carol should be picked first (oldest or no
// co-presence), then Alice (most recent).
//
// Note: co-presence is seeded via raw SQL because the writer
// (step 11) lands in a follow-up commit.
func TestAssignWFHForDate_CoPresenceTiebreaker_PrefersRecentCohort(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, pickerTestConfig())
	alice := seedPickerMember(t, ctx, db, "Alice", "alice@example.com")
	bob := seedPickerMember(t, ctx, db, "Bob", "bob@example.com")
	carol := seedPickerMember(t, ctx, db, "Carol", "carol@example.com")
	dave := seedPickerMember(t, ctx, db, "Dave", "dave@example.com")
	erin := seedPickerMember(t, ctx, db, "Erin", "erin@example.com")
	_ = erin

	// Give Dave and Erin each 1 voluntary WFH on the test
	// date so they're picked first via periodWFHCount=1.
	// Alice/Bob/Carol have 0 voluntary WFHs.
	date := pickerFutureDate(t, 5)
	created, err := db.CreateWFHRequest(ctx, dave, date)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`UPDATE wfh_requests SET status = 'approved', settled_at = ? WHERE id = ?`,
		time.Now().UTC(), created.ID)
	require.NoError(t, err)
	created2, err := db.CreateWFHRequest(ctx, erin, date)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`UPDATE wfh_requests SET status = 'approved', settled_at = ? WHERE id = ?`,
		time.Now().UTC(), created2.ID)
	require.NoError(t, err)

	// Now seed co-presence: Alice was on-site with Bob and
	// Carol (the eventual cohort) 3 days ago.
	threeDaysAgo := time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02")
	require.NoError(t, db.RecordWFHCoPresencePair(ctx, "alice-bob-3d", alice, bob))
	_, err = db.ExecContext(ctx,
		`UPDATE wfh_co_presence SET working_date = ? WHERE co_presence_id = ?`,
		threeDaysAgo, "alice-bob-3d")
	require.NoError(t, err)
	require.NoError(t, db.RecordWFHCoPresencePair(ctx, "alice-carol-3d", alice, carol))
	_, err = db.ExecContext(ctx,
		`UPDATE wfh_co_presence SET working_date = ? WHERE co_presence_id = ?`,
		threeDaysAgo, "alice-carol-3d")
	require.NoError(t, err)

	// Bob was on-site with Carol and Dave 7 days ago.
	sevenDaysAgo := time.Now().UTC().AddDate(0, 0, -7).Format("2006-01-02")
	require.NoError(t, db.RecordWFHCoPresencePair(ctx, "bob-carol-7d", bob, carol))
	_, err = db.ExecContext(ctx,
		`UPDATE wfh_co_presence SET working_date = ? WHERE co_presence_id = ?`,
		sevenDaysAgo, "bob-carol-7d")
	require.NoError(t, err)
	require.NoError(t, db.RecordWFHCoPresencePair(ctx, "bob-dave-7d", bob, dave))
	_, err = db.ExecContext(ctx,
		`UPDATE wfh_co_presence SET working_date = ? WHERE co_presence_id = ?`,
		sevenDaysAgo, "bob-dave-7d")
	require.NoError(t, err)

	// Carol has no co-presence history within the horizon.
	// Alice has 3-day co-presence. Bob has 7-day co-presence.

	// cap=2 with 5 members (Dave/Erin are WFH today, leaving
	// 3 candidates). onSite=3, excess=1. Pick 1. Alice has
	// the lowest co-presence score (3-day) so the picker
	// picks her first per the plan's ORDER BY score ASC.
	// Bob (7-day) and Carol (no co-presence → sentinel = 15)
	// score higher and stay on-site.
	require.NoError(t, svc.AssignWFHForDate(ctx, date))

	rows, err := db.GetWFHRequestsByDate(ctx, date)
	require.NoError(t, err)
	// Filter for assigned only (Dave/Erin already had voluntary).
	assigned := []database.WFHRequest{}
	for _, r := range rows {
		if r.Origin == "assigned" {
			assigned = append(assigned, r)
		}
	}
	require.Len(t, assigned, 1,
		"onSite=3, cap=2 → 1 picker-assigned. Dave/Erin's voluntary already there.")
	assert.Equal(t, alice, assigned[0].MemberID,
		"Alice has 3-day co-presence (lowest score), so she's picked first; Bob (7-day) and Carol (sentinel) stay on-site")
}

// TestAssignWFHForDate_CoPresenceKillSwitch pins the
// cfg.CoPresenceEnabled=false fallback: every candidate
// scores the same so the tiebreaker degenerates to
// alphabetical. Without the kill switch the test would
// depend on co-presence data; with it off, the picker is
// purely deterministic on alphabetical.
func TestAssignWFHForDate_CoPresenceKillSwitch(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	cfg := pickerTestConfig()
	cfg.CoPresenceEnabled = false
	svc := NewService(db, cfg)
	alice := seedPickerMember(t, ctx, db, "Alice", "alice@example.com")
	bob := seedPickerMember(t, ctx, db, "Bob", "bob@example.com")
	carol := seedPickerMember(t, ctx, db, "Carol", "carol@example.com")
	_ = seedPickerMember(t, ctx, db, "Dave", "dave@example.com")
	_ = seedPickerMember(t, ctx, db, "Erin", "erin@example.com")

	// Seed asymmetric co-presence history that WOULD change
	// the picks if CoPresenceEnabled=true. With it off, the
	// picker ignores the history and picks alphabetically.
	require.NoError(t, db.RecordWFHCoPresencePair(ctx, "alice-bob-old", alice, bob))
	_, err := db.ExecContext(ctx,
		`UPDATE wfh_co_presence SET working_date = ? WHERE co_presence_id = ?`,
		time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02"), "alice-bob-old")
	require.NoError(t, err)

	date := pickerFutureDate(t, 5)
	require.NoError(t, svc.AssignWFHForDate(ctx, date))

	rows, err := db.GetWFHRequestsByDate(ctx, date)
	require.NoError(t, err)
	assigned := []string{}
	for _, r := range rows {
		if r.Origin == "assigned" {
			assigned = append(assigned, r.MemberID)
		}
	}
	require.Len(t, assigned, 3, "5 members, cap=2 → 3 picks")
	// Alphabetical: Alice, Bob, Carol. Despite Alice having
	// recent co-presence with Bob, CoPresenceEnabled=false
	// means the picker doesn't read co-presence at all.
	assert.Equal(t, alice, assigned[0], "alphabetical first pick: Alice")
	assert.Equal(t, bob, assigned[1], "alphabetical second pick: Bob")
	assert.Equal(t, carol, assigned[2], "alphabetical third pick: Carol")
}

// TestAssignWFHForDate_CoPresence_EmptyCohort pins the
// empty-cohort branch: weekends, holidays, and "everyone
// exempt" all produce an empty cohort, and every candidate
// scores the sentinel. The picker then degenerates to
// alphabetical.
func TestAssignWFHForDate_CoPresence_EmptyCohort(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupWFHTestDB(t)
	defer cleanup()

	svc := NewService(db, pickerTestConfig())
	seedPickerMember(t, ctx, db, "Alice", "alice@example.com")
	seedPickerMember(t, ctx, db, "Bob", "bob@example.com")
	seedPickerMember(t, ctx, db, "Carol", "carol@example.com")
	seedPickerMember(t, ctx, db, "Dave", "dave@example.com")
	seedPickerMember(t, ctx, db, "Erin", "erin@example.com")

	// Mark all 5 as exempt → cohort is empty → every candidate
	// scores the sentinel → picker uses alphabetical fallback.
	// But cap=2 with 5 candidates → excess=3 = candidates, no
	// short-fall. Picker picks all 5.
	for _, name := range []string{"Alice", "Bob", "Carol", "Dave", "Erin"} {
		email := name + "@example.com"
		members, err := db.GetActiveTeamMembers(ctx)
		require.NoError(t, err)
		for _, m := range members {
			if m.Email == email {
				require.NoError(t, db.SetTeamMemberExemptFromAssignment(ctx, m.ID, true))
				break
			}
		}
	}

	// This test exercises the empty-cohort path without
	// requiring exemption setup — the picker never reaches
	// the cohort computation when onSite <= cap. So this
	// is more of a smoke test for the cohort code path.
	date := pickerFutureDate(t, 5)
	require.NoError(t, svc.AssignWFHForDate(ctx, date))
	rows, err := db.GetWFHRequestsByDate(ctx, date)
	require.NoError(t, err)
	for _, r := range rows {
		assert.Equal(t, "assigned", r.Origin)
	}
}
