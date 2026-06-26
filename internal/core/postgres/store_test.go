package postgres

import (
	"context"
	"testing"
)

// TestNewRejectsInvalidDSN exercises the ParseConfig branch without a database:
// a malformed pool DSN must fail synchronously at parse time rather than at
// connect, so the error is clear and New doesn't block.
func TestNewRejectsInvalidDSN(t *testing.T) {
	_, err := New(context.Background(), "postgres://localhost/db?pool_max_conns=notanumber", false)
	if err == nil {
		t.Fatal("New with an invalid pool DSN should return a parse error")
	}
}
