package rota

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/require"
)

// TestEngine_CoverRotationFairnessOverYear is a fairness test for the
// cover rotation. It generates a year of random work:
//   - 5 team members
//   - Each member gets the same total leave days (sameTotalLeavePerMember),
//     split into random chunks of 1-5 days at random times
//   - Some random holidays scattered across the year
//
// It then runs the full assignment pipeline (generate schedule +
// process leaves) and asserts that the HAT-day distribution across
// the team is close — no member should have dramatically more or
// fewer HAT days than the fair share.
//
// This is the property the cover rotation is designed to provide:
// covers are distributed fairly across the team regardless of the
// leave pattern. If the rotation regresses to the "always Alice"
// failure mode, max-min HAT days will explode and this test will
// fail.
func TestEngine_CoverRotationFairnessOverYear(t *testing.T) {
	const (
		teamSize                = 5
		sameTotalLeavePerMember = 20 // each member takes 20 days off, total 100 leave-days
		maxLeaveChunk           = 5
		numHolidays             = 10

		// 4% of the year is the fairness tolerance. With 5 members
		// and ~252 HAT days, a ±5 swing from the fair share of ~50
		// is a tight bound that the deterministic state-based
		// rotation should satisfy; the old "always Alice" failure
		// mode would push it to 50+.
		maxHatSpread = 10
	)

	rng := rand.New(rand.NewSource(42)) //nolint:gosec // test fixture, not security-sensitive

	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	memberNames := []string{"Alice", "Bob", "Charlie", "Daria", "Eve"}
	memberIDs := seedFiveMemberTeam(t, ctx, db, memberNames)

	holidaySet := pickRandomHolidays(rng, 2024, numHolidays)
	engine := NewEngine(db)
	engine.SetHolidayChecker(func(d time.Time) bool {
		return holidaySet[d.Format("2006-01-02")]
	})
	maintenance := NewScheduleMaintenance(db)
	maintenance.SetHolidayChecker(engine.holidayChecker)

	yearStart := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	yearEnd := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	for _, memberID := range memberIDs {
		require.NoError(t, generateRandomLeaves(rng, db, memberID, yearStart, yearEnd, sameTotalLeavePerMember, maxLeaveChunk, holidaySet))
	}

	require.NoError(t, engine.GenerateSchedule(ctx, yearStart, yearEnd))
	leaves, err := db.GetLeaveRecords(ctx)
	require.NoError(t, err)
	for _, l := range leaves {
		require.NoError(t, maintenance.HandleLeaveChange(ctx, l.ID))
	}

	hatCount := tallyHatDays(t, ctx, db, yearStart, yearEnd, holidaySet)
	logHatDistribution(t, memberNames, memberIDs, hatCount)
	assertFairHatSpread(t, memberIDs, hatCount, maxHatSpread)
	assertTotalHatCount(t, hatCount)
}

// seedFiveMemberTeam adds teamSize members to the DB and returns
// their IDs in the same order as the names.
func seedFiveMemberTeam(t *testing.T, ctx context.Context, db teamMemberAdder, names []string) []string {
	t.Helper()
	ids := make([]string, len(names))
	for i, name := range names {
		id, err := db.AddTeamMember(ctx, name, name+"@example.com")
		require.NoError(t, err)
		ids[i] = id
	}
	return ids
}

// teamMemberAdder is the minimal subset of *database.DB that the
// leaf generator and member seeder need. Defining it as an
// interface lets the helpers stay loosely coupled and makes them
// easy to unit-test in isolation.
type teamMemberAdder interface {
	AddTeamMember(ctx context.Context, name, email string) (string, error)
}

// tallyHatDays walks the calendar and counts HAT days per member.
// The HAT for a given day is the cover if one was created, else
// the original assignment. Weekends and holidays are excluded.
func tallyHatDays(t *testing.T, ctx context.Context, db assignmentReader, yearStart, yearEnd time.Time, holidaySet map[string]bool) map[string]int {
	t.Helper()
	hatCount := make(map[string]int)
	for d := yearStart; !d.After(yearEnd); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		if holidaySet[d.Format("2006-01-02")] {
			continue
		}
		hat := hatForDate(t, ctx, db, d.Format("2006-01-02"))
		if hat != "" {
			hatCount[hat]++
		}
	}
	return hatCount
}

// assignmentReader is the minimal subset of *database.DB that
// tallyHatDays needs.
type assignmentReader interface {
	GetAssignmentsByDate(ctx context.Context, date string) ([]database.RotaAssignment, error)
}

// hatForDate returns the HAT member ID for the given date, or "" if
// the date has no assignment. Iterating the assignments is
// deterministic in test fixtures (the DB returns them in
// insertion order), so we pick the cover if present, else the
// original.
func hatForDate(t *testing.T, ctx context.Context, db assignmentReader, date string) string {
	t.Helper()
	assignments, err := db.GetAssignmentsByDate(ctx, date)
	require.NoError(t, err)
	var hat string
	for _, a := range assignments {
		if a.IsCover {
			hat = a.MemberID
			break
		}
		hat = a.MemberID
	}
	return hat
}

func logHatDistribution(t *testing.T, names []string, ids []string, hatCount map[string]int) {
	t.Helper()
	for _, id := range ids {
		name := names[indexOf(ids, id)]
		t.Logf("%s: %d HAT days", name, hatCount[id])
	}
}

func assertFairHatSpread(t *testing.T, ids []string, hatCount map[string]int, maxHatSpread int) {
	t.Helper()
	minHat, maxHat := -1, -1
	for _, id := range ids {
		c := hatCount[id]
		if minHat == -1 || c < minHat {
			minHat = c
		}
		if maxHat == -1 || c > maxHat {
			maxHat = c
		}
	}
	spread := maxHat - minHat
	t.Logf("HAT spread: min=%d, max=%d, spread=%d (tolerance=%d)", minHat, maxHat, spread, maxHatSpread)
	require.LessOrEqualf(t, spread, maxHatSpread,
		"HAT days should be distributed fairly across the team; "+
			"min=%d, max=%d, spread=%d > tolerance %d",
		minHat, maxHat, spread, maxHatSpread)
}

func assertTotalHatCount(t *testing.T, hatCount map[string]int) {
	t.Helper()
	total := 0
	for _, c := range hatCount {
		total += c
	}
	require.GreaterOrEqual(t, total, 240, "total HAT days should be close to working days in the year")
}

// generateRandomLeaves splits sameTotalLeavePerMember into random
// chunks of 1-maxChunkLen business days at random start dates
// within [yearStart, yearEnd], skipping holidays and weekends. Each
// chunk is a separate leave record.
func generateRandomLeaves(rng *rand.Rand, db leaveCreator, memberID string, yearStart, yearEnd time.Time, sameTotalLeavePerMember, maxChunkLen int, holidaySet map[string]bool) error {
	type leaveDate struct{ start, end time.Time }
	var chunks []leaveDate
	remaining := sameTotalLeavePerMember

	for remaining > 0 {
		chunkLen := min(1+rng.Intn(maxChunkLen), remaining)

		latestStart := yearEnd.AddDate(0, 0, -chunkLen+1)
		daysRange := int(latestStart.Sub(yearStart).Hours() / 24)
		if daysRange <= 0 {
			// Year is too short for another chunk — stop.
			break
		}

		start := pickChunkStart(rng, yearStart, daysRange, chunkLen, holidaySet)
		if start.IsZero() {
			// Could not place this chunk — burn the budget and
			// continue so the loop can terminate.
			remaining -= chunkLen
			continue
		}
		chunks = append(chunks, leaveDate{start: start, end: start.AddDate(0, 0, chunkLen-1)})
		remaining -= chunkLen
	}

	for _, c := range chunks {
		if _, err := db.CreateLeaveRecord(context.Background(), memberID, c.start.Format("2006-01-02"), c.end.Format("2006-01-02"), database.LeaveTypeLeave); err != nil {
			return err
		}
	}
	return nil
}

// leaveCreator is the minimal subset of *database.DB that
// generateRandomLeaves needs.
type leaveCreator interface {
	CreateLeaveRecord(ctx context.Context, memberID, startDate, endDate, leaveType string) (string, error)
}

// pickChunkStart tries up to 50 times to find a random business day
// such that the next chunkLen-1 days are also business days.
// Returns the zero time if no valid start could be found.
func pickChunkStart(rng *rand.Rand, yearStart time.Time, daysRange, chunkLen int, holidaySet map[string]bool) time.Time {
	for range 50 {
		candidate := yearStart.AddDate(0, 0, rng.Intn(daysRange+1))
		if !isBusinessDay(candidate, holidaySet) {
			continue
		}
		ok := true
		for i := 1; i < chunkLen; i++ {
			next := candidate.AddDate(0, 0, i)
			if !isBusinessDay(next, holidaySet) {
				ok = false
				break
			}
		}
		if ok {
			return candidate
		}
	}
	return time.Time{}
}

func isBusinessDay(d time.Time, holidaySet map[string]bool) bool {
	if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		return false
	}
	if holidaySet[d.Format("2006-01-02")] {
		return false
	}
	return true
}

// pickRandomHolidays returns a set of n random weekdays in year, used
// as a fake holiday calendar for the test.
func pickRandomHolidays(rng *rand.Rand, year, n int) map[string]bool {
	holidays := make(map[string]bool)
	for len(holidays) < n {
		day := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, rng.Intn(366))
		if day.Weekday() != time.Saturday && day.Weekday() != time.Sunday {
			holidays[day.Format("2006-01-02")] = true
		}
	}
	return holidays
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
