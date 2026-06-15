package rota

import (
	"context"
	"testing"
	"time"

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

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	carlaID, err := db.AddTeamMember(ctx, "Carla", "carla@example.com")
	require.NoError(t, err)
	dariaID, err := db.AddTeamMember(ctx, "Daria", "daria@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)
	maintenance := NewScheduleMaintenance(db)

	// Two full weeks of schedule, Mon–Fri only.
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC) // Mon
	endDate := time.Date(2024, 1, 26, 0, 0, 0, 0, time.UTC)   // Fri
	require.NoError(t, engine.GenerateSchedule(ctx, startDate, endDate))

	// Both Bob and Daria are on leave for the entire window.
	for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		dateStr := d.Format("2006-01-02")
		_, err = db.CreateLeaveRecord(ctx, bobID, dateStr, dateStr)
		require.NoError(t, err)
		_, err = db.CreateLeaveRecord(ctx, dariaID, dateStr, dateStr)
		require.NoError(t, err)
	}

	// Materialize the rota (assigns covers for every leave).
	leaves, err := db.GetLeaveRecords(ctx)
	require.NoError(t, err)
	for _, l := range leaves {
		require.NoError(t, maintenance.HandleLeaveChange(ctx, l.ID))
	}

	// Walk business days and record the HAT for each. The HAT is the
	// original if present, otherwise the cover.
	hatFor := map[string]string{} // date -> member name
	coverFor := map[string]string{}
	originalFor := map[string]string{}

	// For each day, also log all leaves on that date so we can see
	// exactly which ones the engine considered "on leave" when
	// picking the cover.
	leavesByDate := map[string][]string{}
	names := map[string]string{
		aliceID: "Alice",
		bobID:   "Bob",
		carlaID: "Carla",
		dariaID: "Daria",
	}
	for _, l := range leaves {
		dateStr := l.StartDate.Format("2006-01-02")
		leavesByDate[dateStr] = append(leavesByDate[dateStr], names[l.MemberID])
	}

	for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		dateStr := d.Format("2006-01-02")
		assignments, err := db.GetAssignmentsByDate(ctx, dateStr)
		require.NoError(t, err)
		// The HAT is the cover if there is one (the original is on
		// leave), else the original. Pick explicitly rather than
		// taking the last-iterated row, which would depend on the DB
		// implementation's iteration order.
		var hat, cover, original string
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
		require.NotEmpty(t, hat, "%s has no HAT (no original or cover)", dateStr)
		hatFor[dateStr] = hat
		coverFor[dateStr] = cover
		originalFor[dateStr] = original
	}

	// Print the schedule so the failure is debuggable.
	t.Logf("HAT schedule (Bob and Daria on leave the whole period):")
	for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		dateStr := d.Format("2006-01-02")
		t.Logf("  %s %s: original=%s cover=%s HAT=%s leaves_on_date=%v", dateStr, d.Weekday().String()[:3], originalFor[dateStr], coverFor[dateStr], hatFor[dateStr], leavesByDate[dateStr])
	}

	// Check that no remaining employee (Alice, Carla) has more than
	// 2 consecutive HAT days.
	streak := map[string]int{} // member name -> current streak length
	for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		dateStr := d.Format("2006-01-02")
		who := hatFor[dateStr]
		if who == "Bob" || who == "Daria" {
			// Bob and Daria shouldn't be HAT since they're on leave,
			// but the cover path might surface them as originals on
			// days they aren't on leave. We only check the surviving
			// employees.
			streak[who] = 0
			continue
		}
		// Reset other people's streaks.
		for k := range streak {
			if k != who {
				streak[k] = 0
			}
		}
		streak[who]++
		require.LessOrEqualf(t, streak[who], 2,
			"%s did HAT on %s (consecutive run of %d); expected at most 2",
			who, dateStr, streak[who])
	}
}
