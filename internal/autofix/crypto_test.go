package autofix

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestCryptoRoundTripAndRandomNonce(t *testing.T) {
	t.Parallel()

	key := hex.EncodeToString([]byte("12345678901234567890123456789012"))
	c, err := NewCrypto(key)
	if err != nil {
		t.Fatalf("NewCrypto returned error: %v", err)
	}

	first, err := c.Encrypt("DATABASE_PASSWORD=swordfish")
	if err != nil {
		t.Fatalf("Encrypt first returned error: %v", err)
	}
	second, err := c.Encrypt("DATABASE_PASSWORD=swordfish")
	if err != nil {
		t.Fatalf("Encrypt second returned error: %v", err)
	}
	if first == second {
		t.Fatal("ciphertexts matched; nonce should make repeated encryption distinct")
	}

	got, err := c.Decrypt(first)
	if err != nil {
		t.Fatalf("Decrypt returned error: %v", err)
	}
	if got != "DATABASE_PASSWORD=swordfish" {
		t.Fatalf("Decrypt = %q, want plaintext", got)
	}
}

func TestCryptoDisabledPassThrough(t *testing.T) {
	t.Parallel()

	c, err := NewCrypto("")
	if err != nil {
		t.Fatalf("NewCrypto empty key returned error: %v", err)
	}
	if c != nil {
		t.Fatal("NewCrypto empty key returned non-nil crypto")
	}
	encrypted, err := c.Encrypt("plain")
	if err != nil {
		t.Fatalf("nil Encrypt returned error: %v", err)
	}
	if encrypted != "plain" {
		t.Fatalf("nil Encrypt = %q, want pass-through", encrypted)
	}
	decrypted, err := c.Decrypt("plain")
	if err != nil {
		t.Fatalf("nil Decrypt returned error: %v", err)
	}
	if decrypted != "plain" {
		t.Fatalf("nil Decrypt = %q, want pass-through", decrypted)
	}
}

func TestNewCryptoRejectsBadKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "not hex", key: "not-hex", want: "not valid hex"},
		{name: "wrong length", key: hex.EncodeToString([]byte("short")), want: "must be 32 bytes"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewCrypto(tc.key)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want to contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestDecryptRejectsCorruptCiphertext(t *testing.T) {
	t.Parallel()

	key := hex.EncodeToString([]byte("12345678901234567890123456789012"))
	c, err := NewCrypto(key)
	if err != nil {
		t.Fatalf("NewCrypto returned error: %v", err)
	}

	for _, input := range []string{"not-hex", "abcd"} {
		_, err := c.Decrypt(input)
		if err == nil {
			t.Fatalf("Decrypt(%q) returned nil error", input)
		}
	}
}
