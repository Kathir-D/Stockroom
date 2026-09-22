package main

import (
	"context"
	"testing"
	"time"

	"stockroom/internal/stockroom"
)

// unreachableURL points at a port nothing listens on, which is what a
// still-starting Postgres looks like from here.
const unreachableURL = "postgresql://postgres:postgres@127.0.0.1:1/postgres"

// TestOpenWithRetryKeepsTrying is the reason this helper exists: a closet PC
// reboots, the service starts before Docker has a container ready, and a
// single attempt would exit the process -- taking the nightly backup goroutine
// with it. The retry has to outlive the gap.
func TestOpenWithRetryKeepsTrying(t *testing.T) {
	start := time.Now()
	_, err := openWithRetry(context.Background(), unreachableURL, stockroom.Options{}, 1500*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error against a port nothing is listening on")
	}
	// Without the retry this returns on the first failed connect, well inside
	// the first second. Spending the budget is the behaviour under test.
	if elapsed < time.Second {
		t.Fatalf("gave up after %s; expected it to keep retrying for about the full budget", elapsed)
	}
}

// TestOpenWithRetryStopsOnCancel: Ctrl+C during a slow boot must exit now, not
// two minutes from now.
func TestOpenWithRetryStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if _, err := openWithRetry(ctx, unreachableURL, stockroom.Options{}, time.Minute); err == nil {
		t.Fatal("expected an error")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("ignored the cancelled context for %s", elapsed)
	}
}
