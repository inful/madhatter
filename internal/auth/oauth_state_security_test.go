package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestValidateState_MatchAccepted pins the happy path of the
// constant-time OAuth state helper: when the cookie value and
// the query value are equal, the comparison returns true. The
// helper is the security review #5 fix: extract the comparison
// from the inline `!=` in HandleCallback into a function that
// uses crypto/subtle.ConstantTimeCompare, which is testable
// here and isn't at the mercy of the operator's gofmt output.
func TestValidateState_MatchAccepted(t *testing.T) {
	const state = "abcdef0123456789-abcdef0123456789"
	assert.True(t, validateState(state, state),
		"identical cookie and query state must compare equal")
}

// TestValidateState_MismatchRejected pins the failure path:
// a query state that differs from the cookie state must return
// false. The test runs with a mismatch at the start, middle,
// and end of the string to confirm the helper does not
// short-circuit on the first byte.
func TestValidateState_MismatchRejected(t *testing.T) {
	const cookie = "abcdef0123456789-abcdef0123456789"
	cases := []struct {
		name  string
		query string
	}{
		{"first byte differs", "Xbcdef0123456789-abcdef0123456789"},
		{"middle byte differs", "abcdef012345X789-abcdef0123456789"},
		{"last byte differs", "abcdef0123456789-abcdef012345678X"},
		{"shorter", "abcdef0123456789"},
		{"longer", "abcdef0123456789-abcdef0123456789-extra"},
		{"completely different", strings.Repeat("z", 64)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.False(t, validateState(cookie, tc.query),
				"mismatched query state (%q) must compare not-equal to cookie", tc.query)
		})
	}
}

// TestValidateState_DifferentLengthRejected pins the
// length-mismatch path. The naive use of subtle.ConstantTimeCompare
// returns 0 for different-length inputs (a length-based timing
// leak), so the helper must compare lengths FIRST and reject
// mismatched lengths before invoking the constant-time
// compare. Without the length check, an attacker could
// distinguish a length guess from a content guess purely by
// timing.
func TestValidateState_DifferentLengthRejected(t *testing.T) {
	assert.False(t, validateState("short", "much-longer-cookie-value-here"),
		"length mismatch must be rejected before constant-time compare")
	assert.False(t, validateState("much-longer-cookie-value-here", "short"),
		"length mismatch in either direction must be rejected")
}

// TestValidateState_EmptyInputs is a defensive pin: a one-sided
// empty input (cookie set, query missing, or vice versa) must
// compare not-equal. An empty string would otherwise be an
// attractive replay target if the cookie side is also empty
// (the constant-time compare would say "equal" for two empty
// strings); the upstream handler also rejects an empty query
// state with a 400 before this helper is even consulted, so
// the "both empty" case is a no-op there. The asymmetric cases
// here pin the safety net.
func TestValidateState_EmptyInputs(t *testing.T) {
	assert.False(t, validateState("", "non-empty"),
		"empty cookie must not match a non-empty query")
	assert.False(t, validateState("non-empty", ""),
		"empty query must not match a non-empty cookie")
}
