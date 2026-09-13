package stockroom

import (
	"context"
	"testing"
	"time"
)

func TestOpenPingAndClose(t *testing.T) {
	db := requireTestDB(t)

	if err := db.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	// The pool is configured for a single-machine deployment; a regression to
	// the pgx default (num CPUs, min 4) would be silent otherwise.
	if got := db.Pool.Config().MaxConns; got != 8 {
		t.Errorf("MaxConns = %d, want 8", got)
	}
	if got := db.Pool.Config().MaxConnIdleTime; got != 5*time.Minute {
		t.Errorf("MaxConnIdleTime = %v, want 5m", got)
	}
}
