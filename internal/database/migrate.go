package database

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/denver/discovery/migrations"
)

// migrationLockKey is the pg_advisory_lock key serializing migration runs
// across processes sharing one database. Arbitrary but stable.
const migrationLockKey = 0x00d15c0e17 // "discovery", roughly

// applyMigrations applies every pending migrations/*.up.sql in filename
// order, each inside its own transaction, tracked in schema_migrations.
// Idempotent: already-applied versions are skipped, so calling Open on an
// up-to-date database is a no-op.
func applyMigrations(ctx context.Context, db *sql.DB) error {
	names, err := fs.Glob(migrations.FS, "*.up.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(names)

	// One session holds the advisory lock for the whole run so concurrent
	// starts (e.g. two replicas booting) apply migrations exactly once.
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, int64(migrationLockKey)); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, int64(migrationLockKey))
	}()

	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    BIGINT PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			return err
		}
		var applied bool
		if err := conn.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if applied {
			continue
		}
		stmts, err := migrations.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(stmts)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, version, name,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}

// migrationVersion parses the numeric prefix of NNNNNN_name.up.sql.
func migrationVersion(name string) (int64, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("migration %s: want NNNNNN_name.up.sql", name)
	}
	v, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("migration %s: bad version prefix: %w", name, err)
	}
	return v, nil
}
