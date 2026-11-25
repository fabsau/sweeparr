package crypto_test

import (
	"testing"

	"github.com/fabsau/sweeparr/internal/crypto"
)

func TestEncryptDecryptAndTokens(t *testing.T) {
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 1)
	}
	ct, err := crypto.Encrypt(key, "super-secret-plex-token")
	if err != nil || ct == "" || ct == "super-secret-plex-token" {
		t.Fatalf("encrypt failed: err=%v ct=%q", err, ct)
	}
	pt, err := crypto.Decrypt(key, ct)
	if err != nil || pt != "super-secret-plex-token" {
		t.Fatalf("decrypt failed: err=%v pt=%q", err, pt)
	}
	var wrongKey [32]byte
	if _, err := crypto.Decrypt(wrongKey, ct); err == nil {
		t.Fatalf("expected decrypt with wrong key to fail")
	}
	raw, hash, err := crypto.RandomToken(24)
	if err != nil || raw == "" || !crypto.EqualHash(hash, crypto.HashToken(raw)) {
		t.Fatalf("random token / hash mismatch")
	}
}
