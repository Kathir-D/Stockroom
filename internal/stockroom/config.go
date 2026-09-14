package stockroom

import (
	"fmt"
	"os"
	"path/filepath"
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
	BackupDir          string
	SessionIdleMinutes int
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
	}

	// The idle timeout is the one value that must parse; a bad number is a
	// config error rather than a silent fallback.
	idle := getenv("SESSION_IDLE_MINUTES", "10")
	n, err := strconv.Atoi(idle)
	if err != nil || n <= 0 {
		return Config{}, fmt.Errorf("SESSION_IDLE_MINUTES must be a positive integer, got %q", idle)
	}
	cfg.SessionIdleMinutes = n

	return cfg, nil
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
