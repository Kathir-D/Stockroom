package main

import "testing"

func TestRunRejectsAnUnknownCommand(t *testing.T) {
	if code := run([]string{"frobnicate"}); code != 2 {
		t.Errorf("run(frobnicate) = %d, want 2", code)
	}
}

func TestRunVersion(t *testing.T) {
	if code := run([]string{"version"}); code != 0 {
		t.Errorf("run(version) = %d, want 0", code)
	}
}

// TestServeRejectsArguments: `stockroom serve extra` is a typo, not a request
// to start with defaults.
func TestServeRejectsArguments(t *testing.T) {
	if code := run([]string{"serve", "extra"}); code != 2 {
		t.Errorf("run(serve extra) = %d, want 2", code)
	}
}
