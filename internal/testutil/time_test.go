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
