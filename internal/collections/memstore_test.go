package collections_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/denver/discovery/internal/collections"
	"github.com/denver/discovery/internal/collections/storetest"
)

const (
	memVidA = "AAAAAAAAAAA"
	memVidB = "BBBBBBBBBBB"
)

var memBase = time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)

// quietLogger keeps expected cache warnings out of test output.
func quietLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func newTestStore(t *testing.T, cachePath string) *collections.MemStore {
	t.Helper()
	return collections.NewMemStore(collections.MemStoreOptions{
		CachePath: cachePath,
		Logger:    quietLogger(),
	})
}

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

func testProviderVideo(id, title string, views int64) collections.ProviderVideo {
	return collections.ProviderVideo{
		ID:              id,
		Title:           title,
		Description:     "description of " + id,
		ThumbnailURL:    "https://i.ytimg.com/vi/" + id + "/hq720.jpg",
		ChannelID:       "UCchannel",
		ChannelName:     "Channel",
		PublishedAt:     memBase.AddDate(0, -1, 0),
		DurationSeconds: 600,
		Stats: collections.Statistics{
			ViewCount:  views,
			CapturedAt: memBase,
		},
	}
}

func TestMemStoreConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) collections.Store {
		return newTestStore(t, filepath.Join(t.TempDir(), "cache.json"))
	})
}

func TestMemStoreConformanceNoCache(t *testing.T) {
	storetest.Run(t, func(t *testing.T) collections.Store {
		return newTestStore(t, "")
	})
}

// TestMemStoreCacheRoundTrip verifies a restart serves provider data,
// previous rankings, and last-sync times from the cache file without any
// provider refetch.
func TestMemStoreCacheRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cache.json")

	s1 := newTestStore(t, path)
	if err := s1.UpsertCollection(ctx, testCollection("talks", memVidA, memVidB)); err != nil {
		t.Fatalf("UpsertCollection: %v", err)
	}
	if err := s1.UpsertProviderData(ctx, []collections.ProviderVideo{
		testProviderVideo(memVidA, "Title A", 100),
		testProviderVideo(memVidB, "Title B", 200),
	}); err != nil {
		t.Fatalf("UpsertProviderData: %v", err)
	}
	r1 := map[string]int{memVidA: 1, memVidB: 2}
	r2 := map[string]int{memVidB: 1, memVidA: 2}
	if err := s1.RecordRankings(ctx, "talks", "views", r1, memBase); err != nil {
		t.Fatalf("RecordRankings(1): %v", err)
	}
	if err := s1.RecordRankings(ctx, "talks", "views", r2, memBase.Add(time.Hour)); err != nil {
		t.Fatalf("RecordRankings(2): %v", err)
	}
	if err := s1.RecordSnapshots(ctx, []collections.Snapshot{
		{VideoID: memVidA, ViewCount: 100, CapturedAt: memBase},
	}); err != nil {
		t.Fatalf("RecordSnapshots: %v", err)
	}
	syncTime := memBase.Add(2 * time.Hour)
	if err := s1.SetLastSyncedAt(ctx, "talks", syncTime); err != nil {
		t.Fatalf("SetLastSyncedAt: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// New process: editorial content is re-upserted from the collection
	// file (by the sync engine); everything else comes from the cache.
	s2 := newTestStore(t, path)
	defer s2.Close()
	if err := s2.UpsertCollection(ctx, testCollection("talks", memVidA, memVidB)); err != nil {
		t.Fatalf("s2 UpsertCollection: %v", err)
	}

	videos, err := s2.ListVideos(ctx, "talks")
	if err != nil {
		t.Fatalf("s2 ListVideos: %v", err)
	}
	if len(videos) != 2 {
		t.Fatalf("s2 ListVideos returned %d videos, want 2", len(videos))
	}
	if videos[0].Title != "Title A" || videos[0].Statistics == nil || videos[0].Statistics.ViewCount != 100 {
		t.Errorf("cached provider data not served: %+v", videos[0])
	}

	prev, err := s2.PreviousRankings(ctx, "talks", "views")
	if err != nil {
		t.Fatalf("s2 PreviousRankings: %v", err)
	}
	if prev[memVidA] != 1 || prev[memVidB] != 2 {
		t.Errorf("s2 PreviousRankings = %v, want %v (survives restart)", prev, r1)
	}

	info, err := s2.GetCollection(ctx, "talks")
	if err != nil {
		t.Fatalf("s2 GetCollection: %v", err)
	}
	if info.LastSyncedAt == nil || !info.LastSyncedAt.Equal(syncTime) {
		t.Errorf("s2 LastSyncedAt = %v, want %v (survives restart)", info.LastSyncedAt, syncTime)
	}
}

// TestMemStoreCacheWrittenOnMutation verifies the cache survives a crash
// (no Close): every mutating operation persists.
func TestMemStoreCacheWrittenOnMutation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cache.json")

	s1 := newTestStore(t, path)
	if err := s1.UpsertProviderData(ctx, []collections.ProviderVideo{
		testProviderVideo(memVidA, "Title A", 100),
	}); err != nil {
		t.Fatalf("UpsertProviderData: %v", err)
	}
	// No Close: simulate a crash.

	s2 := newTestStore(t, path)
	defer s2.Close()
	if err := s2.UpsertCollection(ctx, testCollection("talks", memVidA)); err != nil {
		t.Fatalf("UpsertCollection: %v", err)
	}
	videos, err := s2.ListVideos(ctx, "talks")
	if err != nil {
		t.Fatalf("ListVideos: %v", err)
	}
	if len(videos) != 1 || videos[0].Title != "Title A" {
		t.Errorf("provider data lost without Close: %+v", videos)
	}
}

func TestMemStoreCorruptCacheFallsBackToEmpty(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cache.json")
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := newTestStore(t, path)
	infos, err := s.ListCollections(ctx)
	if err != nil || len(infos) != 0 {
		t.Errorf("after corrupt cache: ListCollections = %v, %v; want empty, nil", infos, err)
	}

	// The store still works and rewrites a valid cache.
	if err := s.UpsertProviderData(ctx, []collections.ProviderVideo{
		testProviderVideo(memVidA, "Title A", 100),
	}); err != nil {
		t.Fatalf("UpsertProviderData after corrupt cache: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2 := newTestStore(t, path)
	defer s2.Close()
	if err := s2.UpsertCollection(ctx, testCollection("talks", memVidA)); err != nil {
		t.Fatal(err)
	}
	videos, err := s2.ListVideos(ctx, "talks")
	if err != nil || len(videos) != 1 || videos[0].Title != "Title A" {
		t.Errorf("cache not repaired after corruption: %v, %v", videos, err)
	}
}

func TestMemStoreCacheVersionMismatchDiscarded(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cache.json")
	stale := fmt.Sprintf(`{"version": 999, "provider": {"%s": {"ID": "%s", "Title": "Stale"}}}`, memVidA, memVidA)
	if err := os.WriteFile(path, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}

	s := newTestStore(t, path)
	defer s.Close()
	if err := s.UpsertCollection(ctx, testCollection("talks", memVidA)); err != nil {
		t.Fatal(err)
	}
	videos, err := s.ListVideos(ctx, "talks")
	if err != nil {
		t.Fatalf("ListVideos: %v", err)
	}
	if len(videos) != 1 || videos[0].Title != "" || videos[0].Statistics != nil {
		t.Errorf("version-mismatched cache was not discarded: %+v", videos[0])
	}
}

// TestMemStoreUnresolvedURLEntry documents the best-effort behavior for
// entries whose youtubeUrl has not been resolved to an ID yet (resolution
// happens in the sync engine before upserting).
func TestMemStoreUnresolvedURLEntry(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, "")
	defer s.Close()

	rawURL := "https://youtu.be/abc123def45"
	c := &collections.Collection{
		SchemaVersion: "1.0",
		Slug:          "talks",
		Title:         "Talks",
		Videos: []collections.VideoEntry{
			{YouTubeURL: rawURL, TitleOverride: ptrString("Override Title")},
		},
	}
	if err := s.UpsertCollection(ctx, c); err != nil {
		t.Fatal(err)
	}

	videos, err := s.ListVideos(ctx, "talks")
	if err != nil {
		t.Fatalf("ListVideos: %v", err)
	}
	if len(videos) != 1 {
		t.Fatalf("got %d videos, want 1 (unresolved entries still listed)", len(videos))
	}
	v := videos[0]
	if v.ID != "" || v.URL != rawURL || v.Title != "Override Title" || v.Provider != "youtube" {
		t.Errorf("unresolved entry = id %q url %q title %q provider %q; want best-effort data", v.ID, v.URL, v.Title, v.Provider)
	}

	// Not reachable by ID lookup, and empty ID is never a valid key.
	if _, err := s.GetVideo(ctx, ""); !errors.Is(err, collections.ErrNotFound) {
		t.Errorf("GetVideo(\"\") error = %v, want ErrNotFound", err)
	}
}

// TestMemStoreConcurrentAccess exercises readers running against
// concurrent sync-style writers. Run with -race.
func TestMemStoreConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, filepath.Join(t.TempDir(), "cache.json"))
	defer s.Close()

	if err := s.UpsertCollection(ctx, testCollection("talks", memVidA, memVidB)); err != nil {
		t.Fatal(err)
	}

	const (
		writers    = 4
		readers    = 4
		iterations = 50
	)
	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range iterations {
				views := int64(w*iterations + i)
				if err := s.UpsertProviderData(ctx, []collections.ProviderVideo{
					testProviderVideo(memVidA, "Title A", views),
				}); err != nil {
					t.Errorf("UpsertProviderData: %v", err)
				}
				if err := s.RecordRankings(ctx, "talks", "views",
					map[string]int{memVidA: 1, memVidB: 2}, memBase.Add(time.Duration(i)*time.Second)); err != nil {
					t.Errorf("RecordRankings: %v", err)
				}
				if err := s.RecordSnapshots(ctx, []collections.Snapshot{
					{VideoID: memVidA, ViewCount: views, CapturedAt: memBase},
				}); err != nil {
					t.Errorf("RecordSnapshots: %v", err)
				}
				if err := s.SetLastSyncedAt(ctx, "talks", memBase); err != nil {
					t.Errorf("SetLastSyncedAt: %v", err)
				}
			}
		}()
	}
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				if _, err := s.ListVideos(ctx, "talks"); err != nil {
					t.Errorf("ListVideos: %v", err)
				}
				if _, err := s.GetVideo(ctx, memVidA); err != nil {
					t.Errorf("GetVideo: %v", err)
				}
				if _, err := s.PreviousRankings(ctx, "talks", "views"); err != nil {
					t.Errorf("PreviousRankings: %v", err)
				}
				if _, err := s.ListCollections(ctx); err != nil {
					t.Errorf("ListCollections: %v", err)
				}
			}
		}()
	}
	wg.Wait()
}

// TestMemStoreSetLastSyncedAtUnknownCollection documents the MemStore
// choice: recording a sync time for a collection that was never upserted
// is a caller bug and returns ErrNotFound.
func TestMemStoreSetLastSyncedAtUnknownCollection(t *testing.T) {
	s := newTestStore(t, "")
	defer s.Close()
	if err := s.SetLastSyncedAt(context.Background(), "ghost", memBase); !errors.Is(err, collections.ErrNotFound) {
		t.Errorf("SetLastSyncedAt(unknown) error = %v, want ErrNotFound", err)
	}
}

func ptrString(s string) *string { return &s }
