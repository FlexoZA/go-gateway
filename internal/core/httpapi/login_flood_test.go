package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/dfm/device-gateway/internal/core/logging"
)

// blockingLoginData holds every VerifyUser call inside the handler until released,
// so a test can saturate the login concurrency guard deterministically.
type blockingLoginData struct {
	*fakeData
	entered chan struct{}
	release chan struct{}
}

func (b *blockingLoginData) VerifyUser(_ context.Context, _, _ string) (bool, error) {
	b.entered <- struct{}{}
	<-b.release
	return true, nil
}

// TestLoginFloodShedsLoad: once maxConcurrentLogins verifications are in flight, a
// further login is shed with 429 rather than spawning another bcrypt computation.
func TestLoginFloodShedsLoad(t *testing.T) {
	fd := &blockingLoginData{
		fakeData: &fakeData{users: map[string]string{"a@b.com": "secret"}},
		entered:  make(chan struct{}, maxConcurrentLogins),
		release:  make(chan struct{}),
	}
	s := New("127.0.0.1", 0, []UnitInfo{{Name: "howen"}}, stubVerifier{valid: "k"}, fd, nil, logging.New("test"))
	body := `{"email":"a@b.com","password":"secret"}`

	for i := 0; i < maxConcurrentLogins; i++ {
		go do(s, "POST", "/api/auth/login", body)
	}
	// Wait until all slots are occupied (each blocked inside VerifyUser).
	for i := 0; i < maxConcurrentLogins; i++ {
		select {
		case <-fd.entered:
		case <-time.After(3 * time.Second):
			t.Fatal("login attempts did not saturate the concurrency guard")
		}
	}

	if rec := do(s, "POST", "/api/auth/login", body); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("login over the concurrency cap = %d, want 429", rec.Code)
	}

	close(fd.release) // let the in-flight logins complete
}
