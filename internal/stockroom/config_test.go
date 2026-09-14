package stockroom

import (
	"os"
	"path/filepath"
	"testing"
)

// configVars is every environment variable LoadConfig reads. Tests isolate all
// of them so a developer's real shell environment (or a .env picked up by an
// earlier test) can't change the result.
var configVars = []string{
	"DATABASE_URL",
	"SERVER_ADDR",
	"ADMIN_STUDENT_NUMBER",
	"ADMIN_PASSWORD",
	"UPLOADS_DIR",
	"BACKUP_DIR",
	"SESSION_IDLE_MINUTES",
}

// isolateEnv unsets every config variable for the duration of the test and
// restores the originals afterwards. It deliberately uses Unsetenv rather than
// t.Setenv(k, "") because godotenv treats a set-but-empty variable as already
// present and refuses to load the .env value for it.
func isolateEnv(t *testing.T) {
	t.Helper()
	for _, k := range configVars {
		if old, ok := os.LookupEnv(k); ok {
			t.Cleanup(func() { os.Setenv(k, old) })
		} else {
			t.Cleanup(func() { os.Unsetenv(k) })
		}
		os.Unsetenv(k)
	}
}

// chdirNoDotEnv moves into a fresh temp directory and neutralises any .env
// files above it, so findDotEnv is guaranteed to come up empty.
func chdirNoDotEnv(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if path, ok := findDotEnv(); ok {
		t.Skipf("a .env exists above the temp dir (%s); cannot test the no-.env path here", path)
	}
	return dir
}

func TestLoadConfigDefaults(t *testing.T) {
	isolateEnv(t)
	chdirNoDotEnv(t)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if want := "postgresql://postgres:postgres@127.0.0.1:54322/postgres"; cfg.DatabaseURL != want {
		t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, want)
	}
	if want := "127.0.0.1:8080"; cfg.ServerAddr != want {
		t.Errorf("ServerAddr = %q, want %q", cfg.ServerAddr, want)
	}
	if want := "./uploads"; cfg.UploadsDir != want {
		t.Errorf("UploadsDir = %q, want %q", cfg.UploadsDir, want)
	}
	if cfg.SessionIdleMinutes != 10 {
		t.Errorf("SessionIdleMinutes = %d, want 10", cfg.SessionIdleMinutes)
	}
	// These have no default on purpose: an unconfigured failsafe admin or
	// backup target must be visibly empty, not silently guessed.
	if cfg.AdminStudentNumber != "" || cfg.AdminPassword != "" || cfg.BackupDir != "" {
		t.Errorf("expected admin/backup settings to stay empty, got %+v", cfg)
	}
}
