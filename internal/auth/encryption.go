package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
)

const (
	// KeySize is the size of the encryption key in bytes.
	KeySize = 32
)

// ErrInvalidCiphertext is returned when decryption fails.
var ErrInvalidCiphertext = errors.New("invalid ciphertext")

// TokenEncryptor handles encryption and decryption of OAuth tokens.
type TokenEncryptor struct {
	gcm cipher.AEAD
}

// NewTokenEncryptor creates a new token encryptor.
// It loads the encryption key from the TOKEN_ENCRYPTION_KEY
// environment variable.
//
// When production=true and the env var is unset, the constructor
// fails loud: the dev-mode fallback to a random key is convenient
// for local hacking but loses every stored OAuth refresh token on
// restart, which is silent data loss in production. The security
// review (finding #2) flagged this; the API takes an explicit
// production flag rather than probing the environment so callers
// — typically api/setupAuth, which already knows whether the
// server is in dev mode — are forced to opt in to the strict
// behavior.
//
// In dev mode (production=false) the random-key fallback is
// preserved with its existing warning so local hacking isn't
// blocked by an env-var requirement.
func NewTokenEncryptor(production bool) (*TokenEncryptor, error) {
	// Try to get encryption key from environment
	keyStr := os.Getenv("TOKEN_ENCRYPTION_KEY")

	var key []byte
	switch {
	case keyStr != "":
		// Decode base64-encoded key
		var err error
		key, err = base64.StdEncoding.DecodeString(keyStr)
		if err != nil {
			return nil, fmt.Errorf("failed to decode encryption key: %w", err)
		}
		if len(key) != KeySize {
			return nil, fmt.Errorf("encryption key must be %d bytes, got %d", KeySize, len(key))
		}
	case production:
		// Security review finding #2: refuse the random-key fallback
		// in production. The fallback loses all stored OAuth tokens
		// on the first restart — silent data loss for the most
		// security-sensitive field the app stores.
		return nil, fmt.Errorf("TOKEN_ENCRYPTION_KEY is required in production (set it to a base64-encoded %d-byte key)", KeySize)
	default:
		// Dev mode: random key is fine; local sessions don't need
		// to survive restarts and the warning keeps the
		// consequence visible in the logs.
		slog.Warn("TOKEN_ENCRYPTION_KEY not set, using randomly generated key (tokens won't survive restarts)")
		key = make([]byte, KeySize)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("failed to generate encryption key: %w", err)
		}
	}

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	return &TokenEncryptor{gcm: gcm}, nil
}

// Encrypt encrypts plaintext and returns base64-encoded ciphertext.
func (te *TokenEncryptor) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	// Generate nonce
	nonce := make([]byte, te.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt and append nonce
	ciphertext := te.gcm.Seal(nonce, nonce, []byte(plaintext), nil)

	// Encode to base64 for storage
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts base64-encoded ciphertext and returns plaintext.
func (te *TokenEncryptor) Decrypt(ciphertextB64 string) (string, error) {
	if ciphertextB64 == "" {
		return "", nil
	}

	// Decode from base64
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", fmt.Errorf("failed to decode ciphertext: %w", err)
	}

	// Check minimum length
	nonceSize := te.gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", ErrInvalidCiphertext
	}

	// Split nonce and ciphertext
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]

	// Decrypt
	plaintext, err := te.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", ErrInvalidCiphertext
	}

	return string(plaintext), nil
}
