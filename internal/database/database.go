// Package database implements collections.Store (and the optional
// collections.MoverStore capability) backed by PostgreSQL — database mode.
//
// The schema lives in migrations/ (see .agent/architecture/db-schema.md);
// Open applies pending migrations idempotently at startup. Unlike file
// mode, snapshots and rankings are append-only time series, so History and
// Movers are fully available.
//
// Mode-specific semantics beyond the Store contract:
//
//   - Videos are deduplicated by YouTube ID across collections: one videos
//     row, one collection_videos membership row per collection.
//   - Speakers, topics, and organizations are global entities attached to
//     the video, not to the membership. When two collections disagree, the
//     last import wins (documented tradeoff in db-schema.md).
//   - Entries whose youtubeUrl has not been resolved to a video ID are
//     skipped on upsert: the videos table requires a canonical 11-char ID
//     and the sync engine resolves IDs before upserting (T09).
package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	"github.com/denver/discovery/internal/collections"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
)

// Store is the PostgreSQL-backed collections.Store.
type Store struct {
	db *sql.DB
}

var (
	_ collections.Store      = (*Store)(nil)
	_ collections.MoverStore = (*Store)(nil)
)

// Open connects to PostgreSQL, applies pending migrations, and returns a
// Store. The URL is a standard postgres:// connection string. Errors never
// contain credentials: the URL is reported with userinfo stripped and any
// password scrubbed from driver messages.
func Open(ctx context.Context, databaseURL string) (collections.Store, error) {
	fail := func(step string, err error) error {
		return fmt.Errorf("database: %s %s: %w", step, sanitizeURL(databaseURL), scrubCredentials(err, databaseURL))
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fail("open", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fail("connect", err)
	}
	if err := applyMigrations(ctx, db); err != nil {
		_ = db.Close()
		return nil, fail("migrate", err)
	}
	return &Store{db: db}, nil
}

// Close releases the connection pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// sanitizeURL returns the database URL with userinfo stripped, safe for
// errors and logs. Unparseable URLs are not echoed at all.
func sanitizeURL(databaseURL string) string {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "(unparseable database URL)"
	}
	u.User = nil
	return u.Redacted()
}

// scrubCredentials replaces any password from the database URL that leaks
// into an error message. Driver errors normally omit passwords already;
// this is a belt-and-braces guarantee. The error chain is preserved when
// nothing needs scrubbing.
func scrubCredentials(err error, databaseURL string) error {
	if err == nil {
		return nil
	}
	u, perr := url.Parse(databaseURL)
	if perr != nil || u.User == nil {
		return err
	}
	pass, ok := u.User.Password()
	if !ok || pass == "" || !strings.Contains(err.Error(), pass) {
		return err
	}
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), pass, "xxxxx"))
}

// nullString maps "" to NULL for nullable text columns.
func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// strValue returns the string of a nullable column, "" when NULL.
func strValue(ns sql.NullString) string {
	return ns.String
}

// strPtr returns a pointer to the value of a nullable column, nil when
// NULL. Distinguishes NULL from stored empty strings (overrides, notes).
func strPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}
