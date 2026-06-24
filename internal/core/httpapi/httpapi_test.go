package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dfm/device-gateway/internal/core/logging"
)

type stubVerifier struct {
	valid string
	err   error
}

func (s stubVerifier) VerifyAPIKey(_ context.Context, key string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return key == s.valid, nil
}

func newTestServer(v KeyVerifier) *Server {
	return New("127.0.0.1", 0, []UnitInfo{{Name: "test"}}, v, nil, nil, logging.New("test"))
}

func TestHealthIsPublic(t *testing.T) {
	s := newTestServer(stubVerifier{valid: "good"})
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health = %d, want 200", rec.Code)
	}
}

func TestProtectedRoute(t *testing.T) {
	s := newTestServer(stubVerifier{valid: "good-key"})

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"wrong scheme", "Basic good-key", http.StatusUnauthorized},
		{"bad key", "Bearer nope", http.StatusUnauthorized},
		{"good key", "Bearer good-key", http.StatusOK},
		{"case-insensitive scheme", "bearer good-key", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/ping", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			s.srv.Handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("%s: got %d, want %d", tc.name, rec.Code, tc.want)
			}
		})
	}
}

func TestProtectedRouteNoVerifier(t *testing.T) {
	s := newTestServer(nil)
	req := httptest.NewRequest("GET", "/api/ping", nil)
	req.Header.Set("Authorization", "Bearer anything")
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
}
