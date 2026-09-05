package stockroom

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOpenRejectsMalformedURL(t *testing.T) {
	for name, url := range map[string]string{
		"garbage":      "://not a url",
		"bad port":     "postgresql://user@host:notaport/db",
		"bad sslmode":  "postgresql://u:p@127.0.0.1:5432/db?sslmode=nonsense",
		"empty string": "",
	} {
		t.Run(name, func(t *testing.T) {
			db, err := Open(context.Background(), url)
			if err == nil {
				db.Close()
				t.Fatalf("Open(%q): want an error, got nil", url)
			}
		})
	}
}

// A syntactically valid URL pointing at nothing must fail during Open (the
// ping), not silently succeed and blow up on the first request.
func TestOpenFailsWhenDatabaseUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Port 1 is reserved and refuses connections immediately.
	db, err := Open(ctx, "postgresql://postgres:postgres@127.0.0.1:1/postgres?connect_timeout=2")
	if err == nil {
		db.Close()
		t.Fatal("Open() against an unreachable database: want an error, got nil")
	}
	if !strings.Contains(err.Error(), "ping database") && !strings.Contains(err.Error(), "connect") {
		t.Errorf("error = %q, want it to mention the failed connection", err)
	}
}

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

// Ping on a closed pool must return an error rather than panicking, because
// GET /health turns it straight into a 500.
func TestPingAfterCloseFails(t *testing.T) {
	db := requireTestDB(t)
	db.Close()

	if err := db.Ping(context.Background()); err == nil {
		t.Fatal("Ping() after Close(): want an error, got nil")
	}
}

// Ping honours a cancelled context instead of hanging.
func TestPingRespectsCancelledContext(t *testing.T) {
	db := requireTestDB(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := db.Ping(ctx); err == nil {
		t.Fatal("Ping() with a cancelled context: want an error, got nil")
	}
}
