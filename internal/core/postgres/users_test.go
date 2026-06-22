package postgres

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashPassword(t *testing.T) {
	pw := "correct horse battery staple"
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	if hash == pw {
		t.Fatal("password stored in plaintext")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) != nil {
		t.Fatal("correct password should verify")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("wrong")) == nil {
		t.Fatal("wrong password must not verify")
	}

	// Same password hashed twice must differ (per-user salt).
	hash2, _ := HashPassword(pw)
	if hash == hash2 {
		t.Fatal("hashes should be salted (differ each time)")
	}
}

func TestTimingEqualizerHashValid(t *testing.T) {
	// The fixed timing-equalizer hash must be a parseable bcrypt hash so the
	// missing-user path runs a real comparison.
	if _, err := bcrypt.Cost([]byte(timingEqualizerHash)); err != nil {
		t.Fatalf("timing equalizer hash invalid: %v", err)
	}
}
