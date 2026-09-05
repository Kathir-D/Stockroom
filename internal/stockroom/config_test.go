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
	if cfg.SessionIdleMinutes != 30 {
		t.Errorf("SessionIdleMinutes = %d, want 30", cfg.SessionIdleMinutes)
	}
	// These have no default on purpose: an unconfigured failsafe admin or
	// backup target must be visibly empty, not silently guessed.
	if cfg.AdminStudentNumber != "" || cfg.AdminPassword != "" || cfg.BackupDir != "" {
		t.Errorf("expected admin/backup settings to stay empty, got %+v", cfg)
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	isolateEnv(t)
	chdirNoDotEnv(t)

	t.Setenv("DATABASE_URL", "postgresql://u:p@db:5432/x")
	t.Setenv("SERVER_ADDR", "127.0.0.1:9999")
	t.Setenv("ADMIN_STUDENT_NUMBER", "123456")
	t.Setenv("ADMIN_PASSWORD", "hunter2")
	t.Setenv("UPLOADS_DIR", "/var/uploads")
	t.Setenv("BACKUP_DIR", "/drive/backups")
	t.Setenv("SESSION_IDLE_MINUTES", "45")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	want := Config{
		DatabaseURL:        "postgresql://u:p@db:5432/x",
		ServerAddr:         "127.0.0.1:9999",
		AdminStudentNumber: "123456",
		AdminPassword:      "hunter2",
		UploadsDir:         "/var/uploads",
		BackupDir:          "/drive/backups",
		SessionIdleMinutes: 45,
	}
	if cfg != want {
		t.Errorf("LoadConfig() = %+v, want %+v", cfg, want)
	}
}

func TestLoadConfigReadsDotEnv(t *testing.T) {
	isolateEnv(t)
	dir := chdirNoDotEnv(t)

	writeFile(t, filepath.Join(dir, ".env"), "DATABASE_URL=postgresql://from-dotenv/db\nSESSION_IDLE_MINUTES=5\nBACKUP_DIR=/from/dotenv\n")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.DatabaseURL != "postgresql://from-dotenv/db" {
		t.Errorf("DatabaseURL = %q, want the .env value", cfg.DatabaseURL)
	}
	if cfg.SessionIdleMinutes != 5 {
		t.Errorf("SessionIdleMinutes = %d, want 5", cfg.SessionIdleMinutes)
	}
	if cfg.BackupDir != "/from/dotenv" {
		t.Errorf("BackupDir = %q, want the .env value", cfg.BackupDir)
	}
	// Unmentioned keys still fall back to their defaults.
	if cfg.ServerAddr != "127.0.0.1:8080" {
		t.Errorf("ServerAddr = %q, want the default", cfg.ServerAddr)
	}
}

func TestLoadConfigEnvBeatsDotEnv(t *testing.T) {
	isolateEnv(t)
	dir := chdirNoDotEnv(t)
	writeFile(t, filepath.Join(dir, ".env"), "SERVER_ADDR=127.0.0.1:1111\n")

	t.Setenv("SERVER_ADDR", "127.0.0.1:2222")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ServerAddr != "127.0.0.1:2222" {
		t.Errorf("ServerAddr = %q, want the real environment to win over .env", cfg.ServerAddr)
	}
}

// A variable exported as empty (`export BACKUP_DIR=` in a shell) counts as
// "already set" to godotenv, so the .env value is NOT loaded and the field
// stays empty. This is surprising enough to be worth pinning down.
func TestLoadConfigEmptyEnvVarSuppressesDotEnv(t *testing.T) {
	isolateEnv(t)
	dir := chdirNoDotEnv(t)
	writeFile(t, filepath.Join(dir, ".env"), "BACKUP_DIR=/from/dotenv\n")

	t.Setenv("BACKUP_DIR", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.BackupDir != "" {
		t.Errorf("BackupDir = %q, want empty (an exported-empty var shadows .env)", cfg.BackupDir)
	}
}

// An exported-empty variable that has a default does fall back to the default,
// because getenv treats "" as unset.
func TestLoadConfigEmptyEnvVarFallsBackToDefault(t *testing.T) {
	isolateEnv(t)
	chdirNoDotEnv(t)
	t.Setenv("SERVER_ADDR", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ServerAddr != "127.0.0.1:8080" {
		t.Errorf("ServerAddr = %q, want the default", cfg.ServerAddr)
	}
}

func TestLoadConfigRejectsBadSessionIdleMinutes(t *testing.T) {
	for _, v := range []string{"abc", "0", "-5", "1.5", " 30", "30m"} {
		t.Run(v, func(t *testing.T) {
			isolateEnv(t)
			chdirNoDotEnv(t)
			t.Setenv("SESSION_IDLE_MINUTES", v)

			if _, err := LoadConfig(); err == nil {
				t.Fatalf("LoadConfig() with SESSION_IDLE_MINUTES=%q: want an error, got nil", v)
			}
		})
	}
}

func TestLoadConfigRejectsUnparseableDotEnv(t *testing.T) {
	isolateEnv(t)
	dir := chdirNoDotEnv(t)
	// A line that is neither a comment nor KEY=VALUE.
	writeFile(t, filepath.Join(dir, ".env"), "this is not valid\n")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig() with a malformed .env: want an error, got nil")
	}
}

func TestGetenv(t *testing.T) {
	t.Setenv("STOCKROOM_TEST_GETENV", "value")
	if got := getenv("STOCKROOM_TEST_GETENV", "def"); got != "value" {
		t.Errorf("getenv(set) = %q, want %q", got, "value")
	}
	t.Setenv("STOCKROOM_TEST_GETENV", "")
	if got := getenv("STOCKROOM_TEST_GETENV", "def"); got != "def" {
		t.Errorf("getenv(empty) = %q, want the default", got)
	}
	if got := getenv("STOCKROOM_TEST_GETENV_UNSET", "def"); got != "def" {
		t.Errorf("getenv(unset) = %q, want the default", got)
	}
}

func TestFindDotEnvWalksUp(t *testing.T) {
	isolateEnv(t)
	dir := chdirNoDotEnv(t)
	// .env two levels above the working directory — the case that makes
	// `go run ./server` work from any subdirectory of the repo.
	repoRoot := filepath.Dir(filepath.Dir(dir))
	want := filepath.Join(repoRoot, ".env")
	writeFile(t, want, "SERVER_ADDR=127.0.0.1:3333\n")

	got, ok := findDotEnv()
	if !ok {
		t.Fatal("findDotEnv() found nothing, want the ancestor .env")
	}
	if got != want {
		t.Errorf("findDotEnv() = %q, want %q", got, want)
	}
}

func TestFindDotEnvPrefersNearest(t *testing.T) {
	isolateEnv(t)
	dir := chdirNoDotEnv(t)
	parent := filepath.Dir(dir)
	writeFile(t, filepath.Join(filepath.Dir(parent), ".env"), "SERVER_ADDR=far\n")
	near := filepath.Join(dir, ".env")
	writeFile(t, near, "SERVER_ADDR=near\n")

	got, _ := findDotEnv()
	if got != near {
		t.Errorf("findDotEnv() = %q, want the nearest .env %q", got, near)
	}
}

// A directory named .env must not be mistaken for a config file.
func TestFindDotEnvIgnoresDirectory(t *testing.T) {
	isolateEnv(t)
	dir := chdirNoDotEnv(t)
	if err := os.Mkdir(filepath.Join(dir, ".env"), 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(filepath.Dir(dir), ".env")
	writeFile(t, real, "SERVER_ADDR=127.0.0.1:4444\n")

	got, ok := findDotEnv()
	if !ok || got != real {
		t.Errorf("findDotEnv() = %q (%v), want the real file %q", got, ok, real)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
