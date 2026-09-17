package stockroom

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

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

	// The sign-in photo wall (docs/design/signin-photo-wall.html §8). The
	// remote is the switch: blank disables the feature entirely and no
	// goroutine starts, which is the state every existing .env is in. The
	// live folder is not here -- §7 makes it an admin-panel value seeded
	// from app_settings, and a setting that lives in two places drifts.
	SignInPhotosRemote     string
	SignInPhotosDir        string
	SignInPhotosCount      int
	SignInPhotosBatch      int
	SignInPhotosTTLMinutes int
}

// LoadConfig reads .env (searching the current directory and its parents, so
// `go run ./server` works from the repo root or a subdirectory) and then the
// process environment. Real environment variables win over .env values.
func LoadConfig() (Config, error) {
	if path, ok := findDotEnv(); ok {
		if err := godotenv.Load(path); err != nil {
			return Config{}, fmt.Errorf("load %s: %w", path, err)
		}
	}

	// Values with a sensible local default fall back to it when unset; the
	// admin failsafe and backup dir are deliberately blank until configured.
	cfg := Config{
		DatabaseURL:        getenv("DATABASE_URL", "postgresql://postgres:postgres@127.0.0.1:54322/postgres"),
		ServerAddr:         getenv("SERVER_ADDR", "127.0.0.1:8080"),
		AdminStudentNumber: os.Getenv("ADMIN_STUDENT_NUMBER"),
		AdminPassword:      os.Getenv("ADMIN_PASSWORD"),
		UploadsDir:         getenv("UPLOADS_DIR", "./uploads"),
		BackupDir:          os.Getenv("BACKUP_DIR"),
		PhotoBackupDir:     os.Getenv("PHOTO_BACKUP_DIR"),
		RcloneRemote:       os.Getenv("RCLONE_REMOTE"),

		SignInPhotosRemote: os.Getenv("SIGNIN_PHOTOS_REMOTE"),
		SignInPhotosDir:    getenv("SIGNIN_PHOTOS_DIR", DefaultPhotoWallDir),
	}

	// The idle timeout is the one value that must parse; a bad number is a
	// config error rather than a silent fallback.
	idle := getenv("SESSION_IDLE_MINUTES", "10")
	n, err := strconv.Atoi(idle)
	if err != nil || n <= 0 {
		return Config{}, fmt.Errorf("SESSION_IDLE_MINUTES must be a positive integer, got %q", idle)
	}
	cfg.SessionIdleMinutes = n

	// The photo wall's three numbers follow the same rule as the idle
	// timeout: a value that does not parse is a typo in .env, and a typo that
	// silently falls back to the default is one nobody ever finds.
	for _, v := range []struct {
		key string
		def int
		out *int
	}{
		{"SIGNIN_PHOTOS_COUNT", DefaultPhotoWallCount, &cfg.SignInPhotosCount},
		{"SIGNIN_PHOTOS_BATCH", DefaultPhotoWallBatch, &cfg.SignInPhotosBatch},
		{"SIGNIN_PHOTOS_TTL_MINUTES", int(DefaultPhotoWallTTL / time.Minute), &cfg.SignInPhotosTTLMinutes},
	} {
		n, err := positiveInt(v.key, v.def)
		if err != nil {
			return Config{}, err
		}
		*v.out = n
	}

	return cfg, nil
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
