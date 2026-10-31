package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// PoolConfig controls database/sql connection-pool behavior.
type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
	ConnectTimeout  time.Duration
}

// Open creates and verifies a PostgreSQL connection pool.
func Open(ctx context.Context, databaseURL string, cfg PoolConfig) (*sql.DB, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("database URL is required")
	}
	if cfg.MaxOpenConns < 1 {
		return nil, fmt.Errorf("maximum open connections must be greater than zero")
	}
	if cfg.MaxIdleConns < 0 || cfg.MaxIdleConns > cfg.MaxOpenConns {
		return nil, fmt.Errorf("maximum idle connections must be between zero and maximum open connections")
	}
	if cfg.ConnMaxLifetime <= 0 || cfg.ConnMaxIdleTime <= 0 || cfg.ConnectTimeout <= 0 {
		return nil, fmt.Errorf("connection lifetime, idle time, and connect timeout must be greater than zero")
	}

	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return db, nil
}
