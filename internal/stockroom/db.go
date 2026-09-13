package stockroom

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultSessionIdle is the idle timeout a session gets when Options leaves
// it unset. It matches the SESSION_IDLE_MINUTES default (CLAUDE.md §7).
const DefaultSessionIdle = 5 * time.Minute

// Options is everything a DB needs besides the connection string. main fills
// it from Config; tests fill in only what the case under test needs.
type Options struct {
	// SessionIdle is how long a session survives without a request. Zero
	// means DefaultSessionIdle.
	SessionIdle time.Duration
	// UploadsDir is where photos are copied and the root /files/ serves.
	// Empty means photo uploads answer ErrNotConfigured.
	UploadsDir string
	// BackupDir is where ExportAllTablesToCSV writes. Empty means backups
	// answer ErrNotConfigured.
	BackupDir string
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
	Sessions *SessionStore

	// UploadsDir and BackupDir come from Options. They are plain values, so
	// a test may point them at a temp dir after Open.
	UploadsDir string
	BackupDir  string
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
		Pool:       pool,
		Sessions:   NewSessionStore(idle),
		UploadsDir: opts.UploadsDir,
		BackupDir:  opts.BackupDir,
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
