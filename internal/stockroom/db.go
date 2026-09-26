package stockroom

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
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

	// EnvPath is the .env file the server was configured from, which the
	// setup wizard writes the failsafe admin into (Config.EnvPath). Empty
	// means there was none, and that step answers ErrNotConfigured.
	EnvPath string

	// photoWall is the sign-in photo wall's reel and the Drive source feeding
	// it (docs/design/signin-photo-wall.html §2, §3), or nil when the feature
	// is off. Read through photoWallParts and written through SetPhotoWall.
	//
	// It is not built by Open, unlike Sessions. Open reports its errors by
	// refusing to start, and the wall's §9 invariant is that no failure in
	// this subsystem may break sign-in -- a bad SIGNIN_PHOTOS_DIR must cost
	// the decoration and nothing else. StartPhotoWall logs and carries on.
	//
	// One atomic pointer to the *pair*, because the wall can now start while
	// the server is running: the admin panel's Google sign-in is the switch
	// (photowall_google.go), so the sign-in screen and the admin screen may be
	// reading these at the moment a Finish press writes them. Two separate
	// fields would let a reader see the new reel beside the old nil source.
	photoWall atomic.Pointer[photoWallParts]

	// photoWallStart serialises starting the wall -- at boot, and from a
	// Google sign-in finishing -- so two presses cannot build two reels over
	// one cache directory. photoWallCfg and photoWallCtx are what StartPhotoWall
	// recorded at boot, kept so a later sign-in can start the wall with the
	// same settings and the server's lifetime rather than a request's.
	// The closet camera's watcher (camera_watch.go), built on first use, and
	// the detector connector a test may replace (nil means Frigate).
	cameraOnce      sync.Once
	cameraW         *cameraWatcher
	detectorFactory func(baseURL string) Detector

	photoWallStart sync.Mutex
	photoWallCfg   *PhotoWallConfig
	photoWallCtx   context.Context
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
	db.Sessions.OnExpire(db.logIdleTimeout)
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

// StartSessionSweeper expires idle sessions every 30 seconds until ctx ends,
// so each idle timeout is logged close to when it happened.
func (db *DB) StartSessionSweeper(ctx context.Context) {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				db.Sessions.Sweep()
			}
		}
	}()
}

// Close releases the pool.
func (db *DB) Close() {
	db.Pool.Close()
}
