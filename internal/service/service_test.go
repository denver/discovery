package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/denver/discovery/internal/collections"
	"github.com/denver/discovery/internal/rankings"
)

func seed(t *testing.T) *Service {
	t.Helper()
	store := collections.NewMemStore(collections.MemStoreOptions{})
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()

	c := &collections.Collection{
		SchemaVersion:  "1.0",
		Slug:           "test",
		Title:          "Test",
		DefaultRanking: "views",
		Videos: []collections.VideoEntry{
			{YouTubeID: "aaaaaaaaaaa", Track: "Agents", Topics: []string{"agents"},
				Speakers: []collections.Speaker{{Name: "Alice", Slug: "alice"}}},
			{YouTubeID: "bbbbbbbbbbb", Track: "Keynotes", Topics: []string{"startups"},
				Speakers: []collections.Speaker{{Name: "Bob", Slug: "bob"}}},
			{YouTubeID: "ccccccccccc", Track: "Agents", Topics: []string{"agents", "performance"}},
		},
	}
	if err := store.UpsertCollection(ctx, c); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	provider := []collections.ProviderVideo{
		{ID: "aaaaaaaaaaa", Title: "A", Stats: collections.Statistics{ViewCount: 100, LikeCount: 10, CommentCount: 1, CapturedAt: now}},
		{ID: "bbbbbbbbbbb", Title: "B", Stats: collections.Statistics{ViewCount: 300, LikeCount: 5, CommentCount: 50, CapturedAt: now}},
		{ID: "ccccccccccc", Title: "C", Stats: collections.Statistics{ViewCount: 200, LikeCount: 20, CommentCount: 2, CapturedAt: now}},
	}
	if err := store.UpsertProviderData(ctx, provider); err != nil {
		t.Fatal(err)
	}
	return &Service{Store: store, Registry: rankings.DefaultRegistry(), Now: func() time.Time { return now }}
}

func ids(page *VideoPage) []string {
	out := make([]string, len(page.Videos))
	for i, v := range page.Videos {
		out[i] = v.ID
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestRankingsDefaultStrategy(t *testing.T) {
	s := seed(t)
	page, strategy, err := s.Rankings(context.Background(), "test", "", Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if strategy != "views" {
		t.Errorf("strategy = %q, want views (collection default)", strategy)
	}
	eq(t, ids(page), []string{"bbbbbbbbbbb", "ccccccccccc", "aaaaaaaaaaa"})
	if page.Videos[0].Ranking == nil || page.Videos[0].Ranking.Position != 1 {
		t.Error("missing or wrong Ranking on first video")
	}
	if page.Videos[0].Ranking.PreviousPosition != nil {
		t.Error("previousPosition should be nil with no prior recording")
	}
}

func TestRankingsEngagementOrder(t *testing.T) {
	s := seed(t)
	page, _, err := s.Rankings(context.Background(), "test", "engagement", Filters{})
	if err != nil {
		t.Fatal(err)
	}
	// engagement: b=5+50*3=155, c=20+2*3=26, a=10+1*3=13
	eq(t, ids(page), []string{"bbbbbbbbbbb", "ccccccccccc", "aaaaaaaaaaa"})
}

func TestRankingsPreviousPositions(t *testing.T) {
	s := seed(t)
	ctx := context.Background()
	at := time.Date(2026, 7, 19, 6, 0, 0, 0, time.UTC)
	// Prior run had a first, b second, c third.
	prev := map[string]int{"aaaaaaaaaaa": 1, "bbbbbbbbbbb": 2, "ccccccccccc": 3}
	if err := s.Store.RecordRankings(ctx, "test", "views", prev, at); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.RecordRankings(ctx, "test", "views", map[string]int{"aaaaaaaaaaa": 3, "bbbbbbbbbbb": 1, "ccccccccccc": 2}, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	page, _, err := s.Rankings(ctx, "test", "views", Filters{})
	if err != nil {
		t.Fatal(err)
	}
	top := page.Videos[0] // b, current #1, previous #2
	if top.Ranking.PreviousPosition == nil || *top.Ranking.PreviousPosition != 2 {
		t.Fatalf("previousPosition = %v, want 2", top.Ranking.PreviousPosition)
	}
	if top.Ranking.Change == nil || *top.Ranking.Change != 1 {
		t.Fatalf("change = %v, want +1", top.Ranking.Change)
	}
}

func TestFilters(t *testing.T) {
	s := seed(t)
	ctx := context.Background()

	page, _, err := s.Rankings(ctx, "test", "views", Filters{Track: "agents"}) // case-insensitive
	if err != nil {
		t.Fatal(err)
	}
	eq(t, ids(page), []string{"ccccccccccc", "aaaaaaaaaaa"})

	page, _, err = s.Rankings(ctx, "test", "views", Filters{Topic: "startups"})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, ids(page), []string{"bbbbbbbbbbb"})

	page, _, err = s.Rankings(ctx, "test", "views", Filters{Speaker: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, ids(page), []string{"aaaaaaaaaaa"})

	page, err = s.Videos(ctx, "test", Filters{Topic: "agents"})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, ids(page), []string{"aaaaaaaaaaa", "ccccccccccc"}) // editorial order
}

func TestPagination(t *testing.T) {
	s := seed(t)
	page, _, err := s.Rankings(context.Background(), "test", "views", Filters{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || page.Limit != 2 || page.Offset != 2 {
		t.Errorf("page meta = %+v", page)
	}
	eq(t, ids(page), []string{"aaaaaaaaaaa"})

	if _, _, err := s.Rankings(context.Background(), "test", "views", Filters{Limit: 500}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("limit=500 should be ErrBadRequest, got %v", err)
	}
}

func TestUnknownStrategyIsBadRequest(t *testing.T) {
	s := seed(t)
	if _, _, err := s.Rankings(context.Background(), "test", "nope", Filters{}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("want ErrBadRequest, got %v", err)
	}
}

func TestWindowedStrategyFileMode(t *testing.T) {
	s := seed(t)
	if _, _, err := s.Rankings(context.Background(), "test", "views_24h", Filters{}); !errors.Is(err, rankings.ErrHistoryRequired) {
		t.Errorf("want ErrHistoryRequired, got %v", err)
	}
	if _, _, err := s.Rankings(context.Background(), "test", "rank_change_24h", Filters{}); !errors.Is(err, rankings.ErrHistoryRequired) {
		t.Errorf("rank_change_24h in file mode: want ErrHistoryRequired, got %v", err)
	}
}

func TestNotFoundPassthrough(t *testing.T) {
	s := seed(t)
	if _, _, err := s.Rankings(context.Background(), "missing", "views", Filters{}); !errors.Is(err, collections.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if _, err := s.Video(context.Background(), "zzzzzzzzzzz"); !errors.Is(err, collections.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}
