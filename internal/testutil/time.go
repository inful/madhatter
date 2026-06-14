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
