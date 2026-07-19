package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	stdsync "sync"
	"testing"
	"time"

	"github.com/denver/discovery/internal/api"
	"github.com/denver/discovery/internal/collections"
	"github.com/denver/discovery/internal/rankings"
	"github.com/denver/discovery/internal/service"
	syncpkg "github.com/denver/discovery/internal/sync"
	"github.com/denver/discovery/internal/youtube"
)

// Video IDs used across tests; all match ^[A-Za-z0-9_-]{11}$.
const (
	id1 = "vid00000001" // topic agents, track Engineering, speaker alice
	id2 = "vid00000002" // topic evals,  track Research,    speaker bob
	id3 = "vid00000003" // topic agents, track Engineering, speaker carol
)

const collectionJSON = `{
  "schemaVersion": "1.0",
  "slug": "ai-conf-2026",
  "title": "AI Conf 2026",
  "description": "Curated talks",
  "defaultRanking": "views",
  "videos": [
    {"youtubeId": "vid00000001", "speakers": [{"name": "Alice", "slug": "alice"}], "topics": ["agents"], "track": "Engineering"},
    {"youtubeId": "vid00000002", "speakers": [{"name": "Bob", "slug": "bob"}], "topics": ["evals"], "track": "Research"},
    {"youtubeId": "vid00000003", "speakers": [{"name": "Carol", "slug": "carol"}], "topics": ["agents"], "track": "Engineering"}
  ]
}`

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeFetcher is an in-memory sync.Fetcher.
type fakeFetcher struct {
	mu     stdsync.Mutex
	videos map[string]collections.ProviderVideo

	block     chan struct{} // when non-nil, Fetch blocks until closed
	started   chan struct{} // when non-nil, closed once Fetch begins
	startOnce stdsync.Once
}

func (f *fakeFetcher) Fetch(ctx context.Context, ids []string) ([]collections.ProviderVideo, []string, error) {
	if f.started != nil {
		f.startOnce.Do(func() { close(f.started) })
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []collections.ProviderVideo
	var failed []string
	for _, id := range ids {
		if v, ok := f.videos[id]; ok {
			out = append(out, v)
		} else {
			failed = append(failed, id)
		}
	}
	return out, failed, nil
}

func (f *fakeFetcher) setViews(id string, views int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.videos[id]
	v.Stats.ViewCount = views
	f.videos[id] = v
}

func baseVideos() map[string]collections.ProviderVideo {
	mk := func(id, title string, views, likes, comments int64) collections.ProviderVideo {
		return collections.ProviderVideo{
			ID:              id,
			Title:           title,
			Description:     "about " + title,
			ThumbnailURL:    "https://i.ytimg.com/vi/" + id + "/hq.jpg",
			ChannelID:       "chan0000001",
			ChannelName:     "AI Conf",
			PublishedAt:     time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			DurationSeconds: 1800,
			Stats: collections.Statistics{
				ViewCount:    views,
				LikeCount:    likes,
				CommentCount: comments,
				CapturedAt:   time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			},
		}
	}
	return map[string]collections.ProviderVideo{
		id1: mk(id1, "Talk One", 100, 10, 1),
		id2: mk(id2, "Talk Two", 300, 5, 9),
		id3: mk(id3, "Talk Three", 200, 20, 2),
	}
}

// env is a fully wired API over a seeded MemStore and fake-fetcher engine.
type env struct {
	t       *testing.T
	handler http.Handler
	store   *collections.MemStore
	svc     *service.Service
	engine  *syncpkg.Engine
	fetcher *fakeFetcher
}

func newEnv(t *testing.T, opts ...api.Option) *env {
	t.Helper()
	return newEnvWith(t, collectionJSON, &fakeFetcher{videos: baseVideos()}, opts...)
}

func newEnvWith(t *testing.T, fileJSON string, fetcher *fakeFetcher, opts ...api.Option) *env {
	t.Helper()
	path := filepath.Join(t.TempDir(), "collection.json")
	if err := os.WriteFile(path, []byte(fileJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	store := collections.NewMemStore(collections.MemStoreOptions{Logger: discardLogger()})
	registry := rankings.DefaultRegistry()
	engine := syncpkg.New(store, fetcher, registry, syncpkg.Options{
		CollectionPaths: []string{path},
		Logger:          discardLogger(),
	})
	svc := &service.Service{Store: store, Registry: registry}
	allOpts := append([]api.Option{api.WithLogger(discardLogger())}, opts...)
	return &env{
		t:       t,
		handler: api.New(svc, engine, allOpts...),
		store:   store,
		svc:     svc,
		engine:  engine,
		fetcher: fetcher,
	}
}

// seed runs one direct engine sync (not via HTTP, so no cooldown starts).
func (e *env) seed() {
	e.t.Helper()
	if _, err := e.engine.Run(context.Background()); err != nil {
		e.t.Fatalf("seed sync: %v", err)
	}
}

func (e *env) do(method, target string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

func (e *env) get(target string) *httptest.ResponseRecorder {
	return e.do(http.MethodGet, target)
}

// getJSON asserts status and decodes the body into a generic map.
func (e *env) getJSON(target string, wantStatus int) map[string]any {
	e.t.Helper()
	rec := e.get(target)
	return decodeBody(e.t, rec, wantStatus)
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) map[string]any {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, wantStatus, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body: %v; body: %s", err, rec.Body.String())
	}
	return m
}

// videoIDs extracts the id of each element in a video array field.
func videoIDs(t *testing.T, body map[string]any, field string) []string {
	t.Helper()
	raw, ok := body[field].([]any)
	if !ok {
		t.Fatalf("body[%q] is %T, want array; body: %v", field, body[field], body)
	}
	ids := make([]string, len(raw))
	for i, v := range raw {
		ids[i] = v.(map[string]any)["id"].(string)
	}
	return ids
}

func wantErrorBody(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) string {
	t.Helper()
	body := decodeBody(t, rec, wantStatus)
	msg, ok := body["error"].(string)
	if !ok || msg == "" {
		t.Fatalf("error body missing non-empty \"error\": %v", body)
	}
	if len(body) != 1 {
		t.Fatalf("error body must contain only \"error\": %v", body)
	}
	return msg
}

func TestHealth(t *testing.T) {
	e := newEnv(t)
	body := e.getJSON("/health", http.StatusOK)
	if body["status"] != "ok" {
		t.Fatalf("status = %v, want ok", body["status"])
	}
}

func TestListCollections(t *testing.T) {
	e := newEnv(t)
	e.seed()
	body := e.getJSON("/api/v1/collections", http.StatusOK)
	list, ok := body["collections"].([]any)
	if !ok {
		t.Fatalf("response not wrapped in collections array: %v", body)
	}
	if len(list) != 1 {
		t.Fatalf("len(collections) = %d, want 1", len(list))
	}
	c := list[0].(map[string]any)
	if c["slug"] != "ai-conf-2026" || c["title"] != "AI Conf 2026" {
		t.Fatalf("unexpected collection: %v", c)
	}
	if c["videoCount"] != float64(3) {
		t.Fatalf("videoCount = %v, want 3", c["videoCount"])
	}
	if _, ok := c["lastSyncedAt"]; !ok {
		t.Fatalf("lastSyncedAt missing after sync: %v", c)
	}
	// The contract's Collection schema has neither the raw source video
	// entries nor schemaVersion; they must not leak into responses.
	for _, leak := range []string{"videos", "schemaVersion", "refreshInterval"} {
		if _, present := c[leak]; present {
			t.Fatalf("collection response leaks %q: %v", leak, c)
		}
	}
}

func TestListCollectionsEmpty(t *testing.T) {
	e := newEnv(t) // no seed: nothing upserted yet
	body := e.getJSON("/api/v1/collections", http.StatusOK)
	if list, ok := body["collections"].([]any); !ok || len(list) != 0 {
		t.Fatalf("want empty collections array, got %v", body)
	}
}

func TestGetCollection(t *testing.T) {
	e := newEnv(t)
	e.seed()

	body := e.getJSON("/api/v1/collections/ai-conf-2026", http.StatusOK)
	if body["slug"] != "ai-conf-2026" {
		t.Fatalf("slug = %v", body["slug"])
	}
	if body["defaultRanking"] != "views" {
		t.Fatalf("defaultRanking = %v", body["defaultRanking"])
	}

	msg := wantErrorBody(t, e.get("/api/v1/collections/nope"), http.StatusNotFound)
	if !strings.Contains(msg, "nope") {
		t.Fatalf("404 error should name the slug: %q", msg)
	}
}

func TestListVideos(t *testing.T) {
	e := newEnv(t)
	e.seed()

	t.Run("editorial order, no ranking", func(t *testing.T) {
		body := e.getJSON("/api/v1/collections/ai-conf-2026/videos", http.StatusOK)
		ids := videoIDs(t, body, "videos")
		want := []string{id1, id2, id3}
		if fmt.Sprint(ids) != fmt.Sprint(want) {
			t.Fatalf("order = %v, want %v", ids, want)
		}
		if body["total"] != float64(3) || body["limit"] != float64(25) || body["offset"] != float64(0) {
			t.Fatalf("pagination meta wrong: total=%v limit=%v offset=%v", body["total"], body["limit"], body["offset"])
		}
		first := body["videos"].([]any)[0].(map[string]any)
		if _, present := first["ranking"]; present {
			t.Fatalf("videos endpoint must not include ranking: %v", first)
		}
		if first["provider"] != "youtube" || first["title"] != "Talk One" {
			t.Fatalf("unexpected video: %v", first)
		}
		stats := first["statistics"].(map[string]any)
		if stats["viewCount"] != float64(100) {
			t.Fatalf("viewCount = %v", stats["viewCount"])
		}
	})

	t.Run("filters", func(t *testing.T) {
		cases := []struct {
			query string
			want  []string
		}{
			{"topic=agents", []string{id1, id3}},
			{"topic=AGENTS", []string{id1, id3}}, // case-insensitive
			{"track=research", []string{id2}},
			{"speaker=carol", []string{id3}},
			{"topic=agents&speaker=alice", []string{id1}},
			{"topic=nosuch", nil},
		}
		for _, tc := range cases {
			body := e.getJSON("/api/v1/collections/ai-conf-2026/videos?"+tc.query, http.StatusOK)
			ids := videoIDs(t, body, "videos")
			if fmt.Sprint(ids) != fmt.Sprint(append([]string{}, tc.want...)) {
				t.Errorf("%s: got %v, want %v", tc.query, ids, tc.want)
			}
			if body["total"] != float64(len(tc.want)) {
				t.Errorf("%s: total = %v, want %d", tc.query, body["total"], len(tc.want))
			}
		}
	})

	t.Run("pagination", func(t *testing.T) {
		body := e.getJSON("/api/v1/collections/ai-conf-2026/videos?limit=1&offset=1", http.StatusOK)
		ids := videoIDs(t, body, "videos")
		if len(ids) != 1 || ids[0] != id2 {
			t.Fatalf("page = %v, want [%s]", ids, id2)
		}
		if body["total"] != float64(3) || body["limit"] != float64(1) || body["offset"] != float64(1) {
			t.Fatalf("meta: total=%v limit=%v offset=%v", body["total"], body["limit"], body["offset"])
		}
	})

	t.Run("bad params", func(t *testing.T) {
		for _, q := range []string{"limit=0", "limit=101", "limit=abc", "limit=1.5", "offset=-1", "offset=abc"} {
			wantErrorBody(t, e.get("/api/v1/collections/ai-conf-2026/videos?"+q), http.StatusBadRequest)
		}
	})

	t.Run("unknown slug", func(t *testing.T) {
		wantErrorBody(t, e.get("/api/v1/collections/nope/videos"), http.StatusNotFound)
	})
}

func TestRankings(t *testing.T) {
	e := newEnv(t)
	e.seed()

	t.Run("default strategy views", func(t *testing.T) {
		body := e.getJSON("/api/v1/collections/ai-conf-2026/rankings", http.StatusOK)
		if body["strategy"] != "views" {
			t.Fatalf("top-level strategy = %v, want views", body["strategy"])
		}
		ids := videoIDs(t, body, "videos")
		want := []string{id2, id3, id1} // views 300, 200, 100
		if fmt.Sprint(ids) != fmt.Sprint(want) {
			t.Fatalf("order = %v, want %v", ids, want)
		}
		first := body["videos"].([]any)[0].(map[string]any)
		rank, ok := first["ranking"].(map[string]any)
		if !ok {
			t.Fatalf("ranking object missing: %v", first)
		}
		if rank["position"] != float64(1) || rank["score"] != float64(300) || rank["strategy"] != "views" {
			t.Fatalf("ranking = %v", rank)
		}
		// First recorded sync: no prior run, so no movement fields.
		if _, present := rank["previousPosition"]; present {
			t.Fatalf("previousPosition must be absent on first sync: %v", rank)
		}
	})

	t.Run("previousPosition and change after second sync", func(t *testing.T) {
		e.fetcher.setViews(id1, 500) // id1 climbs from position 3 to 1
		e.seed()
		body := e.getJSON("/api/v1/collections/ai-conf-2026/rankings?sort=views", http.StatusOK)
		ids := videoIDs(t, body, "videos")
		want := []string{id1, id2, id3}
		if fmt.Sprint(ids) != fmt.Sprint(want) {
			t.Fatalf("order = %v, want %v", ids, want)
		}
		rank := body["videos"].([]any)[0].(map[string]any)["ranking"].(map[string]any)
		if rank["previousPosition"] != float64(3) || rank["change"] != float64(2) {
			t.Fatalf("movement wrong: %v", rank)
		}
	})

	t.Run("sort by likes", func(t *testing.T) {
		body := e.getJSON("/api/v1/collections/ai-conf-2026/rankings?sort=likes", http.StatusOK)
		if body["strategy"] != "likes" {
			t.Fatalf("strategy = %v", body["strategy"])
		}
		ids := videoIDs(t, body, "videos")
		want := []string{id3, id1, id2} // likes 20, 10, 5
		if fmt.Sprint(ids) != fmt.Sprint(want) {
			t.Fatalf("order = %v, want %v", ids, want)
		}
	})

	t.Run("filters compose with sort", func(t *testing.T) {
		body := e.getJSON("/api/v1/collections/ai-conf-2026/rankings?sort=views&topic=agents", http.StatusOK)
		ids := videoIDs(t, body, "videos")
		want := []string{id1, id3} // after second sync: 500 vs 200 views
		if fmt.Sprint(ids) != fmt.Sprint(want) {
			t.Fatalf("order = %v, want %v", ids, want)
		}
		if body["total"] != float64(2) {
			t.Fatalf("total = %v, want 2", body["total"])
		}
		rank := body["videos"].([]any)[0].(map[string]any)["ranking"].(map[string]any)
		if rank["position"] != float64(1) {
			t.Fatalf("filtered ranking positions must restart at 1: %v", rank)
		}
	})

	t.Run("pagination meta", func(t *testing.T) {
		body := e.getJSON("/api/v1/collections/ai-conf-2026/rankings?limit=2&offset=2", http.StatusOK)
		ids := videoIDs(t, body, "videos")
		if len(ids) != 1 {
			t.Fatalf("page size = %d, want 1", len(ids))
		}
		if body["total"] != float64(3) || body["limit"] != float64(2) || body["offset"] != float64(2) {
			t.Fatalf("meta: %v %v %v", body["total"], body["limit"], body["offset"])
		}
	})

	t.Run("errors", func(t *testing.T) {
		wantErrorBody(t, e.get("/api/v1/collections/ai-conf-2026/rankings?sort=bogus"), http.StatusBadRequest)
		wantErrorBody(t, e.get("/api/v1/collections/ai-conf-2026/rankings?limit=999"), http.StatusBadRequest)
		wantErrorBody(t, e.get("/api/v1/collections/nope/rankings"), http.StatusNotFound)
	})

	t.Run("windowed strategies are 501 in file mode", func(t *testing.T) {
		for _, sort := range []string{"views_24h", "views_7d", "growth_percent_24h", "rank_change_24h"} {
			wantErrorBody(t, e.get("/api/v1/collections/ai-conf-2026/rankings?sort="+sort), http.StatusNotImplemented)
		}
	})
}

func TestGetVideo(t *testing.T) {
	e := newEnv(t)
	e.seed()

	body := e.getJSON("/api/v1/videos/"+id1, http.StatusOK)
	if body["id"] != id1 || body["title"] != "Talk One" {
		t.Fatalf("video = %v", body)
	}
	if body["url"] != "https://www.youtube.com/watch?v="+id1 {
		t.Fatalf("url = %v", body["url"])
	}

	wantErrorBody(t, e.get("/api/v1/videos/AAAAAAAAAAA"), http.StatusNotFound)

	for _, bad := range []string{"short", "twelve-chars", "bad%20chars0"} {
		wantErrorBody(t, e.get("/api/v1/videos/"+bad), http.StatusBadRequest)
	}
}

// histStore upgrades a MemStore with History and Movers, standing in for
// the postgres store so db-mode response shapes are testable here.
type histStore struct {
	collections.Store
	snaps      []collections.Snapshot
	movers     []collections.Mover
	gotSince   time.Time
	gotWindow  time.Duration
	gotLimit   int
	historyErr error
}

func (h *histStore) History(_ context.Context, _ string, since time.Time) ([]collections.Snapshot, error) {
	h.gotSince = since
	if h.historyErr != nil {
		return nil, h.historyErr
	}
	return h.snaps, nil
}

func (h *histStore) Movers(_ context.Context, _, _ string, window time.Duration, limit int) ([]collections.Mover, error) {
	h.gotWindow = window
	h.gotLimit = limit
	return h.movers, nil
}

func TestVideoHistory(t *testing.T) {
	t.Run("file mode is 501", func(t *testing.T) {
		e := newEnv(t)
		e.seed()
		wantErrorBody(t, e.get("/api/v1/videos/"+id1+"/history"), http.StatusNotImplemented)
	})

	t.Run("unknown video is 404, bad id and since are 400", func(t *testing.T) {
		e := newEnv(t)
		e.seed()
		wantErrorBody(t, e.get("/api/v1/videos/AAAAAAAAAAA/history"), http.StatusNotFound)
		wantErrorBody(t, e.get("/api/v1/videos/bad!/history"), http.StatusBadRequest)
		wantErrorBody(t, e.get("/api/v1/videos/"+id1+"/history?since=yesterday"), http.StatusBadRequest)
	})

	t.Run("history-capable store", func(t *testing.T) {
		now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
		e := newEnv(t, api.WithNow(func() time.Time { return now }))
		e.seed()
		hs := &histStore{
			Store: e.svc.Store,
			snaps: []collections.Snapshot{{
				VideoID:      id1,
				ViewCount:    100,
				LikeCount:    10,
				CommentCount: 1,
				CapturedAt:   now.Add(-time.Hour),
			}},
		}
		e.svc.Store = hs

		body := e.getJSON("/api/v1/videos/"+id1+"/history", http.StatusOK)
		if body["videoId"] != id1 {
			t.Fatalf("videoId = %v", body["videoId"])
		}
		snaps := body["snapshots"].([]any)
		if len(snaps) != 1 || snaps[0].(map[string]any)["viewCount"] != float64(100) {
			t.Fatalf("snapshots = %v", snaps)
		}
		if want := now.Add(-30 * 24 * time.Hour); !hs.gotSince.Equal(want) {
			t.Fatalf("default since = %v, want %v", hs.gotSince, want)
		}

		e.getJSON("/api/v1/videos/"+id1+"/history?since=2026-07-01T00:00:00Z", http.StatusOK)
		if want := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC); !hs.gotSince.Equal(want) {
			t.Fatalf("since = %v, want %v", hs.gotSince, want)
		}
	})
}

func TestMovers(t *testing.T) {
	t.Run("file mode is 501", func(t *testing.T) {
		e := newEnv(t)
		e.seed()
		wantErrorBody(t, e.get("/api/v1/collections/ai-conf-2026/movers"), http.StatusNotImplemented)
	})

	t.Run("errors", func(t *testing.T) {
		e := newEnv(t)
		e.seed()
		wantErrorBody(t, e.get("/api/v1/collections/nope/movers"), http.StatusNotFound)
		wantErrorBody(t, e.get("/api/v1/collections/ai-conf-2026/movers?window=1h"), http.StatusBadRequest)
		wantErrorBody(t, e.get("/api/v1/collections/ai-conf-2026/movers?limit=0"), http.StatusBadRequest)
	})

	t.Run("mover-capable store", func(t *testing.T) {
		e := newEnv(t)
		e.seed()
		video, err := e.svc.Store.GetVideo(context.Background(), id2)
		if err != nil {
			t.Fatal(err)
		}
		hs := &histStore{
			Store:  e.svc.Store,
			movers: []collections.Mover{{Video: video, Position: 1, PreviousPosition: 4, Change: 3}},
		}
		e.svc.Store = hs

		body := e.getJSON("/api/v1/collections/ai-conf-2026/movers", http.StatusOK)
		if body["window"] != "24h" {
			t.Fatalf("window = %v, want 24h (default)", body["window"])
		}
		if hs.gotWindow != 24*time.Hour {
			t.Fatalf("store window = %v", hs.gotWindow)
		}
		movers := body["movers"].([]any)
		if len(movers) != 1 {
			t.Fatalf("movers = %v", movers)
		}
		m := movers[0].(map[string]any)
		if m["id"] != id2 {
			t.Fatalf("mover id = %v", m["id"])
		}
		rank := m["ranking"].(map[string]any)
		if rank["position"] != float64(1) || rank["previousPosition"] != float64(4) || rank["change"] != float64(3) {
			t.Fatalf("mover ranking = %v", rank)
		}

		body = e.getJSON("/api/v1/collections/ai-conf-2026/movers?window=7d&limit=5", http.StatusOK)
		if body["window"] != "7d" || hs.gotWindow != 7*24*time.Hour || hs.gotLimit != 5 {
			t.Fatalf("window = %v (%v), limit = %d", body["window"], hs.gotWindow, hs.gotLimit)
		}
	})
}

func TestSync(t *testing.T) {
	t.Run("success returns the sync result", func(t *testing.T) {
		e := newEnv(t)
		body := decodeBody(t, e.do(http.MethodPost, "/api/v1/sync"), http.StatusOK)
		if body["fetched"] != float64(3) {
			t.Fatalf("fetched = %v, want 3", body["fetched"])
		}
		if failed, ok := body["failed"].([]any); !ok || len(failed) != 0 {
			t.Fatalf("failed = %v, want []", body["failed"])
		}
		if _, ok := body["startedAt"].(string); !ok {
			t.Fatalf("startedAt missing: %v", body)
		}
		if _, ok := body["durationSeconds"].(float64); !ok {
			t.Fatalf("durationSeconds missing: %v", body)
		}
	})

	t.Run("cooldown returns 429 with Retry-After", func(t *testing.T) {
		now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
		clock := &now
		e := newEnv(t,
			api.WithSyncCooldown(60*time.Second),
			api.WithNow(func() time.Time { return *clock }),
		)

		decodeBody(t, e.do(http.MethodPost, "/api/v1/sync"), http.StatusOK)

		*clock = now.Add(10 * time.Second)
		rec := e.do(http.MethodPost, "/api/v1/sync")
		wantErrorBody(t, rec, http.StatusTooManyRequests)
		ra, err := strconv.Atoi(rec.Header().Get("Retry-After"))
		if err != nil || ra != 50 {
			t.Fatalf("Retry-After = %q, want 50", rec.Header().Get("Retry-After"))
		}

		// After the cooldown elapses the endpoint works again.
		*clock = now.Add(61 * time.Second)
		decodeBody(t, e.do(http.MethodPost, "/api/v1/sync"), http.StatusOK)
	})

	t.Run("concurrent sync returns 429", func(t *testing.T) {
		fetcher := &fakeFetcher{
			videos:  baseVideos(),
			block:   make(chan struct{}),
			started: make(chan struct{}),
		}
		e := newEnvWith(t, collectionJSON, fetcher)

		firstDone := make(chan *httptest.ResponseRecorder)
		go func() { firstDone <- e.do(http.MethodPost, "/api/v1/sync") }()

		<-fetcher.started // first sync is now inside the engine
		rec := e.do(http.MethodPost, "/api/v1/sync")
		wantErrorBody(t, rec, http.StatusTooManyRequests)

		close(fetcher.block)
		decodeBody(t, <-firstDone, http.StatusOK)
	})

	t.Run("invalid collection file returns 400 with validation text", func(t *testing.T) {
		invalid := `{"schemaVersion":"1.0","slug":"bad-file","videos":[{"youtubeId":"vid00000001"}]}` // missing title
		e := newEnvWith(t, invalid, &fakeFetcher{videos: baseVideos()})
		msg := wantErrorBody(t, e.do(http.MethodPost, "/api/v1/sync"), http.StatusBadRequest)
		if !strings.Contains(msg, "title") {
			t.Fatalf("validation error should name the field: %q", msg)
		}
	})

	t.Run("GET is not allowed", func(t *testing.T) {
		e := newEnv(t)
		if rec := e.get("/api/v1/sync"); rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
	})
}

// TestNoAPIKeyLeak wires a real YouTube client with a known key against a
// fake YouTube API and asserts the key never appears in any response body.
func TestNoAPIKeyLeak(t *testing.T) {
	const secret = "SUPER-SECRET-KEY-42"

	yt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return items for id1 and id2 only; id3 becomes a failed ID.
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"items":[
			{"id":%q,"snippet":{"title":"One","channelId":"c1","channelTitle":"Chan","publishedAt":"2026-06-01T00:00:00Z","thumbnails":{"high":{"url":"https://i.ytimg.com/x.jpg"}}},"contentDetails":{"duration":"PT30M"},"statistics":{"viewCount":"100","likeCount":"10","commentCount":"1"}},
			{"id":%q,"snippet":{"title":"Two","channelId":"c1","channelTitle":"Chan","publishedAt":"2026-06-01T00:00:00Z","thumbnails":{"high":{"url":"https://i.ytimg.com/y.jpg"}}},"contentDetails":{"duration":"PT30M"},"statistics":{"viewCount":"300","likeCount":"5","commentCount":"9"}}
		]}`, id1, id2)
	}))
	defer yt.Close()

	path := filepath.Join(t.TempDir(), "collection.json")
	if err := os.WriteFile(path, []byte(collectionJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	store := collections.NewMemStore(collections.MemStoreOptions{Logger: discardLogger()})
	registry := rankings.DefaultRegistry()
	client := youtube.NewClient(secret, youtube.WithBaseURL(yt.URL))
	engine := syncpkg.New(store, client, registry, syncpkg.Options{
		CollectionPaths: []string{path},
		Logger:          discardLogger(),
	})
	svc := &service.Service{Store: store, Registry: registry}
	handler := api.New(svc, engine, api.WithLogger(discardLogger()), api.WithSyncCooldown(0))

	targets := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/sync"},
		{http.MethodGet, "/health"},
		{http.MethodGet, "/api/v1/collections"},
		{http.MethodGet, "/api/v1/collections/ai-conf-2026"},
		{http.MethodGet, "/api/v1/collections/ai-conf-2026/videos"},
		{http.MethodGet, "/api/v1/collections/ai-conf-2026/rankings"},
		{http.MethodGet, "/api/v1/collections/ai-conf-2026/rankings?sort=views_24h"}, // 501
		{http.MethodGet, "/api/v1/collections/ai-conf-2026/movers"},                  // 501
		{http.MethodGet, "/api/v1/videos/" + id1},
		{http.MethodGet, "/api/v1/videos/" + id1 + "/history"}, // 501
		{http.MethodGet, "/api/v1/videos/AAAAAAAAAAA"},         // 404
		{http.MethodGet, "/api/v1/collections/nope"},           // 404
		{http.MethodPost, "/api/v1/sync"},                      // second run, includes failed IDs
	}
	for _, target := range targets {
		req := httptest.NewRequest(target.method, target.path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("%s %s: response leaks the API key: %s", target.method, target.path, rec.Body.String())
		}
		for name, vals := range rec.Header() {
			for _, v := range vals {
				if strings.Contains(v, secret) {
					t.Fatalf("%s %s: header %s leaks the API key", target.method, target.path, name)
				}
			}
		}
	}
}

// TestMountable verifies the handler works behind a root mux exactly as
// cmd/server mounts it, with "/" left for the web UI.
func TestMountable(t *testing.T) {
	e := newEnv(t)
	e.seed()

	root := http.NewServeMux()
	root.Handle("/health", e.handler)
	root.Handle("/api/v1/", e.handler)

	srv := httptest.NewServer(root)
	defer srv.Close()

	for _, path := range []string{"/health", "/api/v1/collections", "/api/v1/collections/ai-conf-2026/rankings"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
	}
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("root path must stay unclaimed for the web UI, got %d", resp.StatusCode)
	}
}
