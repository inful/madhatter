package testutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNextBusinessDay(t *testing.T) {
	mustParse := func(s string) time.Time {
		t.Helper()
		v, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return v
	}

	// Monday stays Monday.
	assert.Equal(t, "2026-01-05", NextBusinessDay(mustParse("2026-01-05")).Format("2006-01-02"))

	// Friday stays Friday.
	assert.Equal(t, "2026-01-09", NextBusinessDay(mustParse("2026-01-09")).Format("2006-01-02"))

	// Saturday rolls to Monday.
	assert.Equal(t, "2026-01-12", NextBusinessDay(mustParse("2026-01-10")).Format("2006-01-02"))

	// Sunday rolls to Monday.
	assert.Equal(t, "2026-01-12", NextBusinessDay(mustParse("2026-01-11")).Format("2006-01-02"))

	// Time-of-day is normalised to midnight in the input's location.
	in := time.Date(2026, 1, 10, 14, 30, 45, 0, time.UTC)
	out := NextBusinessDay(in)
	assert.Equal(t, 0, out.Hour())
	assert.Equal(t, 0, out.Minute())
	assert.Equal(t, 0, out.Second())
	assert.Equal(t, time.UTC, out.Location())

	// Location is preserved when input is in a non-UTC zone.
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Skipf("Asia/Tokyo unavailable: %v", err)
	}
	inTokyo := time.Date(2026, 1, 10, 14, 30, 0, 0, tokyo)
	outTokyo := NextBusinessDay(inTokyo)
	assert.Equal(t, tokyo, outTokyo.Location())
	assert.Equal(t, "2026-01-12", outTokyo.Format("2006-01-02"))
}

// TestNextBusinessDays_AlwaysDistinct is the regression guard for the
// weekend-collapse bug that bit TestLoadCurrentUserPresenceStatus_
// AllLeaveDays_NoNextHAT. When today is Friday, the legacy pattern
//
//	NextBusinessDay(today + 1) // Sat → Mon
//	NextBusinessDay(today + 2) // Sun → Mon
//
// produces the SAME date for both calls — a UNIQUE-constraint
// violation downstream. The helper must always return distinct
// dates regardless of which weekday today lands on.
func TestNextBusinessDays_AlwaysDistinct(t *testing.T) {
	mustParse := func(s string) time.Time {
		t.Helper()
		v, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return v
	}

	// One full week of distinct starting weekdays — covers the
	// surface that the flaky test exposed on Friday.
	for _, startDate := range []string{
		"2026-01-05", // Mon
		"2026-01-06", // Tue
		"2026-01-07", // Wed
		"2026-01-08", // Thu
		"2026-01-09", // Fri
		"2026-01-10", // Sat
		"2026-01-11", // Sun
	} {
		start := mustParse(startDate)
		got := NextBusinessDays(start, 3)

		// Three dates must be returned, all strictly in the future of
		// the input (the first one is tomorrow's business day), and
		// pairwise distinct.
		assert.Len(t, got, 3, "from %s (weekday %s)", startDate, start.Weekday())
		assert.True(t, got[0].After(start),
			"from %s: first business day must be strictly in the future, got %s",
			startDate, got[0].Format("2006-01-02"))

		for i := range got {
			for j := i + 1; j < len(got); j++ {
				assert.False(t, got[i].Equal(got[j]),
					"from %s: days must be pairwise distinct, got %s and %s collide",
					startDate,
					got[i].Format("2006-01-02"),
					got[j].Format("2006-01-02"))
			}
		}

		// Every returned date must be a weekday — never Saturday or
		// Sunday. (NextBusinessDay's contract.)
		for _, d := range got {
			assert.NotEqual(t, time.Saturday, d.Weekday(),
				"from %s: returned Saturday %s", startDate, d.Format("2006-01-02"))
			assert.NotEqual(t, time.Sunday, d.Weekday(),
				"from %s: returned Sunday %s", startDate, d.Format("2006-01-02"))
		}
	}
}

func TestNextBusinessDays_EdgeCases(t *testing.T) {
	mustParse := func(s string) time.Time {
		t.Helper()
		v, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return v
	}

	// n=0 and n<0 return nil so callers don't have to special-case
	// "I don't actually need a date" call sites.
	assert.Nil(t, NextBusinessDays(mustParse("2026-01-05"), 0))
	assert.Nil(t, NextBusinessDays(mustParse("2026-01-05"), -3))

	// n=1 returns a single-element slice, which is the common
	// "tomorrow's business day" case.
	one := NextBusinessDays(mustParse("2026-01-09"), 1)
	assert.Len(t, one, 1)
	assert.Equal(t, "2026-01-12", one[0].Format("2006-01-02"),
		"Friday + 1 business day = Monday")
}
