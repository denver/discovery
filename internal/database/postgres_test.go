package database

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/denver/discovery/internal/collections"
	"github.com/denver/discovery/migrations"
)

const (
	tVidA = "AAAAAAAAAAA"
	tVidB = "BBBBBBBBBBB"
	tVidC = "CCCCCCCCCCC"
	tVidD = "DDDDDDDDDDD"
)

func testCollection(slug string, ids ...string) *collections.Collection {
	c := &collections.Collection{
		SchemaVersion: "1.0",
		Slug:          slug,
		Title:         "Collection " + slug,
	}
	for _, id := range ids {
		c.Videos = append(c.Videos, collections.VideoEntry{YouTubeID: id})
	}
	return c
}

func mustUpsert(t *testing.T, s *Store, c *collections.Collection) {
	t.Helper()
	if err := s.UpsertCollection(context.Background(), c); err != nil {
		t.Fatalf("UpsertCollection(%s): %v", c.Slug, err)
	}
}

func countRow(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func listedIDs(t *testing.T, s *Store, slug string) []string {
	t.Helper()
	videos, err := s.ListVideos(context.Background(), slug)
	if err != nil {
		t.Fatalf("ListVideos(%s): %v", slug, err)
	}
	ids := make([]string, len(videos))
	for i, v := range videos {
		ids[i] = v.ID
	}
	return ids
}

// A video in two collections is one videos row with two membership rows.
func TestCrossCollectionDedupe(t *testing.T) {
	s := openTestStore(t)
	mustUpsert(t, s, testCollection("first", tVidA, tVidB))
	mustUpsert(t, s, testCollection("second", tVidA, tVidC))

	if n := countRow(t, s, `SELECT count(*) FROM videos WHERE youtube_id = $1`, tVidA); n != 1 {
		t.Errorf("videos rows for %s = %d, want 1", tVidA, n)
	}
	if n := countRow(t, s, `
		SELECT count(*) FROM collection_videos cv
		JOIN videos v ON v.id = cv.video_id WHERE v.youtube_id = $1`, tVidA); n != 2 {
		t.Errorf("memberships for %s = %d, want 2", tVidA, n)
	}
	// The shared video serves in both collections.
	for _, slug := range []string{"first", "second"} {
		ids := listedIDs(t, s, slug)
		if len(ids) != 2 || ids[0] != tVidA {
			t.Errorf("ListVideos(%s) = %v, want %s first of 2", slug, ids, tVidA)
		}
	}
}

// Re-importing without a video removes its membership but keeps the videos
// row (it may belong to other collections and owns snapshot history).
func TestMembershipReplaceOnReimport(t *testing.T) {
	s := openTestStore(t)
	mustUpsert(t, s, testCollection("talks", tVidA, tVidB, tVidC))
	mustUpsert(t, s, testCollection("talks", tVidA, tVidC)) // drops B

	if got, want := listedIDs(t, s, "talks"), []string{tVidA, tVidC}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("after re-import, ListVideos = %v, want %v", got, want)
	}
	if n := countRow(t, s, `
		SELECT count(*) FROM collection_videos cv
		JOIN videos v ON v.id = cv.video_id WHERE v.youtube_id = $1`, tVidB); n != 0 {
		t.Errorf("memberships for removed %s = %d, want 0", tVidB, n)
	}
	if n := countRow(t, s, `SELECT count(*) FROM videos WHERE youtube_id = $1`, tVidB); n != 1 {
		t.Errorf("videos rows for removed %s = %d, want 1 (row must survive)", tVidB, n)
	}
}

// Reordering entries on re-import must succeed in one transaction; the
// UNIQUE (collection_id, position) constraint is deferrable, so positions
// may collide mid-transaction.
func TestReorderOnReimport(t *testing.T) {
	s := openTestStore(t)
	mustUpsert(t, s, testCollection("talks", tVidA, tVidB, tVidC, tVidD))
	mustUpsert(t, s, testCollection("talks", tVidD, tVidC, tVidB, tVidA))

	if got, want := listedIDs(t, s, "talks"), []string{tVidD, tVidC, tVidB, tVidA}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("after reorder, ListVideos = %v, want %v", got, want)
	}
}

// The snapshot tables are append-only: UPDATEs are rejected by trigger.
func TestSnapshotsAppendOnly(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	mustUpsert(t, s, testCollection("talks", tVidA))
	if err := s.RecordSnapshots(ctx, []collections.Snapshot{
		{VideoID: tVidA, ViewCount: 100, CapturedAt: time.Now()},
	}); err != nil {
		t.Fatalf("RecordSnapshots: %v", err)
	}
	if err := s.RecordRankings(ctx, "talks", "views", map[string]int{tVidA: 1}, time.Now()); err != nil {
		t.Fatalf("RecordRankings: %v", err)
	}

	if _, err := s.db.ExecContext(ctx, `UPDATE video_snapshots SET view_count = 999`); err == nil {
		t.Error("UPDATE video_snapshots succeeded, want append-only trigger error")
	} else if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("UPDATE video_snapshots error = %v, want append-only trigger", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE rank_snapshots SET position = 999`); err == nil {
		t.Error("UPDATE rank_snapshots succeeded, want append-only trigger error")
	} else if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("UPDATE rank_snapshots error = %v, want append-only trigger", err)
	}
}

func TestMovers(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	mustUpsert(t, s, testCollection("talks", tVidA, tVidB, tVidC, tVidD))

	now := time.Now()
	record := func(at time.Time, positions map[string]int) {
		t.Helper()
		if err := s.RecordRankings(ctx, "talks", "views", positions, at); err != nil {
			t.Fatalf("RecordRankings(%v): %v", at, err)
		}
	}
	// Baseline two hours ago; vidD is new (latest run only) and must be
	// omitted for lack of a baseline within the window.
	record(now.Add(-2*time.Hour), map[string]int{tVidA: 1, tVidB: 2, tVidC: 3})
	record(now, map[string]int{tVidB: 1, tVidC: 2, tVidD: 3, tVidA: 4})

	movers, err := s.Movers(ctx, "talks", "views", time.Hour, 0)
	if err != nil {
		t.Fatalf("Movers: %v", err)
	}
	if len(movers) != 3 {
		t.Fatalf("Movers returned %d entries %+v, want 3 (vidD has no baseline)", len(movers), movers)
	}
	// vidA: 1 -> 4 (change -3) is the biggest absolute move.
	first := movers[0]
	if first.Video == nil || first.Video.ID != tVidA {
		t.Fatalf("movers[0].Video = %+v, want %s", first.Video, tVidA)
	}
	if first.Position != 4 || first.PreviousPosition != 1 || first.Change != -3 {
		t.Errorf("movers[0] = pos %d prev %d change %d, want 4/1/-3", first.Position, first.PreviousPosition, first.Change)
	}
	for _, m := range movers {
		if m.Video == nil {
			t.Fatalf("mover missing Video: %+v", m)
		}
		if m.Video.ID == tVidD {
			t.Errorf("vidD present, want omitted (no baseline)")
		}
		if m.Video.URL != "https://www.youtube.com/watch?v="+m.Video.ID {
			t.Errorf("mover video URL = %q, want canonical watch URL", m.Video.URL)
		}
	}
	// Ordered by |change| descending.
	for i := 1; i < len(movers); i++ {
		if abs(movers[i].Change) > abs(movers[i-1].Change) {
			t.Errorf("movers not ordered by |change|: %+v", movers)
		}
	}

	limited, err := s.Movers(ctx, "talks", "views", time.Hour, 2)
	if err != nil {
		t.Fatalf("Movers(limit=2): %v", err)
	}
	if len(limited) != 2 {
		t.Errorf("Movers(limit=2) returned %d entries, want 2", len(limited))
	}

	if _, err := s.Movers(ctx, "nope", "views", time.Hour, 0); !errors.Is(err, collections.ErrNotFound) {
		t.Errorf("Movers(unknown slug) error = %v, want ErrNotFound", err)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// PreviousRankings across one and three recordings: empty until two runs
// exist, then always the run before the most recent.
func TestPreviousRankingsRunCounts(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	mustUpsert(t, s, testCollection("talks", tVidA, tVidB))
	base := time.Now().Add(-3 * time.Hour)

	r1 := map[string]int{tVidA: 1, tVidB: 2}
	r2 := map[string]int{tVidB: 1, tVidA: 2}
	r3 := map[string]int{tVidA: 1, tVidB: 2}

	if err := s.RecordRankings(ctx, "talks", "views", r1, base); err != nil {
		t.Fatalf("RecordRankings(1): %v", err)
	}
	got, err := s.PreviousRankings(ctx, "talks", "views")
	if err != nil || len(got) != 0 {
		t.Errorf("after 1 recording: PreviousRankings = %v, %v; want empty, nil", got, err)
	}

	if err := s.RecordRankings(ctx, "talks", "views", r2, base.Add(time.Hour)); err != nil {
		t.Fatalf("RecordRankings(2): %v", err)
	}
	if err := s.RecordRankings(ctx, "talks", "views", r3, base.Add(2*time.Hour)); err != nil {
		t.Fatalf("RecordRankings(3): %v", err)
	}
	got, err = s.PreviousRankings(ctx, "talks", "views")
	if err != nil {
		t.Fatalf("after 3 recordings: PreviousRankings: %v", err)
	}
	if fmt.Sprint(got) != fmt.Sprint(r2) {
		t.Errorf("after 3 recordings: PreviousRankings = %v, want %v", got, r2)
	}
}

// Opening the same database twice must be a no-op the second time, with
// one schema_migrations row per embedded up migration.
func TestMigrationIdempotence(t *testing.T) {
	dbURL := openTestDatabase(t)
	ctx := context.Background()

	s1, err := Open(ctx, dbURL)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := Open(ctx, dbURL)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s2.Close()

	ups, err := fs.Glob(migrations.FS, "*.up.sql")
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if n := countRow(t, s2.(*Store), `SELECT count(*) FROM schema_migrations`); n != len(ups) {
		t.Errorf("schema_migrations rows = %d, want %d", n, len(ups))
	}
}

// Connection errors must never leak credentials: userinfo is stripped from
// the reported URL and the password never appears in the message.
func TestCredentialSanitization(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const badURL = "postgres://alice:sup3rs3cret@127.0.0.1:1/discovery"
	_, err := Open(ctx, badURL)
	if err == nil {
		t.Fatal("Open(unroutable URL) succeeded, want error")
	}
	msg := err.Error()
	if strings.Contains(msg, "sup3rs3cret") {
		t.Errorf("error leaks password: %q", msg)
	}
	if strings.Contains(msg, "alice:") || strings.Contains(msg, "sup3rs3cret@") {
		t.Errorf("error leaks userinfo: %q", msg)
	}
}
