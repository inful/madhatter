package web

import (
	"embed"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed assets/js/fun-effects.js
var funEffectsJS embed.FS

// TestFireBirthdayBurst_LastsAtLeastTwiceAsLong pins the
// duration of the birthday confetti blast. The user-reported
// issue: the original recipe (1 setTimeout at 350ms after two
// immediate bursts) was a ~1.5s experience. Twice-as-long is
// at least ~3s of visible celebration.
//
// The recipe is now data-driven: a `schedule` array holds
// one entry per wave, each with a `delayMs` literal. The
// test extracts every `delayMs: N` literal from the function
// body — the maximum value gives a lower bound on the
// celebration timeline. The recipe enforces that the
// schedule loops over the array via setTimeout (so adding a
// schedule entry automatically adds a burst), and the test
// pins both the maximum delay and the wave count.
//
// This is a regression guard for a UX-level requirement, not a
// behavior test. The exact schedule can change as long as the
// duration stays above the threshold.
func TestFireBirthdayBurst_LastsAtLeastTwiceAsLong(t *testing.T) {
	delays := extractScheduleDelays(t)

	// Pin the schedule: at least 1500ms of staggered bursts.
	// The full visible duration is max-delay + per-burst-fall-time,
	// so 1500ms of scheduled delays yields >= 3 seconds total
	// (more than 2x the original 350ms-then-pause schedule).
	var maxDelay int
	for _, d := range delays {
		if d > maxDelay {
			maxDelay = d
		}
	}
	assert.GreaterOrEqual(t, maxDelay, 1500,
		"birthday burst must schedule at least one wave at >=1500ms so the celebration lasts >2x the original 350ms schedule (got max=%dms, delays=%v)", maxDelay, delays)
}

// TestFireBirthdayBurst_HasMultipleWaves pins the celebration's
// wave structure: the user asked for "at least twice as long",
// and a single bigger burst doesn't feel longer — it feels
// bigger. The fix is staggered waves over time, so the test
// asserts at least 3 distinct delay values are encoded in
// the schedule. The lowest wave runs at t=0; the highest
// wave runs at a much later timestamp — distinct values mean
// distinct bursts over time.
func TestFireBirthdayBurst_HasMultipleWaves(t *testing.T) {
	delays := extractScheduleDelays(t)

	seen := make(map[int]bool)
	for _, d := range delays {
		seen[d] = true
	}

	// Multiple distinct delays = multiple waves over time.
	assert.GreaterOrEqual(t, len(seen), 3,
		"fireBirthdayBurst must schedule at least 3 distinct bursts (got delays=%v)", sortedKeys(seen))
}

// extractScheduleDelays pulls every `delayMs: N` literal out
// of the fireBirthdayBurst function body. The regex matches
// the property name and the integer literal, skipping
// surrounding whitespace and trailing comma. This works for
// both the current data-driven schedule and any future
// variant that keeps `delayMs` as the schedule key.
func extractScheduleDelays(t *testing.T) []int {
	t.Helper()
	fnBody := extractFireBirthdayBurstBody(t)

	delayRe := regexp.MustCompile(`delayMs\s*:\s*(\d+)`)
	matches := delayRe.FindAllStringSubmatch(fnBody, -1)
	requireGreater(t, len(matches), 0,
		"fireBirthdayBurst must define a schedule with delayMs entries (got 0 matches)")

	out := make([]int, 0, len(matches))
	for _, m := range matches {
		d, err := strconv.Atoi(m[1])
		require.NoError(t, err, "delayMs must be a numeric literal; got %q", m[1])
		out = append(out, d)
	}
	return out
}

// sortedKeys returns the integer keys of m in ascending order —
// a small helper so the assertion message is readable.
func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// findMatchingClose walks a JS function body from its opening
// brace, skipping braces inside strings ('...') and line
// comments (// ...), and returns the byte offset of the
// matching close brace. Returns -1 if no match is found.
//
// The walk is a small but correct implementation tailored to
// the fireBirthdayBurst function (single-line statements,
// IIFE-wrapped). It does NOT handle template literals or
// block comments — the recipe doesn't use them, so a correct
// walk covers the cases the recipe actually exercises.
//
// depth starts at 1 (the caller's regex already consumed the
// opening `{` of the function signature, so we're inside
// the function body). The function's matching `}` is the
// first `}` that brings depth back to 0.
// findMatchingClose walks a JS function body from its opening
// brace, skipping braces inside strings ('...') and line
// comments (// ...), and returns the byte offset of the
// matching close brace. Returns -1 if no match is found.
//
// The walk is a small but correct implementation tailored to
// the fireBirthdayBurst function (single-line statements,
// IIFE-wrapped). It does NOT handle template literals or
// block comments — the recipe doesn't use them, so a correct
// walk covers the cases the recipe actually exercises.
//
// depth starts at 1 (the caller's regex already consumed the
// opening `{` of the function signature, so we're inside
// the function body). The function's matching `}` is the
// first `}` that brings depth back to 0.

//nolint:cyclop // 14 > 10 — the state machine genuinely needs all branches. Extracting per-state helpers makes the call-site less readable.
func findMatchingClose(s string) int {
	depth := 1
	inString := false
	stringQuote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if c == '\\' && i+1 < len(s) {
				i++ // skip escaped char
				continue
			}
			if c == stringQuote {
				inString = false
			}
			continue
		}
		if isLineCommentStart(s, i) {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		if c == '\'' || c == '"' {
			inString = true
			stringQuote = c
			continue
		}
		if c == '{' {
			depth++
			continue
		}
		if c == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// isLineCommentStart reports whether s[i:] starts a JS line
// comment (//). The walker skips the comment to end-of-line so
// `}` characters inside a comment don't perturb the depth
// counter.
func isLineCommentStart(s string, i int) bool {
	return i+1 < len(s) && s[i] == '/' && s[i+1] == '/'
}

// extractFireBirthdayBurstBody returns the function body of
// fireBirthdayBurst from the vendored fun-effects.js source.
// The braces-balancer is the only reliable way to slice the
// body without bringing in a full JS parser; a regex would
// stop at the first `}` inside a string literal.
func extractFireBirthdayBurstBody(t *testing.T) string {
	t.Helper()
	body := readFunEffectsJS(t)

	start := regexp.MustCompile(`function fireBirthdayBurst\([^)]*\)\s*\{`).FindStringIndex(body)
	if start == nil {
		t.Fatalf("couldn't find fireBirthdayBurst opening brace")
	}

	body_after := body[start[1]:]
	end := findMatchingClose(body_after)
	if end <= 0 {
		t.Fatalf("couldn't find fireBirthdayBurst closing brace")
	}
	return body_after[:end]
}

// readFunEffectsJS loads the vendored JS source so the
// schedule-extraction regexes above can read it directly. The
// file is embedded via go:embed at the top of this file.
func readFunEffectsJS(t *testing.T) string {
	t.Helper()
	f, err := funEffectsJS.Open("assets/js/fun-effects.js")
	if err != nil {
		t.Fatalf("open fun-effects.js: %v", err)
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 0, 64*1024)
	tmp := make([]byte, 4096)
	for {
		n, err := f.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	return string(buf)
}

// requireGreater is a tiny assert helper that fails the test
// (not just the sub-assertion) when the precondition fails.
// Avoids the awkward "assert.Greater on length 0 gives a
// useless error" pattern. Named with a leading lowercase to
// stay clear of Go's predeclared `min` identifier (1.21+).
func requireGreater(t *testing.T, got, lowerBound int, msg string) {
	t.Helper()
	if got <= lowerBound {
		t.Fatalf("%s (got %d, need > %d)", msg, got, lowerBound)
	}
}
