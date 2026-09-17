package stockroom

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	"SIGNIN_PHOTOS_REMOTE",
	"SIGNIN_PHOTOS_DIR",
	"SIGNIN_PHOTOS_COUNT",
	"SIGNIN_PHOTOS_BATCH",
	"SIGNIN_PHOTOS_TTL_MINUTES",
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

// TestLoadConfigPhotoWallDefaults pins the sign-in photo wall's defaults
// (docs/design/signin-photo-wall.html §8). The remote is the switch and has no
// default on purpose: unset must mean the feature is cleanly off, so an
// existing .env keeps today's sign-in screen.
func TestLoadConfigPhotoWallDefaults(t *testing.T) {
	isolateEnv(t)
	chdirNoDotEnv(t)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.SignInPhotosRemote != "" {
		t.Errorf("SignInPhotosRemote = %q, want empty so the wall stays off", cfg.SignInPhotosRemote)
	}
	if cfg.SignInPhotosDir != DefaultPhotoWallDir {
		t.Errorf("SignInPhotosDir = %q, want %q", cfg.SignInPhotosDir, DefaultPhotoWallDir)
	}
	if cfg.SignInPhotosCount != DefaultPhotoWallCount {
		t.Errorf("SignInPhotosCount = %d, want %d", cfg.SignInPhotosCount, DefaultPhotoWallCount)
	}
	if cfg.SignInPhotosBatch != DefaultPhotoWallBatch {
		t.Errorf("SignInPhotosBatch = %d, want %d", cfg.SignInPhotosBatch, DefaultPhotoWallBatch)
	}
	if want := int(DefaultPhotoWallTTL / time.Minute); cfg.SignInPhotosTTLMinutes != want {
		t.Errorf("SignInPhotosTTLMinutes = %d, want %d", cfg.SignInPhotosTTLMinutes, want)
	}
}

// TestLoadConfigRejectsABadPhotoWallNumber: a typo in .env that silently falls
// back to the default is a typo nobody ever finds, so the numbers follow
// SESSION_IDLE_MINUTES and refuse to start.
func TestLoadConfigRejectsABadPhotoWallNumber(t *testing.T) {
	for _, key := range []string{"SIGNIN_PHOTOS_COUNT", "SIGNIN_PHOTOS_BATCH", "SIGNIN_PHOTOS_TTL_MINUTES"} {
		for _, bad := range []string{"none", "0", "-4"} {
			t.Run(key+"="+bad, func(t *testing.T) {
				isolateEnv(t)
				chdirNoDotEnv(t)
				t.Setenv(key, bad)

				if _, err := LoadConfig(); err == nil {
					t.Fatalf("LoadConfig accepted %s=%q", key, bad)
				} else if !strings.Contains(err.Error(), key) {
					t.Errorf("error should name %s, got %q", key, err)
				}
			})
		}
	}
}
