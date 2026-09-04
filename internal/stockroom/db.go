package stockroom

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps the pgx connection pool. Every stockroom operation hangs off it;
// this package is the only code in the repo that talks to Postgres.
type DB struct {
	Pool *pgxpool.Pool
}

// Open connects to Postgres at databaseURL and verifies the connection with a
// ping, so a bad URL or a stopped database fails at startup rather than on
// the first request.
func Open(ctx context.Context, databaseURL string) (*DB, error) {
	pcfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	// Single-machine deployment with two local frontends: a small pool is
	// plenty, and idle connections are dropped so the Docker stack can be
	// restarted underneath us without leaking dead sockets.
	pcfg.MaxConns = 8
	pcfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	db := &DB{Pool: pool}
	if err := db.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return db, nil
}

// Ping runs a trivial query to confirm the database is reachable. It bounds
// the wait so a hung database surfaces as an error instead of a stall.
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
