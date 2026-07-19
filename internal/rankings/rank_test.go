package rankings

import (
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/denver/discovery/internal/collections"
)

func viewStats(views int64) *collections.Statistics {
	return &collections.Statistics{ViewCount: views}
}

// tieBreakFixture returns videos whose expected order under "views"
// exercises every tie-break rule: score desc, PublishedAt desc (nil
// last), ID asc. Expected order of IDs:
//
//	top, tie-young-a, tie-young-b, tie-old, nostats-dated, nostats-a, nostats-b
func tieBreakFixture() ([]*collections.Video, []string) {
	young := testNow.Add(-24 * time.Hour)
	old := testNow.Add(-30 * day)
	videos := []*collections.Video{
		videoPublished("top", viewStats(500), old),
		videoPublished("tie-young-b", viewStats(100), young),
		videoPublished("tie-young-a", viewStats(100), young),
		videoPublished("tie-old", viewStats(100), old),
		videoPublished("nostats-dated", nil, young),
		video("nostats-b", nil),
		video("nostats-a", nil),
	}
	want := []string{"top", "tie-young-a", "tie-young-b", "tie-old", "nostats-dated", "nostats-a", "nostats-b"}
	return videos, want
}

func TestRankTieBreaking(t *testing.T) {
	videos, want := tieBreakFixture()
	ranked, err := Rank(videos, Views{}, NoHistory{}, testNow)
	if err != nil {
		t.Fatalf("Rank() error = %v", err)
	}
	if len(ranked) != len(want) {
		t.Fatalf("Rank() returned %d videos, want %d", len(ranked), len(want))
	}
	for i, r := range ranked {
		if r.Video.ID != want[i] {
			t.Errorf("position %d = %q, want %q", i+1, r.Video.ID, want[i])
		}
		if r.Position != i+1 {
			t.Errorf("Position at index %d = %d, want dense %d", i, r.Position, i+1)
		}
	}
	if ranked[0].Score != 500 {
		t.Errorf("top score = %v, want 500", ranked[0].Score)
	}
	for _, r := range ranked[4:] {
		if r.Score != 0 {
			t.Errorf("%s score = %v, want 0 (nil stats)", r.Video.ID, r.Score)
		}
	}
}

func TestRankDeterministicUnderShuffle(t *testing.T) {
	videos, want := tieBreakFixture()
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 25; i++ {
		shuffled := make([]*collections.Video, len(videos))
		copy(shuffled, videos)
		rng.Shuffle(len(shuffled), func(a, b int) {
			shuffled[a], shuffled[b] = shuffled[b], shuffled[a]
		})
		ranked, err := Rank(shuffled, Views{}, NoHistory{}, testNow)
		if err != nil {
			t.Fatalf("Rank() error = %v", err)
		}
		for j, r := range ranked {
			if r.Video.ID != want[j] {
				t.Fatalf("shuffle %d: position %d = %q, want %q", i, j+1, r.Video.ID, want[j])
			}
		}
	}
}

func TestRankEmptyInput(t *testing.T) {
	ranked, err := Rank(nil, Views{}, NoHistory{}, testNow)
	if err != nil {
		t.Fatalf("Rank() error = %v", err)
	}
	if len(ranked) != 0 {
		t.Errorf("Rank() returned %d videos, want 0", len(ranked))
	}
}

func TestRankFailsWholeOnHistoryRequired(t *testing.T) {
	videos := []*collections.Video{
		video("a", viewStats(10)),
		video("b", viewStats(20)),
	}
	rk, err := DefaultRegistry().Get("views_24h")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	ranked, err := Rank(videos, rk, NoHistory{}, testNow)
	if !errors.Is(err, ErrHistoryRequired) {
		t.Fatalf("Rank() error = %v, want ErrHistoryRequired", err)
	}
	if ranked != nil {
		t.Errorf("Rank() = %v, want nil on error", ranked)
	}
}

func TestRankWindowedWithFakeHistory(t *testing.T) {
	hist := fakeHistory{snaps: map[string][]collections.Snapshot{
		"fast": {
			{VideoID: "fast", ViewCount: 100, CapturedAt: testNow.Add(-20 * time.Hour)},
			{VideoID: "fast", ViewCount: 400, CapturedAt: testNow.Add(-1 * time.Hour)},
		},
		"slow": {
			{VideoID: "slow", ViewCount: 100, CapturedAt: testNow.Add(-20 * time.Hour)},
			{VideoID: "slow", ViewCount: 150, CapturedAt: testNow.Add(-1 * time.Hour)},
		},
	}}
	videos := []*collections.Video{
		video("slow", nil),
		video("new", nil), // no snapshots: scores 0, ranks last
		video("fast", nil),
	}
	rk, err := DefaultRegistry().Get("views_24h")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	ranked, err := Rank(videos, rk, hist, testNow)
	if err != nil {
		t.Fatalf("Rank() error = %v", err)
	}
	wantOrder := []string{"fast", "slow", "new"}
	wantScores := []float64{300, 50, 0}
	for i, r := range ranked {
		if r.Video.ID != wantOrder[i] || r.Score != wantScores[i] || r.Position != i+1 {
			t.Errorf("rank %d = (%s, %v, pos %d), want (%s, %v, pos %d)",
				i+1, r.Video.ID, r.Score, r.Position, wantOrder[i], wantScores[i], i+1)
		}
	}
}

func TestDefaultRegistry(t *testing.T) {
	reg := DefaultRegistry()
	want := []string{
		"comments", "engagement", "growth_percent_24h", "likes",
		"rank_change_24h", "views", "views_24h", "views_7d",
	}
	got := reg.Names()
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names() = %v, want %v", got, want)
		}
	}
	for _, name := range want {
		rk, err := reg.Get(name)
		if err != nil {
			t.Errorf("Get(%q) error = %v", name, err)
			continue
		}
		if rk.Name() != name {
			t.Errorf("Get(%q).Name() = %q", name, rk.Name())
		}
	}
}

func TestRegistryUnknownStrategy(t *testing.T) {
	_, err := DefaultRegistry().Get("upvotes")
	if err == nil {
		t.Fatal("Get(unknown) error = nil, want error")
	}
}
