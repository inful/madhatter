package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// testSessionSecret32 is well above the production minimum so the
// happy-path tests below stay focused on the validation logic
// rather than on the boundary condition (covered separately).
const testSessionSecret32 = "test-secret-that-is-at-least-thirty-two-bytes"

// testTokenEncryptionKey is a base64-encoded 32-byte value used by
// NewTokenEncryptor. Set explicitly so these tests don't couple to
// the encryptor's "missing key → random fallback" warning path.
//
//nolint:gosec // Test-only fixture; not a real credential.
const testTokenEncryptionKey = "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI="

// TestNewServer_RefusesWhenSessionSecretMissing pins the
// production enforcement added in response to security review #1:
// when development=false and SESSION_SECRET is unset, NewServer
// must fail rather than boot with an empty HMAC key (a missing
// key means anyone can forge unsubscribe tokens for arbitrary
// members — see internal/notify/token.go for the threat model).
func TestNewServer_RefusesWhenSessionSecretMissing(t *testing.T) {
	t.Setenv("SESSION_SECRET", "")
	for _, k := range []string{"FORGEJO_CLIENT_ID", "GITLAB_CLIENT_ID"} {
		t.Setenv(k, "")
	}
	t.Setenv("TOKEN_ENCRYPTION_KEY", testTokenEncryptionKey)

	db := newAuthSetupDB(t)
	_, err := NewServer(db, false)
	require.Error(t, err, "production server must refuse to start without SESSION_SECRET")
	require.Contains(t, err.Error(), "SESSION_SECRET")
}

// TestNewServer_RefusesWhenSessionSecretTooShort pins the
// minimum-length check: a non-empty SESSION_SECRET under the
// minimum is still rejected because an HMAC key shorter than 16
// bytes doesn't provide reasonable resistance to brute force on
// captured unsubscribe tokens.
func TestNewServer_RefusesWhenSessionSecretTooShort(t *testing.T) {
	t.Setenv("SESSION_SECRET", "short") // 5 bytes — under the minimum
	for _, k := range []string{"FORGEJO_CLIENT_ID", "GITLAB_CLIENT_ID"} {
		t.Setenv(k, "")
	}
	t.Setenv("TOKEN_ENCRYPTION_KEY", testTokenEncryptionKey)

	db := newAuthSetupDB(t)
	_, err := NewServer(db, false)
	require.Error(t, err, "production server must refuse to start with a too-short SESSION_SECRET")
	require.Contains(t, err.Error(), "SESSION_SECRET")
}

// TestNewServer_AcceptsValidSessionSecret verifies the happy
// path: with a sufficiently long SESSION_SECRET in production
// mode and no OAuth providers configured, the server still boots
// (auth is wired as nil and the documented "auth disabled"
// behavior is preserved).
func TestNewServer_AcceptsValidSessionSecret(t *testing.T) {
	t.Setenv("SESSION_SECRET", testSessionSecret32)
	for _, k := range []string{"FORGEJO_CLIENT_ID", "GITLAB_CLIENT_ID"} {
		t.Setenv(k, "")
	}
	t.Setenv("TOKEN_ENCRYPTION_KEY", testTokenEncryptionKey)

	db := newAuthSetupDB(t)
	s, err := NewServer(db, false)
	require.NoError(t, err, "production server must accept a SESSION_SECRET that meets the minimum length")
	require.NotNil(t, s)
}

// TestNewServer_AllowsMissingSessionSecretInDev verifies the
// escape hatch: --development mode tolerates a missing or weak
// SESSION_SECRET so local hacking isn't blocked by the production
// hardening. The token-encryption-key still gets a valid value
// here so the test doesn't fail for an unrelated reason.
func TestNewServer_AllowsMissingSessionSecretInDev(t *testing.T) {
	t.Setenv("SESSION_SECRET", "")
	t.Setenv("TOKEN_ENCRYPTION_KEY", testTokenEncryptionKey)

	db := newAuthSetupDB(t)
	s, err := NewServer(db, true)
	require.NoError(t, err, "development mode must tolerate missing SESSION_SECRET")
	require.NotNil(t, s)
}
