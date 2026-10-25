// Package demogate isolates deterministic verification from the demo workload.
package demogate

import (
	"context"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
)

// Acquire holds a session advisory lock until release. PostgreSQL releases it
// automatically if either CLI crashes. API transactions use a different lock.
// Local CLI runs without DATABASE_URL can operate against a remote API directly.
func Acquire(ctx context.Context) (release func(), err error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return func() {}, nil
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock(6435, 0)`); err != nil {
		_ = conn.Close(context.Background())
		return nil, err
	}
	return func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}, nil
}
