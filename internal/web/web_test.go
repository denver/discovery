package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/denver/discovery/internal/collections"
	"github.com/denver/discovery/internal/rankings"
	"github.com/denver/discovery/internal/service"
)

const testSlug = "test-videos"

func fixedNow() time.Time {
	return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
}

// newTestStore seeds a MemStore with one collection of three synced
// videos. Views order: bravo > alpha > charlie. Comments order:
// alpha > bravo > charlie.
func newTestStore(t *testing.T) *collections.MemStore {
	t.Helper()
	ctx := context.Background()
	store := collections.NewMemStore(collections.MemStoreOptions{})

	coll := &collections.Collection{
		SchemaVersion:  "1.0",
		Slug:           testSlug,
		Title:          "Test Videos",
		Description:    "Three AI Engineer talks.",
		DefaultRanking: "views",
		Videos: []collections.VideoEntry{
			{
				YouTubeID: "aaaaaaaaaaa",
				Speakers: []collections.Speaker{
					{Name: "Alexander Embiricos", Slug: "alexander-embiricos"},
					{Name: "Romain Huet", Slug: "romain-huet"},
				},
				Track:  "Keynotes",
				Topics: []string{"ai-engineering"},
			},
			{
				YouTubeID: "bbbbbbbbbbb",
				Speakers:  []collections.Speaker{{Name: "Garry Tan", Slug: "garry-tan"}},
				Track:     "Keynotes",
				Topics:    []string{"startups", "ai-engineering"},
				Featured:  true,
			},
			{
				YouTubeID: "ccccccccccc",
				Track:     "Agents",
				Topics:    []string{"agents", "performance"},
			},
		},
	}
	if err := store.UpsertCollection(ctx, coll); err != nil {
		t.Fatalf("UpsertCollection: %v", err)
	}

	pub := func(day int) time.Time {
		return time.Date(2026, 6, day, 10, 0, 0, 0, time.UTC)
	}
	provider := []collections.ProviderVideo{
		{
			ID: "aaaaaaaaaaa", Title: "Talk Alpha",
			ThumbnailURL: "https://i.ytimg.com/vi/aaaaaaaaaaa/hqdefault.jpg",
			ChannelID:    "chan1", ChannelName: "AI Engineer",
			PublishedAt: pub(10), DurationSeconds: 1200,
			Stats: collections.Statistics{ViewCount: 54321, LikeCount: 1200, CommentCount: 340, CapturedAt: fixedNow()},
		},
		{
			ID: "bbbbbbbbbbb", Title: "Talk Bravo",
			ThumbnailURL: "https://i.ytimg.com/vi/bbbbbbbbbbb/hqdefault.jpg",
			ChannelID:    "chan1", ChannelName: "AI Engineer",
			PublishedAt: pub(11), DurationSeconds: 900,
			Stats: collections.Statistics{ViewCount: 1800000000, LikeCount: 250000, CommentCount: 98, CapturedAt: fixedNow()},
		},
		{
			ID: "ccccccccccc", Title: "Talk Charlie",
			ThumbnailURL: "https://i.ytimg.com/vi/ccccccccccc/hqdefault.jpg",
			ChannelID:    "chan1", ChannelName: "AI Engineer",
			PublishedAt: pub(12), DurationSeconds: 600,
			Stats: collections.Statistics{ViewCount: 999, LikeCount: 10, CommentCount: 2, CapturedAt: fixedNow()},
		},
	}
	if err := store.UpsertProviderData(ctx, provider); err != nil {
		t.Fatalf("UpsertProviderData: %v", err)
	}
	if err := store.SetLastSyncedAt(ctx, testSlug, fixedNow().Add(-12*time.Minute)); err != nil {
		t.Fatalf("SetLastSyncedAt: %v", err)
	}
	return store
}

func newTestHandler(t *testing.T, store collections.Store) http.Handler {
	t.Helper()
	svc := &service.Service{
		Store:    store,
		Registry: rankings.DefaultRegistry(),
		Now:      fixedNow,
	}
	h, err := newHandler(svc, fixedNow)
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	return h
}

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.String()
}

// assertOrder fails unless the needles appear in body in the given order.
func assertOrder(t *testing.T, body string, needles ...string) {
	t.Helper()
	last := -1
	for _, n := range needles {
		i := strings.Index(body, n)
		if i < 0 {
			t.Fatalf("body does not contain %q", n)
		}
		if i < last {
			t.Fatalf("%q appears out of order", n)
		}
		last = i
	}
}

func TestNewConstructor(t *testing.T) {
	svc := &service.Service{Store: newTestStore(t), Registry: rankings.DefaultRegistry()}
	h, err := New(svc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if code, _ := get(t, h, "/"); code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", code)
	}
}

func TestIndexListsCollections(t *testing.T) {
	h := newTestHandler(t, newTestStore(t))
	code, body := get(t, h, "/")
	if code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", code)
	}
	for _, want := range []string{
		"Test Videos",
		"Three AI Engineer talks.",
		"3 videos",
		`href="/c/test-videos"`,
		"synced 12 minutes ago",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q", want)
		}
	}
}

func TestIndexEmptyState(t *testing.T) {
	h := newTestHandler(t, collections.NewMemStore(collections.MemStoreOptions{}))
	code, body := get(t, h, "/")
	if code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", code)
	}
	if !strings.Contains(body, "No collections yet.") {
		t.Error("empty index missing empty state")
	}
}

func TestLeaderboardDefaultRankOrder(t *testing.T) {
	h := newTestHandler(t, newTestStore(t))
	code, body := get(t, h, "/c/test-videos")
	if code != http.StatusOK {
		t.Fatalf("GET /c/test-videos = %d, want 200", code)
	}
	// Default sort is the collection's defaultRanking ("views").
	assertOrder(t, body, "Talk Bravo", "Talk Alpha", "Talk Charlie")
	for _, want := range []string{
		`<span class="rank-num">1</span>`,
		`<span class="rank-num">2</span>`,
		`<span class="rank-num">3</span>`,
		"refreshed 12 minutes ago",
		"<strong>1.8B</strong> views",
		"<strong>54.3K</strong> views",
		"<strong>999</strong> views",
		"AI Engineer",
		"Garry Tan",
		"Alexander Embiricos, Romain Huet",
		"Jun 11, 2026",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("leaderboard missing %q", want)
		}
	}
}

func TestLeaderboardSortParam(t *testing.T) {
	h := newTestHandler(t, newTestStore(t))
	code, body := get(t, h, "/c/test-videos?sort=comments")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	assertOrder(t, body, "Talk Alpha", "Talk Bravo", "Talk Charlie")
	active := `<a class="sort active" href="/c/test-videos?sort=comments">Comments</a>`
	if !strings.Contains(body, active) {
		t.Errorf("active sort tab missing: %q", active)
	}
}

func TestMovementIndicator(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	h := newTestHandler(t, store)

	// No prior rankings recorded: no movement markers at all.
	_, body := get(t, h, "/c/test-videos")
	for _, marker := range []string{"move-up", "move-down", "move-same", "↑", "↓"} {
		if strings.Contains(body, marker) {
			t.Fatalf("movement marker %q rendered without history", marker)
		}
	}

	// Two recorded runs: previous = first run. Current views order is
	// bravo(1) alpha(2) charlie(3).
	first := map[string]int{"aaaaaaaaaaa": 1, "bbbbbbbbbbb": 2, "ccccccccccc": 3}
	second := map[string]int{"bbbbbbbbbbb": 1, "aaaaaaaaaaa": 2, "ccccccccccc": 3}
	for i, positions := range []map[string]int{first, second} {
		at := fixedNow().Add(time.Duration(i-2) * time.Hour)
		if err := store.RecordRankings(ctx, testSlug, "views", positions, at); err != nil {
			t.Fatalf("RecordRankings: %v", err)
		}
	}

	_, body = get(t, h, "/c/test-videos")
	for _, want := range []string{
		`<span class="move move-up">↑1</span>`,
		`<span class="move move-down">↓1</span>`,
		`<span class="move move-same">—</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("leaderboard missing movement %q", want)
		}
	}
}

func TestLinksPreserveParams(t *testing.T) {
	h := newTestHandler(t, newTestStore(t))
	_, body := get(t, h, "/c/test-videos?sort=likes&track=Keynotes")

	// Switching sort keeps the track filter.
	sortLink := `href="/c/test-videos?sort=views&amp;track=Keynotes"`
	if !strings.Contains(body, sortLink) {
		t.Errorf("sort tab does not preserve track: want %q", sortLink)
	}
	// Adding a topic keeps sort and track.
	topicLink := `href="/c/test-videos?sort=likes&amp;topic=ai-engineering&amp;track=Keynotes"`
	if !strings.Contains(body, topicLink) {
		t.Errorf("topic chip does not preserve sort+track: want %q", topicLink)
	}
	// Clearing the track ("All") keeps the sort.
	allLink := `<a class="chip" href="/c/test-videos?sort=likes">All</a>`
	if !strings.Contains(body, allLink) {
		t.Errorf("All chip does not preserve sort: want %q", allLink)
	}
}

func TestTrackFilter(t *testing.T) {
	h := newTestHandler(t, newTestStore(t))
	_, body := get(t, h, "/c/test-videos?track=Agents")
	if !strings.Contains(body, "Talk Charlie") {
		t.Error("filtered leaderboard missing Talk Charlie")
	}
	for _, absent := range []string{"Talk Alpha", "Talk Bravo"} {
		if strings.Contains(body, absent) {
			t.Errorf("track filter leaked %q", absent)
		}
	}
}

func TestTopicFilter(t *testing.T) {
	h := newTestHandler(t, newTestStore(t))
	_, body := get(t, h, "/c/test-videos?topic=startups")
	if !strings.Contains(body, "Talk Bravo") {
		t.Error("filtered leaderboard missing Talk Bravo")
	}
	for _, absent := range []string{"Talk Alpha", "Talk Charlie"} {
		if strings.Contains(body, absent) {
			t.Errorf("topic filter leaked %q", absent)
		}
	}
}

func TestUnknownSlugNotFound(t *testing.T) {
	h := newTestHandler(t, newTestStore(t))
	code, body := get(t, h, "/c/nope")
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
	if !strings.Contains(body, "Page not found") {
		t.Error("404 page missing heading")
	}
}

func TestUnknownPathNotFound(t *testing.T) {
	h := newTestHandler(t, newTestStore(t))
	code, body := get(t, h, "/bogus/path")
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
	if !strings.Contains(body, "Page not found") {
		t.Error("404 page missing heading")
	}
}

func TestWindowedSortNotice(t *testing.T) {
	h := newTestHandler(t, newTestStore(t))
	code, body := get(t, h, "/c/test-videos?sort=views_24h")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (friendly notice, not an error page)", code)
	}
	if !strings.Contains(body, "database mode") {
		t.Error("notice missing 'database mode'")
	}
	if strings.Contains(body, "rank-num") {
		t.Error("cards rendered alongside history notice")
	}
}

func TestUnknownSortBadRequest(t *testing.T) {
	h := newTestHandler(t, newTestStore(t))
	code, _ := get(t, h, "/c/test-videos?sort=zzz")
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

func TestExternalLinksAreYouTubeWatchURLs(t *testing.T) {
	h := newTestHandler(t, newTestStore(t))
	_, body := get(t, h, "/c/test-videos")

	hrefs := regexp.MustCompile(`href="(https?://[^"]+)"`).FindAllStringSubmatch(body, -1)
	if len(hrefs) == 0 {
		t.Fatal("no external links found")
	}
	watch := 0
	for _, m := range hrefs {
		if !strings.HasPrefix(m[1], "https://www.youtube.com/watch?v=") {
			t.Errorf("external link %q is not a youtube.com watch URL", m[1])
			continue
		}
		watch++
	}
	// Title link + thumbnail link per card.
	if want := 6; watch != want {
		t.Errorf("youtube watch links = %d, want %d", watch, want)
	}
	if !strings.Contains(body, `target="_blank" rel="noopener"`) {
		t.Error(`youtube links missing target="_blank" rel="noopener"`)
	}
}

func TestFeaturedBadge(t *testing.T) {
	h := newTestHandler(t, newTestStore(t))
	_, body := get(t, h, "/c/test-videos")
	if !strings.Contains(body, `<span class="badge">Featured</span>`) {
		t.Error("featured badge missing for featured video")
	}
	// The only featured video is on the Keynotes track.
	_, body = get(t, h, "/c/test-videos?track=Agents")
	if strings.Contains(body, `<span class="badge">Featured</span>`) {
		t.Error("featured badge rendered for non-featured videos")
	}
}

func TestStaticStylesheet(t *testing.T) {
	h := newTestHandler(t, newTestStore(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/style.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/style.css = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Errorf("Content-Type = %q, want text/css", ct)
	}
	if !strings.Contains(rec.Body.String(), ":root") {
		t.Error("stylesheet body looks wrong")
	}
}
