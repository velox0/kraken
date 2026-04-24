package autofix

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Crypto provides AES-256-GCM encryption and decryption for fix environment
// variable values at rest.
type Crypto struct {
	gcm cipher.AEAD
}

// NewCrypto creates a Crypto from a 32-byte hex-encoded secret key.
// Returns nil and no error if keyHex is empty (encryption disabled).
func NewCrypto(keyHex string) (*Crypto, error) {
	if keyHex == "" {
		return nil, nil
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("FIX_ENV_SECRET is not valid hex: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("FIX_ENV_SECRET must be 32 bytes (64 hex chars), got %d bytes", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Crypto{gcm: gcm}, nil
}

// Encrypt encrypts plaintext and returns hex-encoded ciphertext.
func (c *Crypto) Encrypt(plaintext string) (string, error) {
	if c == nil {
		return plaintext, nil
	}
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(sealed), nil
}

// Decrypt decrypts hex-encoded ciphertext and returns plaintext.
func (c *Crypto) Decrypt(ciphertextHex string) (string, error) {
	if c == nil {
		return ciphertextHex, nil
	}
	data, err := hex.DecodeString(ciphertextHex)
	if err != nil {
		return "", errors.New("corrupt env var ciphertext")
	}
	nonceSize := c.gcm.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("corrupt env var ciphertext (too short)")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := c.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("env var decryption failed (wrong key?)")
	}
	return string(plaintext), nil
}
