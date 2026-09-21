package stockroom

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultSessionIdle is the idle timeout a session gets when Options leaves
// it unset. It matches the SESSION_IDLE_MINUTES default (CLAUDE.md §7).
const DefaultSessionIdle = 10 * time.Minute

// Options is everything a DB needs besides the connection string. main fills
// it from Config; tests fill in only what the case under test needs.
type Options struct {
	// SessionIdle is how long a session survives without a request. Zero
	// means DefaultSessionIdle.
	SessionIdle time.Duration
	// UploadsDir is where photos are copied and the root /files/ serves.
	// Empty means photo uploads answer ErrNotConfigured.
	UploadsDir string
	// BackupDir and PhotoBackupDir are *overrides* for the folders in
	// app_settings, not the configured values. main leaves both empty so
	// production always reads the settings row an admin can edit
	// (docs/design/backup.md §C.2); tests set them to a temp directory so a
	// run never touches whatever the machine has configured.
	BackupDir      string
	PhotoBackupDir string
}

// DB is the package's one handle: the connection pool, the session store and
// the two directories the package writes to. Every stockroom operation hangs
// off it; this package is the only code in the repo that talks to Postgres.
type DB struct {
	Pool *pgxpool.Pool

	// Sessions is the live session store shared by both frontends
	// (CLAUDE.md §4). Account operations that must sign a user out (an admin
	// delete or password reset, §7) reach it directly, so the rule holds for
	// every caller and not only the HTTP handlers.
	// Sessions is never nil on a DB that Open built; the package assumes that
	// and does not guard it, so a DB literal must set it (NewSessionStore).
	Sessions *SessionStore

	// UploadsDir comes from Options. BackupDir and PhotoBackupDir are the
	// overrides described there: empty in production, where the app_settings
	// row decides. All three are plain values, so a test may point them at a
	// temp dir after Open.
	UploadsDir     string
	BackupDir      string
	PhotoBackupDir string

	// PhotoWall is the sign-in photo wall's reel
	// (docs/design/signin-photo-wall.html §2), or nil when the feature is
	// off. Every method on it is nil-safe, so callers do not branch on this.
	//
	// It is assigned by the caller rather than built by Open, unlike
	// Sessions. Open reports its errors by refusing to start, and the wall's
	// §9 invariant is that no failure in this subsystem may break sign-in --
	// a bad SIGNIN_PHOTOS_DIR must cost the decoration and nothing else. So
	// server/main.go calls NewPhotoWall, logs a warning on failure and serves
	// without one, which is exactly how it already treats the failsafe admin.
	PhotoWall *PhotoWall

	// PhotoWallSource is where that reel's photographs come from (§3), or nil
	// when rclone is missing, no remote is configured, or the reel is off.
	// Assigned alongside PhotoWall by server/main.go, for the same reason.
	//
	// It is the concrete type rather than the PhotoSource interface the reel
	// holds, because §7's admin screen needs three things no photo source in
	// general has: the folder switch, the manifest's listing progress, and
	// the reachability probe a pasted link is validated against. Every method
	// on it is nil-safe too.
	PhotoWallSource *DrivePhotoSource
}

// Open connects to Postgres at databaseURL and verifies the connection with a
// ping, so a bad URL or a stopped database fails at startup rather than on
// the first request.
func Open(ctx context.Context, databaseURL string, opts Options) (*DB, error) {
	pcfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	// Localhost, a handful of clients; a small pool avoids hogging Postgres
	// connections that Studio and the CLI also want.
	pcfg.MaxConns = 8
	pcfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	idle := opts.SessionIdle
	if idle <= 0 {
		idle = DefaultSessionIdle
	}
	db := &DB{
		Pool:           pool,
		Sessions:       NewSessionStore(idle),
		UploadsDir:     opts.UploadsDir,
		BackupDir:      opts.BackupDir,
		PhotoBackupDir: opts.PhotoBackupDir,
	}
	if err := db.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return db, nil
}

// Ping runs a trivial query with a short timeout. Used by Open and by
// GET /health.
func (db *DB) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var one int
	if err := db.Pool.QueryRow(ctx, "select 1").Scan(&one); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

// Close releases the pool.
func (db *DB) Close() {
	db.Pool.Close()
}
