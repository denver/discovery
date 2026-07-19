package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"testing"

	"github.com/denver/discovery/internal/collections"
	"github.com/denver/discovery/internal/collections/storetest"
)

// Tests run against a real local PostgreSQL (default unix socket, e.g.
// Homebrew's). Each test creates a throwaway database and drops it on
// cleanup. When the initial admin connection fails (CI without postgres),
// the test skips with a clear message.
//
// Override the admin connection with DISCOVERY_TEST_DATABASE_URL; it must
// grant CREATE DATABASE.

var testDBSeq atomic.Int64

func adminDatabaseURL() string {
	if u := os.Getenv("DISCOVERY_TEST_DATABASE_URL"); u != "" {
		return u
	}
	return "postgres:///postgres?host=/tmp"
}

// openTestDatabase creates a uniquely named database and returns its URL.
// The database is dropped (forcefully, disconnecting stragglers) on test
// cleanup.
func openTestDatabase(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	adminURL := adminDatabaseURL()
	admin, err := sql.Open("pgx", adminURL)
	if err != nil {
		t.Skipf("postgres unavailable (set DISCOVERY_TEST_DATABASE_URL or run local postgres): %v", err)
	}
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Skipf("postgres unavailable (set DISCOVERY_TEST_DATABASE_URL or run local postgres): %v", err)
	}

	name := fmt.Sprintf("discovery_test_%d_%d", os.Getpid(), testDBSeq.Add(1))
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		_ = admin.Close()
		t.Fatalf("create test database %s: %v", name, err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
		_ = admin.Close()
	})

	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse admin URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

// openTestStore opens a Store against a fresh throwaway database.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), openTestDatabase(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s.(*Store)
}

// dataTables are every table holding store data (not migration
// bookkeeping), truncated to reset state between conformance subtests.
var dataTables = []string{
	"collections", "videos", "collection_videos",
	"speakers", "video_speakers", "topics", "video_topics",
	"organizations", "video_organizations",
	"video_snapshots", "rank_snapshots",
}

func truncateAll(t *testing.T, db *sql.DB) {
	t.Helper()
	stmt := "TRUNCATE "
	for i, table := range dataTables {
		if i > 0 {
			stmt += ", "
		}
		stmt += table
	}
	if _, err := db.ExecContext(context.Background(), stmt+" RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// TestConformance runs the shared Store conformance suite against the
// postgres store. One database serves the whole suite; each subtest gets a
// fresh pool over truncated tables.
func TestConformance(t *testing.T) {
	dbURL := openTestDatabase(t)
	storetest.Run(t, func(t *testing.T) collections.Store {
		s, err := Open(context.Background(), dbURL)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		truncateAll(t, s.(*Store).db)
		return s
	})
}
