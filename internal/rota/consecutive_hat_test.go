package rota

import (
	"context"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/require"
)

// TestCoverRotation_NoThreeConsecutiveHATDays is a regression test for
// a bug in the cover-assignment rotation. Setup: 4 team members
// (Alice, Bob, Carla, Daria), with Bob and Daria on leave for the
// entire test period. The remaining two (Alice, Carla) become the
// only candidates for the HAT day, and the rotation must distribute
// the load between them so that neither does more than two
// consecutive HAT days — true originals or covers.
//
// The test fails the first time the bug shows up: a third consecutive
// HAT day for Alice or Carla, in any contiguous window.
func TestCoverRotation_NoThreeConsecutiveHATDays(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	memberNames := []string{"Alice", "Bob", "Carla", "Daria"}
	ids := seedTeam(t, ctx, db, memberNames)
	names := nameByID(ids, memberNames)

	engine := NewEngine(db)
	maintenance := NewScheduleMaintenance(db)

	// Two full weeks of schedule, Mon–Fri only.
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC) // Mon
	endDate := time.Date(2024, 1, 26, 0, 0, 0, 0, time.UTC)   // Fri
	require.NoError(t, engine.GenerateSchedule(ctx, startDate, endDate))

	// Both Bob and Daria are on leave for the entire window.
	createDailyLeaves(t, ctx, db, ids[1], startDate, endDate)
	createDailyLeaves(t, ctx, db, ids[3], startDate, endDate)

	leaves, err := db.GetLeaveRecords(ctx)
	require.NoError(t, err)
	for _, l := range leaves {
		require.NoError(t, maintenance.HandleLeaveChange(ctx, l.ID))
	}

	hatFor, coverFor, originalFor, leavesByDate := buildScheduleView(t, ctx, db, startDate, endDate, leaves, names)
	logSchedule(t, startDate, endDate, hatFor, coverFor, originalFor, leavesByDate)

	// Bob and Daria are on leave the whole period, so they shouldn't
	// be HAT — but the cover path can surface them as originals on
	// days they aren't on leave. We only check the surviving
	// employees for the "no three consecutive" property.
	assertNoLongHATStreak(t, startDate, endDate, hatFor, map[string]bool{
		names[ids[1]]: true, // Bob
		names[ids[3]]: true, // Daria
	})
}

// seedTeam adds one member per name and returns the IDs in the same
// order.
func seedTeam(t *testing.T, ctx context.Context, db *database.DB, names []string) []string {
	t.Helper()
	ids := make([]string, len(names))
	for i, name := range names {
		id, err := db.AddTeamMember(ctx, name, name+"@example.com")
		require.NoError(t, err)
		ids[i] = id
	}
	return ids
}

// nameByID returns a map from member ID to display name, in the
// order produced by seedTeam.
func nameByID(ids, names []string) map[string]string {
	out := make(map[string]string, len(ids))
	for i, id := range ids {
		out[id] = names[i]
	}
	return out
}

// createDailyLeaves adds a single-day leave for memberID on every
// business day in [start, end]. Weekends are skipped because the
// rota has no original assignments on weekends for the cover to
// replace.
func createDailyLeaves(t *testing.T, ctx context.Context, db *database.DB, memberID string, start, end time.Time) {
	t.Helper()
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if isWeekend(d) {
			continue
		}
		dateStr := d.Format("2006-01-02")
		_, err := db.CreateLeaveRecord(ctx, memberID, dateStr, dateStr)
		require.NoError(t, err)
	}
}

// buildScheduleView walks the date range, recording the HAT, cover,
// and original member for each business day, and the set of members
// on leave for that day.
func buildScheduleView(
	t *testing.T,
	ctx context.Context,
	db *database.DB,
	start, end time.Time,
	leaves []database.LeaveRecord,
	names map[string]string,
) (hatFor, coverFor, originalFor map[string]string, leavesByDate map[string][]string) {
	t.Helper()
	leavesByDate = indexLeavesByDate(leaves, names)

	hatFor = make(map[string]string)
	coverFor = make(map[string]string)
	originalFor = make(map[string]string)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if isWeekend(d) {
			continue
		}
		dateStr := d.Format("2006-01-02")
		hat, cover, original := readDayAssignments(t, ctx, db, dateStr, names)
		require.NotEmptyf(t, hat, "%s has no HAT (no original or cover)", dateStr)
		hatFor[dateStr] = hat
		coverFor[dateStr] = cover
		originalFor[dateStr] = original
	}
	return hatFor, coverFor, originalFor, leavesByDate
}

func isWeekend(d time.Time) bool {
	return d.Weekday() == time.Saturday || d.Weekday() == time.Sunday
}

// readDayAssignments reads the assignments for a single day and
// resolves the HAT, cover, and original member. The HAT is the
// cover if one exists, else the original.
func readDayAssignments(t *testing.T, ctx context.Context, db *database.DB, date string, names map[string]string) (hat, cover, original string) {
	t.Helper()
	assignments, err := db.GetAssignmentsByDate(ctx, date)
	require.NoError(t, err)
	for _, a := range assignments {
		if a.IsCover {
			cover = names[a.MemberID]
		} else {
			original = names[a.MemberID]
		}
	}
	hat = cover
	if hat == "" {
		hat = original
	}
	return hat, cover, original
}

func indexLeavesByDate(leaves []database.LeaveRecord, names map[string]string) map[string][]string {
	out := make(map[string][]string)
	for i := range leaves {
		dateStr := leaves[i].StartDate.Format("2006-01-02")
		out[dateStr] = append(out[dateStr], names[leaves[i].MemberID])
	}
	return out
}

func logSchedule(t *testing.T, start, end time.Time, hatFor, coverFor, originalFor map[string]string, leavesByDate map[string][]string) {
	t.Helper()
	t.Logf("HAT schedule (Bob and Daria on leave the whole period):")
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if isWeekend(d) {
			continue
		}
		dateStr := d.Format("2006-01-02")
		t.Logf("  %s %s: original=%s cover=%s HAT=%s leaves_on_date=%v",
			dateStr, d.Weekday().String()[:3],
			originalFor[dateStr], coverFor[dateStr], hatFor[dateStr], leavesByDate[dateStr])
	}
}

// assertNoLongHATStreak walks the date range and asserts that no
// member (other than the ones in ignoredMembers) ever has more
// than 2 consecutive HAT days. The streak resets on any non-matching
// day, so a run of N same-member HATs followed by a different
// member counts as one streak of length N.
func assertNoLongHATStreak(t *testing.T, start, end time.Time, hatFor map[string]string, ignoredMembers map[string]bool) {
	t.Helper()
	const maxStreak = 2
	streak := make(map[string]int)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if isWeekend(d) {
			continue
		}
		dateStr := d.Format("2006-01-02")
		who := hatFor[dateStr]
		if ignoredMembers[who] {
			streak[who] = 0
			continue
		}
		for k := range streak {
			if k != who {
				streak[k] = 0
			}
		}
		streak[who]++
		require.LessOrEqualf(t, streak[who], maxStreak,
			"%s did HAT on %s (consecutive run of %d); expected at most %d",
			who, dateStr, streak[who], maxStreak)
	}
}
