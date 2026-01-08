package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
)

var (
	// ErrInvalidCiphertext is returned when decryption fails.
	ErrInvalidCiphertext = errors.New("invalid ciphertext")
)

// TokenEncryptor handles encryption and decryption of OAuth tokens.
type TokenEncryptor struct {
	gcm cipher.AEAD
}

// NewTokenEncryptor creates a new token encryptor.
// It loads the encryption key from environment variable TOKEN_ENCRYPTION_KEY.
// If not set, it generates a random key (WARNING: tokens won't survive restarts).
func NewTokenEncryptor() (*TokenEncryptor, error) {
	// Try to get encryption key from environment
	keyStr := os.Getenv("TOKEN_ENCRYPTION_KEY")
	
	var key []byte
	if keyStr != "" {
		// Decode base64-encoded key
		var err error
		key, err = base64.StdEncoding.DecodeString(keyStr)
		if err != nil {
			return nil, fmt.Errorf("failed to decode encryption key: %w", err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(key))
		}
	} else {
		// Generate a random key (WARNING: tokens won't survive restarts)
		fmt.Println("WARNING: TOKEN_ENCRYPTION_KEY not set, using randomly generated key (tokens won't survive restarts)")
		key = make([]byte, 32)
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
