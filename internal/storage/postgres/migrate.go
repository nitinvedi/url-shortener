package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.up.sql
var migrationFS embed.FS

const migrationLockID int64 = 721903214

type migration struct {
	version  int64
	name     string
	checksum string
	sql      string
}

// Migrate applies embedded up migrations in version order. SHA-256 checksums
// prevent an applied migration from being silently edited. Migrations are
// forward-only; corrective changes must be added as a new migration.
func Migrate(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database is nil")
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()

	var locked bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", migrationLockID).Scan(&locked); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("another process is currently running database migrations")
	}
	defer func() {
		var unlocked bool
		_ = conn.QueryRowContext(context.Background(), "SELECT pg_advisory_unlock($1)", migrationLockID).Scan(&unlocked)
	}()

	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version BIGINT PRIMARY KEY,
			name TEXT NOT NULL,
			checksum CHAR(64) NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	for _, item := range migrations {
		var appliedName, appliedChecksum string
		err := conn.QueryRowContext(ctx,
			"SELECT name, checksum FROM schema_migrations WHERE version = $1",
			item.version,
		).Scan(&appliedName, &appliedChecksum)

		switch {
		case err == nil:
			if appliedName != item.name || strings.TrimSpace(appliedChecksum) != item.checksum {
				return fmt.Errorf("applied migration %d (%s) does not match its embedded file; create a new migration instead of editing history", item.version, item.name)
			}
			continue
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("read migration %d status: %w", item.version, err)
		}

		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", item.version, err)
		}
		if _, err := tx.ExecContext(ctx, item.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d (%s): %w", item.version, item.name, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)",
			item.version, item.name, item.checksum,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d (%s): %w", item.version, item.name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d (%s): %w", item.version, item.name, err)
		}
	}
	return nil
}

func loadMigrations() ([]migration, error) {
	files, err := fs.Glob(migrationFS, "migrations/*.up.sql")
	if err != nil {
		return nil, fmt.Errorf("list embedded migrations: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no embedded SQL migrations found")
	}

	migrations := make([]migration, 0, len(files))
	for _, filename := range files {
		base := path.Base(filename)
		parts := strings.SplitN(base, "_", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid migration filename %q: expected <version>_<name>.up.sql", base)
		}
		version, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("invalid migration version in filename %q", base)
		}
		content, err := migrationFS.ReadFile(filename)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", filename, err)
		}
		sum := sha256.Sum256(content)
		name := strings.TrimSuffix(parts[1], ".up.sql")
		migrations = append(migrations, migration{
			version: version,
			name: name,
			checksum: fmt.Sprintf("%x", sum),
			sql: string(content),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})
	for i := 1; i < len(migrations); i++ {
		if migrations[i-1].version == migrations[i].version {
			return nil, fmt.Errorf("duplicate migration version %d", migrations[i].version)
		}
	}
	return migrations, nil
}
