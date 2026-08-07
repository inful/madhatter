// Package testutil provides shared helpers for the project's test files.
//
// Helpers in this package are intentionally small and dependency-free so
// they can be imported from any *_test.go file across the project without
// pulling in extra transitive imports.
package testutil

import "time"

// NextBusinessDay returns midnight (00:00:00) on the next weekday
// (Monday–Friday) that is the same as or after the given time. The
// returned time is in the input's location.
//
// Use this when a test needs a date that is guaranteed not to be a
// weekend — for example, when computing a future WFH day or a settlement
// cutoff. Time-of-day is normalised to midnight so callers can safely
// chain AddDate / Format("2006-01-02") without TZ surprises.
func NextBusinessDay(from time.Time) time.Time {
	date := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	for date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
		date = date.AddDate(0, 0, 1)
	}
	return date
}

// NextBusinessDays returns n business days strictly in the future of
// from, returned in ascending order. The dates are guaranteed distinct
// from each other regardless of which weekday from lands on.
//
// This is the safe alternative to the pattern
//
//	day1 := NextBusinessDay(time.Now().AddDate(0, 0, K))
//	day2 := NextBusinessDay(time.Now().AddDate(0, 0, K+1))
//
// which collapses to the same date when K crosses a weekend boundary:
// e.g. with today=Friday, both K=1 (Saturday→Mon) and K=2 (Sunday→Mon)
// resolve to the same Monday, and tests that assert "two distinct
// business days" trip UNIQUE constraints downstream. The legacy
// time.Now().AddDate(0, 0, K) + NextBusinessDay pattern is the
// flake surface the helper was added to replace.
//
// The cursor advances one calendar day at a time and snaps each step
// forward to the next business day, so the minimum gap between any
// two returned dates is one business day.
func NextBusinessDays(from time.Time, n int) []time.Time {
	if n <= 0 {
		return nil
	}
	out := make([]time.Time, 0, n)
	cursor := NextBusinessDay(from.AddDate(0, 0, 1))
	for range n {
		out = append(out, cursor)
		cursor = NextBusinessDay(cursor.AddDate(0, 0, 1))
	}
	return out
}
