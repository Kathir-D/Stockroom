package stockroom

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVersionLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		less bool
	}{
		{"1.60.1", "1.60.0", false},
		{"1.59.9", "1.60.0", true},
		{"1.60", "1.60.0", false},
		{"1.75.1", "1.60.0", false},
		{"1.9.0", "1.60.0", true},
		{"1.61.0-DEV", "1.60.0", false},
	} {
		if got := versionLess(c.a, c.b); got != c.less {
			t.Errorf("versionLess(%q, %q) = %v, want %v", c.a, c.b, got, c.less)
		}
	}
}

// TestResolveRclone uses a fake rclone that prints an old version, and checks
// the explicit path wins over PATH and the result is recorded.
func TestResolveRclone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake rclone is a shell script")
	}
	old := rcloneBinary
	t.Cleanup(func() {
		rcloneBinary = old
		resolvedRclone.mu.Lock()
		resolvedRclone.info = nil
		resolvedRclone.mu.Unlock()
	})

	dir := t.TempDir()
	fake := filepath.Join(dir, "rclone")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'rclone v1.53.3'\necho '- os/version: test'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	info := ResolveRclone(context.Background(), fake)
	if !info.Found || info.Path != fake || info.Version != "1.53.3" || !info.TooOld {
		t.Errorf("ResolveRclone = %+v", info)
	}
	if rcloneBinary != fake {
		t.Errorf("rcloneBinary = %q, want %q", rcloneBinary, fake)
	}
	if got := Rclone(); got == nil || got.Path != fake {
		t.Errorf("Rclone() = %+v", got)
	}

	missing := ResolveRclone(context.Background(), filepath.Join(dir, "nope"))
	if missing.Found || missing.Error == "" {
		t.Errorf("a missing RCLONE_BINARY resolved: %+v", missing)
	}
}
