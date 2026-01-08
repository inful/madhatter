package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenEncryptor(t *testing.T) {
	t.Run("Encrypt and Decrypt", func(t *testing.T) {
		// Set a test encryption key (base64 of 32 bytes)
		t.Setenv("TOKEN_ENCRYPTION_KEY", "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")

		encryptor, err := NewTokenEncryptor()
		require.NoError(t, err)

		plaintext := "my-secret-token-12345"

		// Encrypt
		ciphertext, err := encryptor.Encrypt(plaintext)
		require.NoError(t, err)
		assert.NotEmpty(t, ciphertext)
		assert.NotEqual(t, plaintext, ciphertext)

		// Decrypt
		decrypted, err := encryptor.Decrypt(ciphertext)
		require.NoError(t, err)
		assert.Equal(t, plaintext, decrypted)
	})

	t.Run("Empty String", func(t *testing.T) {
		t.Setenv("TOKEN_ENCRYPTION_KEY", "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")

		encryptor, err := NewTokenEncryptor()
		require.NoError(t, err)

		// Encrypt empty string
		ciphertext, err := encryptor.Encrypt("")
		require.NoError(t, err)
		assert.Empty(t, ciphertext)

		// Decrypt empty string
		decrypted, err := encryptor.Decrypt("")
		require.NoError(t, err)
		assert.Empty(t, decrypted)
	})

	t.Run("Invalid Ciphertext", func(t *testing.T) {
		t.Setenv("TOKEN_ENCRYPTION_KEY", "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")

		encryptor, err := NewTokenEncryptor()
		require.NoError(t, err)

		// Try to decrypt invalid data
		_, err = encryptor.Decrypt("invalid-base64-data")
		assert.Error(t, err)

		// Try to decrypt too short data
		_, err = encryptor.Decrypt("YWJj") // base64 of "abc"
		assert.Error(t, err)
	})

	t.Run("Multiple Encryptions Produce Different Ciphertexts", func(t *testing.T) {
		t.Setenv("TOKEN_ENCRYPTION_KEY", "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")

		encryptor, err := NewTokenEncryptor()
		require.NoError(t, err)

		plaintext := "same-plaintext"

		ciphertext1, err := encryptor.Encrypt(plaintext)
		require.NoError(t, err)

		ciphertext2, err := encryptor.Encrypt(plaintext)
		require.NoError(t, err)

		// Due to random nonce, ciphertexts should be different
		assert.NotEqual(t, ciphertext1, ciphertext2)

		// But both should decrypt to same plaintext
		decrypted1, err := encryptor.Decrypt(ciphertext1)
		require.NoError(t, err)
		assert.Equal(t, plaintext, decrypted1)

		decrypted2, err := encryptor.Decrypt(ciphertext2)
		require.NoError(t, err)
		assert.Equal(t, plaintext, decrypted2)
	})

	t.Run("Invalid Key Length", func(t *testing.T) {
		t.Setenv("TOKEN_ENCRYPTION_KEY", "dG9vLXNob3J0") // base64 of "too-short"

		_, err := NewTokenEncryptor()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "encryption key must be 32 bytes")
	})

	t.Run("No Key Set - Generates Random Key", func(t *testing.T) {
		// Don't set TOKEN_ENCRYPTION_KEY
		encryptor, err := NewTokenEncryptor()
		require.NoError(t, err)

		// Should still work with randomly generated key
		plaintext := "test-token"
		ciphertext, err := encryptor.Encrypt(plaintext)
		require.NoError(t, err)

		decrypted, err := encryptor.Decrypt(ciphertext)
		require.NoError(t, err)
		assert.Equal(t, plaintext, decrypted)
	})
}
