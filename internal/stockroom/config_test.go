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
	"SIGNIN_PHOTOS_DIR",
	"SIGNIN_PHOTOS_COUNT",
	"SIGNIN_PHOTOS_BATCH",
	"SIGNIN_PHOTOS_TTL_MINUTES",
	"PHOTO_BACKUP_DIR",
	"STOCKROOM_CONFIG",
	"PRE_MIGRATE_DUMP",
	"PG_DUMP",
	"RCLONE_BINARY",
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
// (docs/design/signin-photo-wall.html §8). No remote is named here: the wall
// reads through the one shared Google remote (google.go).
func TestLoadConfigPhotoWallDefaults(t *testing.T) {
	isolateEnv(t)
	chdirNoDotEnv(t)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
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

// writeEnv writes a config file holding one DATABASE_URL, so a test can tell
// which of several files LoadConfigFrom read.
func writeEnv(t *testing.T, path, marker string, extra ...string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "DATABASE_URL=" + marker + "\n" + strings.Join(extra, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// useSystemPath points the system config path at a temporary file for the
// length of the test.
func useSystemPath(t *testing.T, path string) {
	t.Helper()
	old := systemConfigPaths
	systemConfigPaths = func() []string { return []string{path} }
	t.Cleanup(func() { systemConfigPaths = old })
}

// TestLoadConfigPrecedence walks the list from the bottom up: each file wins
// until one above it appears. Absolute data paths keep the installed sources
// from failing on the photo wall's relative default.
func TestLoadConfigPrecedence(t *testing.T) {
	root := t.TempDir()
	abs := []string{"UPLOADS_DIR=" + filepath.Join(root, "up"), "SIGNIN_PHOTOS_DIR=" + filepath.Join(root, "cache")}
	system := writeEnv(t, filepath.Join(root, "etc", "stockroom.env"), "system", abs...)
	envVar := writeEnv(t, filepath.Join(root, "var.env"), "variable", abs...)
	flagFile := writeEnv(t, filepath.Join(root, "flag.env"), "flag", abs...)

	load := func(t *testing.T, flagPath string) Config {
		t.Helper()
		cfg, err := LoadConfigFrom(flagPath)
		if err != nil {
			t.Fatalf("LoadConfigFrom(%q): %v", flagPath, err)
		}
		return cfg
	}

	cases := []struct {
		name       string
		dotEnv     bool
		envVar     bool
		flag       string
		wantURL    string
		wantSource ConfigSource
	}{
		{"system path only", false, false, "", "system", ConfigFromSystem},
		{".env beats the system path", true, false, "", "dotenv", ConfigFromDotEnv},
		{"STOCKROOM_CONFIG beats .env", true, true, "", "variable", ConfigFromEnv},
		{"--config beats everything", true, true, flagFile, "flag", ConfigFromFlag},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolateEnv(t)
			dir := chdirNoDotEnv(t)
			useSystemPath(t, system)
			if c.dotEnv {
				writeEnv(t, filepath.Join(dir, ".env"), "dotenv")
			}
			if c.envVar {
				t.Setenv("STOCKROOM_CONFIG", envVar)
			}
			cfg := load(t, c.flag)
			if cfg.DatabaseURL != c.wantURL || cfg.Source != c.wantSource {
				t.Errorf("read %q from %s, want %q from %s", cfg.DatabaseURL, cfg.Source, c.wantURL, c.wantSource)
			}
			if cfg.EnvPath == "" || !filepath.IsAbs(cfg.EnvPath) {
				t.Errorf("EnvPath = %q, want the absolute path of the file read", cfg.EnvPath)
			}
		})
	}
}

func TestLoadConfigMissingExplicitFile(t *testing.T) {
	isolateEnv(t)
	chdirNoDotEnv(t)
	useSystemPath(t, filepath.Join(t.TempDir(), "absent.env"))

	if _, err := LoadConfigFrom(filepath.Join(t.TempDir(), "nope.env")); err == nil {
		t.Error("LoadConfigFrom accepted a --config that does not exist")
	}
	t.Setenv("STOCKROOM_CONFIG", filepath.Join(t.TempDir(), "nope.env"))
	if _, err := LoadConfigFrom(""); err == nil {
		t.Error("LoadConfigFrom accepted a STOCKROOM_CONFIG that does not exist")
	}
}

// TestLoadConfigRefusesRelativePathsWhenInstalled: under systemd the working
// directory is /, so ./uploads would be /uploads.
func TestLoadConfigRefusesRelativePathsWhenInstalled(t *testing.T) {
	isolateEnv(t)
	dir := chdirNoDotEnv(t)
	useSystemPath(t, filepath.Join(dir, "absent.env"))

	path := writeEnv(t, filepath.Join(t.TempDir(), "stockroom.env"), "x",
		"SIGNIN_PHOTOS_DIR=/var/lib/stockroom/cache")
	_, err := LoadConfigFrom(path)
	if err == nil || !strings.Contains(err.Error(), "UPLOADS_DIR") {
		t.Fatalf("relative default UPLOADS_DIR in an installed config: err = %v, want one naming UPLOADS_DIR", err)
	}

	// The same file found as a working copy's .env keeps relative paths.
	isolateEnv(t)
	writeEnv(t, filepath.Join(dir, ".env"), "x")
	if _, err := LoadConfigFrom(""); err != nil {
		t.Errorf("a .env with relative paths was refused: %v", err)
	}
}

func TestLoadConfigPreMigrateDump(t *testing.T) {
	isolateEnv(t)
	chdirNoDotEnv(t)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PreMigrateDump != PreMigrateDumpOff {
		t.Errorf("unset PRE_MIGRATE_DUMP = %q, want off", cfg.PreMigrateDump)
	}
	t.Setenv("PRE_MIGRATE_DUMP", "sometimes")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "PRE_MIGRATE_DUMP") {
		t.Errorf("PRE_MIGRATE_DUMP=sometimes: err = %v, want one naming the key", err)
	}
}
