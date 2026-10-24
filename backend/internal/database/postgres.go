package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolStats represents connection pool telemetry from pgxpool.
type PoolStats struct {
	TotalConns    int32 `json:"total_conns"`
	IdleConns     int32 `json:"idle_conns"`
	AcquiredConns int32 `json:"acquired_conns"`
	MaxConns      int32 `json:"max_conns"`
}

// HealthReport represents the database status returned by /health.
type HealthReport struct {
	Status    string    `json:"status"`
	Driver    string    `json:"driver"`
	LatencyMs float64   `json:"latency_ms"`
	Pool      PoolStats `json:"pool"`
	Error     string    `json:"error,omitempty"`
}

// DiagnosticReport represents detailed PostgreSQL diagnostics returned by /api/v1/db/check.
type DiagnosticReport struct {
	Connected       bool      `json:"connected"`
	Driver          string    `json:"driver"`
	ServerVersion   string    `json:"server_version"`
	CurrentDatabase string    `json:"current_database"`
	CurrentUser     string    `json:"current_user"`
	ServerTime      time.Time `json:"server_time"`
	LatencyMs       float64   `json:"latency_ms"`
	TotalChecks     int64     `json:"total_checks"`
	Pool            PoolStats `json:"pool"`
	Error           string    `json:"error,omitempty"`
}

// Probe defines the interface used by HTTP handlers to inspect database health.
type Probe interface {
	Health(ctx context.Context) (HealthReport, error)
	Diagnostics(ctx context.Context) (DiagnosticReport, error)
	Close()
}

// Postgres wraps a pgxpool.Pool and implements Probe.
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgresPool creates a configured pgxpool.Pool, verifies connectivity, and runs migrations + seed.
func NewPostgresPool(ctx context.Context, dsn string, maxConns, minConns int32) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}

	cfg.MaxConns = maxConns
	cfg.MinConns = minConns
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 15 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	pg := &Postgres{pool: pool}
	if err := pg.MigrateAndSeed(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate and seed schema: %w", err)
	}

	return pg, nil
}

// Pool returns the underlying pgxpool.Pool.
func (p *Postgres) Pool() *pgxpool.Pool {
	return p.pool
}

// Health pings PostgreSQL and returns pool statistics.
func (p *Postgres) Health(ctx context.Context) (HealthReport, error) {
	start := time.Now()
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	err := p.pool.Ping(pingCtx)
	latencyMs := float64(time.Since(start).Microseconds()) / 1000.0

	stats := p.pool.Stat()
	poolStats := PoolStats{
		TotalConns:    stats.TotalConns(),
		IdleConns:     stats.IdleConns(),
		AcquiredConns: stats.AcquiredConns(),
		MaxConns:      stats.MaxConns(),
	}

	if err != nil {
		return HealthReport{
			Status:    "down",
			Driver:    "pgx/v5",
			LatencyMs: latencyMs,
			Pool:      poolStats,
			Error:     err.Error(),
		}, err
	}

	return HealthReport{
		Status:    "up",
		Driver:    "pgx/v5",
		LatencyMs: latencyMs,
		Pool:      poolStats,
	}, nil
}

// Diagnostics runs an active SQL diagnostic query and records the check in PostgreSQL 16.
func (p *Postgres) Diagnostics(ctx context.Context) (DiagnosticReport, error) {
	start := time.Now()
	diagCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var (
		version     string
		currentDB   string
		currentUser string
		serverTime  time.Time
		totalChecks int64
	)

	query := `
		WITH inserted AS (
			INSERT INTO "dz-connection-checks" (checked_at, latency_ms)
			VALUES (NOW(), 0)
			RETURNING id
		)
		SELECT
			version(),
			current_database(),
			current_user,
			NOW(),
			(SELECT COUNT(*) FROM "dz-connection-checks") + 1
	`

	err := p.pool.QueryRow(diagCtx, query).Scan(
		&version,
		&currentDB,
		&currentUser,
		&serverTime,
		&totalChecks,
	)
	latencyMs := float64(time.Since(start).Microseconds()) / 1000.0

	stats := p.pool.Stat()
	poolStats := PoolStats{
		TotalConns:    stats.TotalConns(),
		IdleConns:     stats.IdleConns(),
		AcquiredConns: stats.AcquiredConns(),
		MaxConns:      stats.MaxConns(),
	}

	if err != nil {
		return DiagnosticReport{
			Connected: false,
			Driver:    "pgx/v5",
			LatencyMs: latencyMs,
			Pool:      poolStats,
			Error:     err.Error(),
		}, err
	}

	return DiagnosticReport{
		Connected:       true,
		Driver:          "pgx/v5",
		ServerVersion:   version,
		CurrentDatabase: currentDB,
		CurrentUser:     currentUser,
		ServerTime:      serverTime,
		LatencyMs:       latencyMs,
		TotalChecks:     totalChecks,
		Pool:            poolStats,
	}, nil
}

// Close closes the underlying pgxpool.Pool.
func (p *Postgres) Close() {
	if p.pool != nil {
		p.pool.Close()
	}
}
