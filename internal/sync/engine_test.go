package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/denver/discovery/internal/collections"
	"github.com/denver/discovery/internal/rankings"
	"github.com/denver/discovery/internal/youtube"
)

// Fixture video IDs (11 characters, like real YouTube IDs).
const (
	idA = "AAAAAAAAAAA"
	idB = "BBBBBBBBBBB"
	idC = "CCCCCCCCCCC"
	idD = "DDDDDDDDDDD"
	idE = "EEEEEEEEEEE"
	idM = "MMMMMMMMMMM" // configured missing at the fake provider
)

var fixedTime = time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)

// fakeFetcher is an in-memory Fetcher. It serves videos from a map,
// reports configured IDs as missing, and can simulate a batch-level error
// or block mid-fetch for concurrency tests.
type fakeFetcher struct {
	videos  map[string]collections.ProviderVideo
	missing map[string]bool
	err     error // batch-level error, returned with whatever resolved

	calls   atomic.Int32
	gotIDs  [][]string
	entered chan struct{} // signaled (non-blocking) when Fetch starts
	release chan struct{} // when non-nil, Fetch blocks until closed
}

func (f *fakeFetcher) Fetch(_ context.Context, ids []string) ([]collections.ProviderVideo, []string, error) {
	f.calls.Add(1)
	f.gotIDs = append(f.gotIDs, slices.Clone(ids))
	if f.entered != nil {
		select {
		case f.entered <- struct{}{}:
		default:
		}
	}
	if f.release != nil {
		<-f.release
	}
	var videos []collections.ProviderVideo
	var failed []string
	for _, id := range ids {
		switch {
		case f.missing[id]:
			failed = append(failed, id)
		default:
			if v, ok := f.videos[id]; ok {
				videos = append(videos, v)
			} else {
				failed = append(failed, id)
			}
		}
	}
	if f.err != nil {
		// Batch-level failure: return what "succeeded" plus the error,
		// like the real client when a later batch fails.
		return videos, nil, f.err
	}
	return videos, failed, nil
}

// spyStore wraps a MemStore, recording snapshot and ranking writes so
// tests can assert on them directly.
type spyStore struct {
	collections.Store
	snapshots []collections.Snapshot
	rankings  map[string]map[string]int // "slug/strategy" -> latest positions
	rankTimes []time.Time
}

func newSpyStore() *spyStore {
	return &spyStore{
		Store:    collections.NewMemStore(collections.MemStoreOptions{Logger: discardLogger()}),
		rankings: map[string]map[string]int{},
	}
}

func (s *spyStore) RecordSnapshots(ctx context.Context, snaps []collections.Snapshot) error {
	s.snapshots = append(s.snapshots, snaps...)
	return s.Store.RecordSnapshots(ctx, snaps)
}

func (s *spyStore) RecordRankings(ctx context.Context, slug, strategy string, positions map[string]int, at time.Time) error {
	s.rankings[slug+"/"+strategy] = maps.Clone(positions)
	s.rankTimes = append(s.rankTimes, at)
	return s.Store.RecordRankings(ctx, slug, strategy, positions, at)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func pv(id string, views, likes, comments int64) collections.ProviderVideo {
	return collections.ProviderVideo{
		ID:              id,
		Title:           "Title " + id,
		Description:     "Description " + id,
		ThumbnailURL:    "https://i.ytimg.com/vi/" + id + "/hq.jpg",
		ChannelID:       "chan-" + id,
		ChannelName:     "Channel " + id,
		PublishedAt:     fixedTime.AddDate(0, -1, 0),
		DurationSeconds: 100,
		Stats: collections.Statistics{
			ViewCount:    views,
			LikeCount:    likes,
			CommentCount: comments,
			CapturedAt:   fixedTime,
		},
	}
}

// writeCollection marshals a collection to a JSON file under dir and
// returns its path.
func writeCollection(t *testing.T, dir, name string, c *collections.Collection) string {
	t.Helper()
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal collection: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write collection: %v", err)
	}
	return path
}

func baseCollection(slug string, videos ...collections.VideoEntry) *collections.Collection {
	return &collections.Collection{
		SchemaVersion: "1.0",
		Slug:          slug,
		Title:         "Test " + slug,
		Videos:        videos,
	}
}

func newEngine(store collections.Store, fetcher Fetcher, paths ...string) *Engine {
	return New(store, fetcher, rankings.DefaultRegistry(), Options{
		CollectionPaths: paths,
		Now:             func() time.Time { return fixedTime },
		Logger:          discardLogger(),
	})
}

func TestRunHappyPath(t *testing.T) {
	unpublished := false
	path := writeCollection(t, t.TempDir(), "happy.json", baseCollection("happy",
		collections.VideoEntry{YouTubeID: idA},
		collections.VideoEntry{YouTubeURL: "https://www.youtube.com/watch?v=" + idB},
		// Duplicate of idA in a different URL form; unpublished so ranking
		// assertions stay simple, but still resolved and deduped for fetch.
		collections.VideoEntry{YouTubeURL: "https://youtu.be/" + idA, Published: &unpublished},
		collections.VideoEntry{YouTubeURL: "https://www.youtube.com/shorts/" + idC},
	))
	fetcher := &fakeFetcher{videos: map[string]collections.ProviderVideo{
		idA: pv(idA, 300, 10, 1),
		idB: pv(idB, 200, 30, 2),
		idC: pv(idC, 100, 20, 9),
	}}
	store := newSpyStore()
	engine := newEngine(store, fetcher, path)

	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Fetched != 3 {
		t.Errorf("Fetched = %d, want 3", result.Fetched)
	}
	if result.Failed == nil || len(result.Failed) != 0 {
		t.Errorf("Failed = %#v, want non-nil empty slice", result.Failed)
	}
	if !result.StartedAt.Equal(fixedTime) {
		t.Errorf("StartedAt = %v, want %v", result.StartedAt, fixedTime)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if !strings.Contains(string(encoded), `"failed":[]`) {
		t.Errorf("result JSON = %s, want it to contain %q", encoded, `"failed":[]`)
	}

	// Each unique ID fetched exactly once, in entry order.
	if got := fetcher.calls.Load(); got != 1 {
		t.Errorf("fetch calls = %d, want 1", got)
	}
	if want := []string{idA, idB, idC}; !slices.Equal(fetcher.gotIDs[0], want) {
		t.Errorf("fetched IDs = %v, want %v", fetcher.gotIDs[0], want)
	}

	// One snapshot per fetched video, stamped with the injected clock.
	if len(store.snapshots) != 3 {
		t.Fatalf("snapshots recorded = %d, want 3", len(store.snapshots))
	}
	for _, snap := range store.snapshots {
		if !snap.CapturedAt.Equal(fixedTime) {
			t.Errorf("snapshot %s CapturedAt = %v, want %v", snap.VideoID, snap.CapturedAt, fixedTime)
		}
	}

	// Rankings recorded for exactly the non-windowed strategies.
	wantRankings := map[string]map[string]int{
		"happy/views":      {idA: 1, idB: 2, idC: 3},
		"happy/likes":      {idB: 1, idC: 2, idA: 3},
		"happy/comments":   {idC: 1, idB: 2, idA: 3},
		"happy/engagement": {idC: 1, idB: 2, idA: 3}, // 20+27, 30+6, 10+3
	}
	if len(store.rankings) != len(wantRankings) {
		t.Errorf("ranking keys = %v, want %v", slices.Sorted(maps.Keys(store.rankings)), slices.Sorted(maps.Keys(wantRankings)))
	}
	for key, want := range wantRankings {
		if got := store.rankings[key]; !maps.Equal(got, want) {
			t.Errorf("rankings[%s] = %v, want %v", key, got, want)
		}
	}
	for _, at := range store.rankTimes {
		if !at.Equal(fixedTime) {
			t.Errorf("ranking time = %v, want %v", at, fixedTime)
		}
	}

	info, err := store.GetCollection(context.Background(), "happy")
	if err != nil {
		t.Fatalf("GetCollection: %v", err)
	}
	if info.LastSyncedAt == nil || !info.LastSyncedAt.Equal(fixedTime) {
		t.Errorf("LastSyncedAt = %v, want %v", info.LastSyncedAt, fixedTime)
	}

	// Resolved IDs were set before upserting, so the store keys correctly.
	video, err := store.GetVideo(context.Background(), idB)
	if err != nil {
		t.Fatalf("GetVideo(%s): %v", idB, err)
	}
	if video.Statistics == nil || video.Statistics.ViewCount != 200 {
		t.Errorf("GetVideo(%s).Statistics = %+v, want viewCount 200", idB, video.Statistics)
	}
}

func TestSecondRunRecordsPreviousRankings(t *testing.T) {
	path := writeCollection(t, t.TempDir(), "c.json", baseCollection("prev",
		collections.VideoEntry{YouTubeID: idA},
		collections.VideoEntry{YouTubeID: idB},
	))
	fetcher := &fakeFetcher{videos: map[string]collections.ProviderVideo{
		idA: pv(idA, 300, 1, 1),
		idB: pv(idB, 200, 1, 1),
	}}
	store := newSpyStore()

	now := fixedTime
	engine := New(store, fetcher, rankings.DefaultRegistry(), Options{
		CollectionPaths: []string{path},
		Now:             func() time.Time { return now },
		Logger:          discardLogger(),
	})

	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Stats change between runs: B overtakes A.
	fetcher.videos[idB] = pv(idB, 500, 1, 1)
	now = now.Add(time.Hour)

	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	prev, err := store.PreviousRankings(context.Background(), "prev", "views")
	if err != nil {
		t.Fatalf("PreviousRankings: %v", err)
	}
	if want := map[string]int{idA: 1, idB: 2}; !maps.Equal(prev, want) {
		t.Errorf("PreviousRankings = %v, want first run's positions %v", prev, want)
	}
	if want := map[string]int{idB: 1, idA: 2}; !maps.Equal(store.rankings["prev/views"], want) {
		t.Errorf("latest views rankings = %v, want %v", store.rankings["prev/views"], want)
	}
	if got := fetcher.calls.Load(); got != 2 {
		t.Errorf("fetch calls = %d, want 2 (one per run)", got)
	}
}

func TestPerEntryFailures(t *testing.T) {
	noVideoURL := "https://www.youtube.com/watch" // valid YouTube host, no video ID
	path := writeCollection(t, t.TempDir(), "c.json", baseCollection("failures",
		collections.VideoEntry{YouTubeID: idA}, // the one good entry
		collections.VideoEntry{YouTubeURL: noVideoURL},
		collections.VideoEntry{YouTubeURL: "https://www.youtube.com/watch?v=" + idD, YouTubeID: idE}, // disagreement
		collections.VideoEntry{YouTubeID: idM},                                                       // provider reports it missing
	))
	fetcher := &fakeFetcher{
		videos:  map[string]collections.ProviderVideo{idA: pv(idA, 100, 1, 1)},
		missing: map[string]bool{idM: true},
	}
	store := newSpyStore()
	engine := newEngine(store, fetcher, path)

	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v (per-entry failures must not be fatal)", err)
	}

	if result.Fetched != 1 {
		t.Errorf("Fetched = %d, want 1", result.Fetched)
	}
	wantFailed := []string{noVideoURL, idE, idM}
	if !slices.Equal(result.Failed, wantFailed) {
		t.Errorf("Failed = %v, want %v", result.Failed, wantFailed)
	}

	// Failed entries are never fetched; the disagreeing entry's IDs stay out.
	if want := []string{idA, idM}; !slices.Equal(fetcher.gotIDs[0], want) {
		t.Errorf("fetched IDs = %v, want %v", fetcher.gotIDs[0], want)
	}

	// The run still completed: rankings and lastSyncedAt recorded.
	if len(store.rankings) != 4 {
		t.Errorf("ranking writes = %d, want 4", len(store.rankings))
	}
	info, err := store.GetCollection(context.Background(), "failures")
	if err != nil {
		t.Fatalf("GetCollection: %v", err)
	}
	if info.LastSyncedAt == nil {
		t.Error("LastSyncedAt not set after run with per-entry failures")
	}
}

func TestInvalidCollectionAborts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.json")
	// Missing title, bad slug: two validation problems, zero store writes.
	raw := `{"schemaVersion":"1.0","slug":"Bad Slug","videos":[{"youtubeId":"` + idA + `"}]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	store := newSpyStore()
	fetcher := &fakeFetcher{}
	engine := newEngine(store, fetcher, path)

	result, err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("Run succeeded, want validation error")
	}
	if result != nil {
		t.Errorf("result = %+v, want nil on abort", result)
	}
	var verrs collections.ValidationErrors
	if !errors.As(err, &verrs) {
		t.Fatalf("error %v does not unwrap to ValidationErrors", err)
	}
	if len(verrs) == 0 {
		t.Error("ValidationErrors is empty")
	}
	if got := fetcher.calls.Load(); got != 0 {
		t.Errorf("fetch calls = %d, want 0 after abort", got)
	}
	infos, err := store.ListCollections(context.Background())
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("collections upserted = %d, want 0 after abort", len(infos))
	}
}

func TestUnreadableCollectionAborts(t *testing.T) {
	engine := newEngine(newSpyStore(), &fakeFetcher{}, filepath.Join(t.TempDir(), "missing.json"))
	if _, err := engine.Run(context.Background()); err == nil {
		t.Fatal("Run succeeded, want error for missing file")
	}
}

func TestNoCollectionPaths(t *testing.T) {
	engine := New(newSpyStore(), &fakeFetcher{}, rankings.DefaultRegistry(), Options{Logger: discardLogger()})
	if _, err := engine.Run(context.Background()); err == nil {
		t.Fatal("Run succeeded, want error when no collection paths are configured")
	}
}

func TestConcurrentRunReturnsErrSyncInProgress(t *testing.T) {
	path := writeCollection(t, t.TempDir(), "c.json", baseCollection("conc",
		collections.VideoEntry{YouTubeID: idA},
	))
	fetcher := &fakeFetcher{
		videos:  map[string]collections.ProviderVideo{idA: pv(idA, 1, 1, 1)},
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	engine := newEngine(newSpyStore(), fetcher, path)

	firstDone := make(chan error, 1)
	go func() {
		_, err := engine.Run(context.Background())
		firstDone <- err
	}()

	<-fetcher.entered // first Run is now blocked inside Fetch

	if _, err := engine.Run(context.Background()); !errors.Is(err, ErrSyncInProgress) {
		t.Errorf("second Run error = %v, want ErrSyncInProgress", err)
	}

	close(fetcher.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// The guard releases: a later Run succeeds.
	if _, err := engine.Run(context.Background()); err != nil {
		t.Errorf("Run after completion: %v", err)
	}
}

func TestFetcherBatchErrorReturnsPartialResult(t *testing.T) {
	path := writeCollection(t, t.TempDir(), "c.json", baseCollection("partial",
		collections.VideoEntry{YouTubeID: idA},
		collections.VideoEntry{YouTubeID: idB},
	))
	batchErr := errors.New("quota exceeded")
	fetcher := &fakeFetcher{
		videos: map[string]collections.ProviderVideo{idA: pv(idA, 100, 1, 1)},
		err:    batchErr,
	}
	store := newSpyStore()
	engine := newEngine(store, fetcher, path)

	result, err := engine.Run(context.Background())
	if !errors.Is(err, batchErr) {
		t.Fatalf("Run error = %v, want wrapped batch error", err)
	}
	if result == nil {
		t.Fatal("result = nil, want partial result alongside the error")
	}
	if result.Fetched != 1 {
		t.Errorf("Fetched = %d, want 1", result.Fetched)
	}

	// What succeeded is persisted; rankings and lastSyncedAt are not.
	video, err := store.GetVideo(context.Background(), idA)
	if err != nil {
		t.Fatalf("GetVideo(%s): %v", idA, err)
	}
	if video.Statistics == nil || video.Statistics.ViewCount != 100 {
		t.Errorf("GetVideo(%s).Statistics = %+v, want viewCount 100", idA, video.Statistics)
	}
	if len(store.snapshots) != 1 {
		t.Errorf("snapshots recorded = %d, want 1", len(store.snapshots))
	}
	if len(store.rankings) != 0 {
		t.Errorf("rankings recorded = %v, want none after batch error", store.rankings)
	}
	info, err := store.GetCollection(context.Background(), "partial")
	if err != nil {
		t.Fatalf("GetCollection: %v", err)
	}
	if info.LastSyncedAt != nil {
		t.Errorf("LastSyncedAt = %v, want nil after batch error", info.LastSyncedAt)
	}
}

func TestVideoSharedAcrossCollectionsFetchedOnce(t *testing.T) {
	dir := t.TempDir()
	path1 := writeCollection(t, dir, "one.json", baseCollection("one",
		collections.VideoEntry{YouTubeID: idA},
		collections.VideoEntry{YouTubeID: idB},
	))
	path2 := writeCollection(t, dir, "two.json", baseCollection("two",
		collections.VideoEntry{YouTubeURL: "https://www.youtube.com/watch?v=" + idA}, // shared with "one"
		collections.VideoEntry{YouTubeID: idC},
	))
	fetcher := &fakeFetcher{videos: map[string]collections.ProviderVideo{
		idA: pv(idA, 300, 1, 1),
		idB: pv(idB, 200, 1, 1),
		idC: pv(idC, 100, 1, 1),
	}}
	store := newSpyStore()
	engine := newEngine(store, fetcher, path1, path2)

	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fetched != 3 {
		t.Errorf("Fetched = %d, want 3", result.Fetched)
	}
	if got := fetcher.calls.Load(); got != 1 {
		t.Errorf("fetch calls = %d, want 1", got)
	}
	if want := []string{idA, idB, idC}; !slices.Equal(fetcher.gotIDs[0], want) {
		t.Errorf("fetched IDs = %v, want %v (shared video deduped)", fetcher.gotIDs[0], want)
	}
	if len(store.rankings) != 8 {
		t.Errorf("ranking writes = %d, want 8 (4 strategies x 2 collections)", len(store.rankings))
	}
	for _, slug := range []string{"one", "two"} {
		info, err := store.GetCollection(context.Background(), slug)
		if err != nil {
			t.Fatalf("GetCollection(%s): %v", slug, err)
		}
		if info.LastSyncedAt == nil {
			t.Errorf("LastSyncedAt not set for %s", slug)
		}
	}
}

// TestRealYouTubeClient wires the real *youtube.Client against an httptest
// fake of the Data API, proving the Fetcher interface matches Lane A's
// public API end to end.
func TestRealYouTubeClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var items []map[string]any
		for _, id := range strings.Split(r.URL.Query().Get("id"), ",") {
			items = append(items, map[string]any{
				"id": id,
				"snippet": map[string]any{
					"title":        "Title " + id,
					"description":  "Description " + id,
					"channelId":    "chan-" + id,
					"channelTitle": "Channel " + id,
					"publishedAt":  "2026-06-01T00:00:00Z",
					"thumbnails":   map[string]any{"high": map[string]any{"url": "https://i.ytimg.com/vi/" + id + "/hq.jpg"}},
				},
				"contentDetails": map[string]any{"duration": "PT1M40S"},
				"statistics": map[string]any{
					"viewCount":    fmt.Sprintf("%d", 100*(len(items)+1)),
					"likeCount":    "10",
					"commentCount": "2",
				},
			})
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"items": items}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	path := writeCollection(t, t.TempDir(), "c.json", baseCollection("real",
		collections.VideoEntry{YouTubeID: idA},
		collections.VideoEntry{YouTubeURL: "https://youtu.be/" + idB},
	))
	client := youtube.NewClient("test-key", youtube.WithBaseURL(server.URL))
	store := collections.NewMemStore(collections.MemStoreOptions{Logger: discardLogger()})
	engine := newEngine(store, client, path)

	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fetched != 2 {
		t.Errorf("Fetched = %d, want 2", result.Fetched)
	}
	if len(result.Failed) != 0 {
		t.Errorf("Failed = %v, want empty", result.Failed)
	}
	video, err := store.GetVideo(context.Background(), idB)
	if err != nil {
		t.Fatalf("GetVideo(%s): %v", idB, err)
	}
	if video.Title != "Title "+idB {
		t.Errorf("Title = %q, want %q", video.Title, "Title "+idB)
	}
	if video.DurationSeconds != 100 {
		t.Errorf("DurationSeconds = %d, want 100", video.DurationSeconds)
	}
	if video.Statistics == nil || video.Statistics.LikeCount != 10 {
		t.Errorf("Statistics = %+v, want likeCount 10", video.Statistics)
	}
}
