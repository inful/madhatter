// Package envutil provides tiny helpers for parsing environment
// variables into typed Go values. Each helper follows the same
// contract:
//
//   - empty/unset → default value
//   - a string that stdlib strconv / time.ParseDuration rejects → default value
//   - otherwise → the parsed value
//
// The silent fallback keeps existing config-loading code paths
// backwards-compatible (a typo in a deployment env var should never
// hard-fail the boot).
//
// This package replaces three near-identical copies that lived in
// internal/wfh, internal/notify, and internal/auth before the
// review-driven dedup.
package envutil

import (
	"os"
	"strconv"
	"time"
)

// Bool reads key as a bool. Falls back to def on missing /
// unparseable values. Accepts the same syntax as strconv.ParseBool.
func Bool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// Int reads key as an int via strconv.Atoi. Falls back to def on
// missing / unparseable values.
func Int(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}

// Float64 reads key as a float64 via strconv.ParseFloat. Falls back to
// def on missing / unparseable values.
func Float64(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

// String reads key as a string. Falls back to def on missing / empty
// values. Use this for free-form strings such as URLs, scopes, and
// from-addresses that have no richer schema.
func String(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v
}

// Duration reads key as a time.Duration via time.ParseDuration.
// Falls back to def on missing / unparseable values. Accepts the
// same syntax as time.ParseDuration ("5s", "1m30s", "2h", …).
func Duration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
