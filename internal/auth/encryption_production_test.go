package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewTokenEncryptor_ProductionRefusesMissingKey pins the
// production-mode enforcement added in response to security
// review finding #2: when production=true and TOKEN_ENCRYPTION_KEY
// is unset, NewTokenEncryptor must return an error rather than
// silently fall back to a random key. The fallback exists for dev
// mode but loses all OAuth tokens on restart, which is silent
// data loss in production.
//
// "production" here is a parameter rather than an env-var probe so
// the test pins the contract — callers explicitly tell the
// encryptor whether a missing key is fatal.
func TestNewTokenEncryptor_ProductionRefusesMissingKey(t *testing.T) {
	t.Setenv("TOKEN_ENCRYPTION_KEY", "")

	_, err := NewTokenEncryptor(true)
	require.Error(t, err, "production encryptor must refuse to construct without TOKEN_ENCRYPTION_KEY")
	assert.Contains(t, err.Error(), "TOKEN_ENCRYPTION_KEY")
}

// TestNewTokenEncryptor_ProductionAcceptsValidKey pins the happy
// path: a base64-encoded 32-byte key is accepted in production.
func TestNewTokenEncryptor_ProductionAcceptsValidKey(t *testing.T) {
	t.Setenv("TOKEN_ENCRYPTION_KEY", "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")

	enc, err := NewTokenEncryptor(true)
	require.NoError(t, err)
	require.NotNil(t, enc)

	// Round-trip must work end-to-end.
	ct, err := enc.Encrypt("hello")
	require.NoError(t, err)
	pt, err := enc.Decrypt(ct)
	require.NoError(t, err)
	assert.Equal(t, "hello", pt)
}

// TestNewTokenEncryptor_DevAcceptsMissingKey pins the dev-mode
// escape hatch: production=false retains the existing behavior
// where the encryptor generates a random key with a warning.
// Local hacking sessions don't need restart-survivable token
// storage; the silent-data-loss risk is only material in
// production.
func TestNewTokenEncryptor_DevAcceptsMissingKey(t *testing.T) {
	t.Setenv("TOKEN_ENCRYPTION_KEY", "")

	enc, err := NewTokenEncryptor(false)
	require.NoError(t, err, "dev mode must tolerate missing TOKEN_ENCRYPTION_KEY (random fallback)")
	require.NotNil(t, enc)
}
