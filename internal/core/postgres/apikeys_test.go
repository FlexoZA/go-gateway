package postgres

import (
	"strings"
	"testing"
)

func TestGenerateAPIKey(t *testing.T) {
	plain, hash, prefix, err := GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, "dgw_") {
		t.Fatalf("key missing prefix: %q", plain)
	}
	if len(hash) != 64 {
		t.Fatalf("sha256 hex must be 64 chars, got %d", len(hash))
	}
	if hash == plain || strings.Contains(hash, plain) {
		t.Fatal("hash must not contain the plaintext key")
	}
	if !strings.HasPrefix(plain, prefix) || len(prefix) > len(plain) {
		t.Fatalf("prefix %q is not a leading slice of key", prefix)
	}

	// Hash is deterministic and unique per key.
	if hashAPIKey(plain) != hash {
		t.Fatal("hashAPIKey not deterministic")
	}
	plain2, hash2, _, _ := GenerateAPIKey()
	if plain == plain2 || hash == hash2 {
		t.Fatal("generated keys must be unique")
	}
}
