package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashToken(t *testing.T) {
	t.Run("Same Token Produces Same Hash", func(t *testing.T) {
		token := "my-session-token"
		hash1 := hashToken(token)
		hash2 := hashToken(token)

		assert.Equal(t, hash1, hash2)
		assert.NotEqual(t, token, hash1)
		assert.Len(t, hash1, 64) // SHA-256 produces 64 hex characters
	})

	t.Run("Different Tokens Produce Different Hashes", func(t *testing.T) {
		token1 := "token-1"
		token2 := "token-2"

		hash1 := hashToken(token1)
		hash2 := hashToken(token2)

		assert.NotEqual(t, hash1, hash2)
	})

	t.Run("Empty Token", func(t *testing.T) {
		hash := hashToken("")
		assert.NotEmpty(t, hash)
		assert.Len(t, hash, 64)
	})
}

func TestGenerateSecureToken(t *testing.T) {
	t.Run("Generate Token", func(t *testing.T) {
		token, err := generateSecureToken()
		require.NoError(t, err)
		assert.NotEmpty(t, token)

		// Token should be base64 encoded, so longer than 32 bytes
		assert.Greater(t, len(token), 32)
	})

	t.Run("Tokens Are Unique", func(t *testing.T) {
		token1, err := generateSecureToken()
		require.NoError(t, err)

		token2, err := generateSecureToken()
		require.NoError(t, err)

		assert.NotEqual(t, token1, token2)
	})
}

func TestGenerateStateToken(t *testing.T) {
	t.Run("Generate State Token", func(t *testing.T) {
		state, err := generateStateToken()
		require.NoError(t, err)
		assert.NotEmpty(t, state)
	})

	t.Run("State Tokens Are Unique", func(t *testing.T) {
		state1, err := generateStateToken()
		require.NoError(t, err)

		state2, err := generateStateToken()
		require.NoError(t, err)

		assert.NotEqual(t, state1, state2)
	})
}
