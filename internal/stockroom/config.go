package stockroom

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/joho/godotenv"
)

// Config holds every setting the server and CLIs read from the environment.
// See CLAUDE.md §9 for what each variable means.
type Config struct {
	DatabaseURL        string
	ServerAddr         string
	AdminStudentNumber string
	AdminPassword      string
	UploadsDir         string
	SessionIdleMinutes int

	// These three are a first-boot seed for the app_settings row and nothing
	// more (docs/design/backup.md §C.2). EnsureSettings copies them into null
	// columns once; after that the admin panel owns them and the environment
	// is ignored, so a value an admin typed is never reverted by a restart.
	BackupDir      string
	PhotoBackupDir string
	RcloneRemote   string

	// The sign-in photo wall (docs/design/signin-photo-wall.html §8). It
	// reads Drive through the one Google remote the application shares
	// (google.go), so nothing here names a remote any more; the switch is an
	// admin signing in to Google. Until somebody has, no set is built and no
	// goroutine starts.
	//
	// SignInPhotosFolderID is a first-boot seed rather than a setting, the
	// same shape as the three backup values above: §7 makes the live folder
	// an admin-panel value in app_settings, and a value that lives in two
	// places drifts. Until §7's column exists this is simply where the folder
	// comes from, and §7 takes ownership of it without changing its meaning
	// here. It is a capability, not a label -- see §7 on why it is never sent
	// back to a client and must be redacted from the backup export the moment
	// it reaches app_settings.
	//
	// SignInPhotosDir is where the Drive folder's manifest is kept. The
	// photographs themselves live in memory only (photowall.go).
	SignInPhotosFolderID      string
	SignInPhotosDir           string
	SignInPhotosManifestHours int

	// PreMigrateDump is PRE_MIGRATE_DUMP: whether serve runs pg_dump before
	// applying a migration (premigrate.go). PreMigrateDir is PRE_MIGRATE_DIR,
	// where the dumps go; empty means <BACKUP_DIR>/pre-migrate. PGDump is PG_DUMP, an explicit
	// path to that binary, and RcloneBinary is RCLONE_BINARY, the same for
	// rclone. Both are empty unless a service manager's short PATH needs them.
	PreMigrateDump PreMigrateDumpMode
	PreMigrateDir  string
	PGDump         string
	RcloneBinary   string

	// EnvPath is the .env file this configuration was read from, or "" when
	// there was none. The setup wizard writes the failsafe admin into it,
	// because that account has to survive the database being lost and so
	// cannot live in the database (CLAUDE.md §7).
	EnvPath string
	// Source says how EnvPath was found. An installed server (a --config
	// flag, STOCKROOM_CONFIG or a system path) refuses relative data paths;
	// a working copy's .env keeps them.
	Source ConfigSource
}

// ConfigSource is where LoadConfigFrom found its file, in precedence order.
type ConfigSource string

const (
	ConfigFromFlag   ConfigSource = "--config"
	ConfigFromEnv    ConfigSource = "STOCKROOM_CONFIG"
	ConfigFromDotEnv ConfigSource = ".env in a working copy"
	ConfigFromSystem ConfigSource = "system path"
	ConfigNotFound   ConfigSource = "no config file"
)

// Installed reports whether the config belongs to an install rather than a
// working copy. Relative paths in an installed config would resolve against
// whatever directory the service manager started in, which is / under systemd.
func (c Config) Installed() bool {
	switch c.Source {
	case ConfigFromFlag, ConfigFromEnv, ConfigFromSystem:
		return true
	}
	return false
}

// PreMigrateDumpDir is where serve writes the pre-migrate dump: PRE_MIGRATE_DIR,
// else <BACKUP_DIR>/pre-migrate, else "" to use the backup folder saved in the
// admin panel.
func (c Config) PreMigrateDumpDir() string {
	switch {
	case c.PreMigrateDir != "":
		return c.PreMigrateDir
	case c.BackupDir != "":
		return filepath.Join(c.BackupDir, "pre-migrate")
	}
	return ""
}

// Describe is the one line every subcommand prints about its config.
func (c Config) Describe() string {
	if c.EnvPath == "" {
		return "config: none found, using defaults and the environment"
	}
	return fmt.Sprintf("config: %s (%s)", c.EnvPath, c.Source)
}

// PreMigrateDumpMode is PRE_MIGRATE_DUMP's value.
type PreMigrateDumpMode string

const (
	// PreMigrateDumpOff skips the dump. It is the default, so a development
	// machine with no pg_dump starts as before.
	PreMigrateDumpOff PreMigrateDumpMode = "off"
	// PreMigrateDumpRequired refuses to migrate without a dump. Setup writes it.
	PreMigrateDumpRequired PreMigrateDumpMode = "required"
)

// systemConfigPaths lists where an installed Stockroom keeps its config, for
// this platform. A variable so tests can point it at a temporary directory.
var systemConfigPaths = defaultSystemConfigPaths

func defaultSystemConfigPaths() []string {
	switch runtime.GOOS {
	case "linux":
		return []string{"/etc/stockroom/stockroom.env"}
	case "darwin":
		return []string{filepath.Join(BrewPrefix(), "var", "stockroom", "stockroom.env")}
	}
	return nil
}

// BrewPrefix is Homebrew's prefix without running brew, which a LaunchDaemon's
// PATH doesn't have: HOMEBREW_PREFIX when set, else the default for this CPU.
func BrewPrefix() string {
	if p := os.Getenv("HOMEBREW_PREFIX"); p != "" {
		return p
	}
	if runtime.GOARCH == "arm64" {
		return "/opt/homebrew"
	}
	return "/usr/local"
}

// FindConfig picks the config file. The first match wins: the --config flag,
// STOCKROOM_CONFIG, a .env found walking up from the working directory (so
// development is unchanged), then the system path. A path given by flag or
// variable must exist; pointing at a missing file is a typo, not a request
// for defaults.
func FindConfig(flagPath string) (string, ConfigSource, error) {
	for _, explicit := range []struct {
		path   string
		source ConfigSource
	}{
		{flagPath, ConfigFromFlag},
		{os.Getenv("STOCKROOM_CONFIG"), ConfigFromEnv},
	} {
		if explicit.path == "" {
			continue
		}
		abs, err := filepath.Abs(explicit.path)
		if err != nil {
			return "", "", fmt.Errorf("%s %q: %w", explicit.source, explicit.path, err)
		}
		if st, err := os.Stat(abs); err != nil {
			return "", "", fmt.Errorf("%s %q: %w", explicit.source, explicit.path, err)
		} else if st.IsDir() {
			return "", "", fmt.Errorf("%s %q is a directory", explicit.source, explicit.path)
		}
		return abs, explicit.source, nil
	}
	if path, ok := findDotEnv(); ok {
		return path, ConfigFromDotEnv, nil
	}
	for _, path := range systemConfigPaths() {
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return path, ConfigFromSystem, nil
		}
	}
	return "", ConfigNotFound, nil
}

// LoadConfig is LoadConfigFrom with no --config flag.
func LoadConfig() (Config, error) {
	return LoadConfigFrom("")
}

// LoadConfigFrom reads the file FindConfig picks and then the process
// environment. Real environment variables win over the file's values.
func LoadConfigFrom(flagPath string) (Config, error) {
	envPath, source, err := FindConfig(flagPath)
	if err != nil {
		return Config{}, err
	}
	if envPath != "" {
		if err := godotenv.Load(envPath); err != nil {
			return Config{}, fmt.Errorf("load %s: %w", envPath, err)
		}
	}

	// Values with a sensible local default fall back to it when unset; the
	// admin failsafe and backup dir are deliberately blank until configured.
	cfg := Config{
		EnvPath:            envPath,
		Source:             source,
		DatabaseURL:        getenv("DATABASE_URL", "postgresql://postgres:postgres@127.0.0.1:54322/postgres"),
		ServerAddr:         getenv("SERVER_ADDR", "127.0.0.1:8080"),
		AdminStudentNumber: os.Getenv("ADMIN_STUDENT_NUMBER"),
		AdminPassword:      os.Getenv("ADMIN_PASSWORD"),
		UploadsDir:         getenv("UPLOADS_DIR", "./uploads"),
		BackupDir:          os.Getenv("BACKUP_DIR"),
		PhotoBackupDir:     os.Getenv("PHOTO_BACKUP_DIR"),
		RcloneRemote:       os.Getenv("RCLONE_REMOTE"),

		SignInPhotosFolderID: os.Getenv("SIGNIN_PHOTOS_FOLDER_ID"),
		SignInPhotosDir:      getenv("SIGNIN_PHOTOS_DIR", DefaultPhotoWallDir),

		PreMigrateDir: os.Getenv("PRE_MIGRATE_DIR"),
		PGDump:        os.Getenv("PG_DUMP"),
		RcloneBinary:  os.Getenv("RCLONE_BINARY"),
	}

	switch mode := PreMigrateDumpMode(getenv("PRE_MIGRATE_DUMP", string(PreMigrateDumpOff))); mode {
	case PreMigrateDumpOff, PreMigrateDumpRequired:
		cfg.PreMigrateDump = mode
	default:
		return Config{}, fmt.Errorf("PRE_MIGRATE_DUMP must be %q or %q, got %q", PreMigrateDumpRequired, PreMigrateDumpOff, mode)
	}

	// The idle timeout is the one value that must parse; a bad number is a
	// config error rather than a silent fallback.
	idle := getenv("SESSION_IDLE_MINUTES", "10")
	n, err := strconv.Atoi(idle)
	if err != nil || n <= 0 {
		return Config{}, fmt.Errorf("SESSION_IDLE_MINUTES must be a positive integer, got %q", idle)
	}
	cfg.SessionIdleMinutes = n

	// The manifest interval follows the same rule as the idle timeout: a
	// value that does not parse is a typo in .env, and a typo that silently
	// falls back to the default is one nobody ever finds. The photo wall's
	// set size is an admin setting in app_settings, not a variable here.
	if cfg.SignInPhotosManifestHours, err = positiveInt("SIGNIN_PHOTOS_MANIFEST_HOURS", DefaultPhotoWallManifestHours); err != nil {
		return Config{}, err
	}

	if err := cfg.checkInstalledPaths(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// checkInstalledPaths refuses a relative data path in an installed config.
// The backup folders already follow this rule (validateDir, docs/decisions.md
// 2026-09-21); these are the paths the server writes to before any admin has
// chosen anything.
func (c Config) checkInstalledPaths() error {
	if !c.Installed() {
		return nil
	}
	var errs []error
	for _, p := range []struct{ key, value string }{
		{"UPLOADS_DIR", c.UploadsDir},
		{"SIGNIN_PHOTOS_DIR", c.SignInPhotosDir},
		{"BACKUP_DIR", c.BackupDir},
		{"PHOTO_BACKUP_DIR", c.PhotoBackupDir},
		{"PRE_MIGRATE_DIR", c.PreMigrateDir},
		{"PG_DUMP", c.PGDump},
		{"RCLONE_BINARY", c.RcloneBinary},
	} {
		if p.value != "" && !filepath.IsAbs(p.value) {
			errs = append(errs, fmt.Errorf("%s must be an absolute path in %s, got %q", p.key, c.EnvPath, p.value))
		}
	}
	return errors.Join(errs...)
}

// positiveInt reads key as a positive integer, falling back to def when it is
// unset or empty. Anything else -- a word, a zero, a negative -- is a config
// error naming the variable.
func positiveInt(key string, def int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", key, raw)
	}
	return n, nil
}

// getenv returns the environment value for key, or def when it is unset or
// empty.
func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// findDotEnv walks up from the working directory looking for a .env file,
// stopping at the filesystem root. This lets `go run ./server` and the CLIs
// be started from any subdirectory of the repo.
func findDotEnv() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		p := filepath.Join(dir, ".env")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
