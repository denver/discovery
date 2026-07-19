package rankings

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/denver/discovery/internal/collections"
)

// fakeHistory serves canned snapshots, filtering by since like a real
// store would. A non-nil err is returned from every call.
type fakeHistory struct {
	snaps map[string][]collections.Snapshot
	err   error
}

func (f fakeHistory) Snapshots(videoID string, since time.Time) ([]collections.Snapshot, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []collections.Snapshot
	for _, s := range f.snaps[videoID] {
		if !s.CapturedAt.Before(since) {
			out = append(out, s)
		}
	}
	return out, nil
}

func snap(views int64, capturedAt time.Time) collections.Snapshot {
	return collections.Snapshot{VideoID: "a", ViewCount: views, CapturedAt: capturedAt}
}

func windowedNames() []string {
	return []string{"views_24h", "views_7d", "growth_percent_24h", "rank_change_24h"}
}

func TestWindowedHistoryRequired(t *testing.T) {
	reg := DefaultRegistry()
	for _, name := range windowedNames() {
		t.Run(name, func(t *testing.T) {
			rk, err := reg.Get(name)
			if err != nil {
				t.Fatalf("Get(%q) error = %v", name, err)
			}
			_, err = rk.Score(video("a", nil), NoHistory{}, testNow)
			if !errors.Is(err, ErrHistoryRequired) {
				t.Errorf("Score() with NoHistory error = %v, want ErrHistoryRequired", err)
			}
			_, err = rk.Score(video("a", nil), nil, testNow)
			if !errors.Is(err, ErrHistoryRequired) {
				t.Errorf("Score() with nil History error = %v, want ErrHistoryRequired", err)
			}
		})
	}
}

func TestWindowedViewDelta(t *testing.T) {
	hist := fakeHistory{snaps: map[string][]collections.Snapshot{
		"a": {
			snap(40, testNow.Add(-30*time.Hour)),  // outside 24h window
			snap(100, testNow.Add(-20*time.Hour)), // 24h baseline
			snap(150, testNow.Add(-1*time.Hour)),
		},
		"weekly": {
			snap(1000, testNow.Add(-8*day)), // outside 7d window
			snap(2000, testNow.Add(-6*day)), // 7d baseline
			snap(2300, testNow.Add(-1*time.Hour)),
		},
		"single": {
			snap(500, testNow.Add(-2*time.Hour)),
		},
	}}

	tests := []struct {
		name     string
		strategy string
		videoID  string
		want     float64
	}{
		{"delta inside 24h window", "views_24h", "a", 50},
		{"delta inside 7d window", "views_7d", "weekly", 300},
		{"7d window sees the 24h history too", "views_7d", "a", 110},
		{"single snapshot has no baseline", "views_24h", "single", 0},
		{"no snapshots at all", "views_24h", "missing", 0},
	}
	reg := DefaultRegistry()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rk, err := reg.Get(tc.strategy)
			if err != nil {
				t.Fatalf("Get(%q) error = %v", tc.strategy, err)
			}
			got, err := rk.Score(video(tc.videoID, nil), hist, testNow)
			if err != nil {
				t.Fatalf("Score() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("Score() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWindowedGrowthPercent(t *testing.T) {
	hist := fakeHistory{snaps: map[string][]collections.Snapshot{
		"grew": {
			snap(100, testNow.Add(-20*time.Hour)),
			snap(150, testNow.Add(-1*time.Hour)),
		},
		"zero-baseline": {
			snap(0, testNow.Add(-20*time.Hour)),
			snap(500, testNow.Add(-1*time.Hour)),
		},
		"shrank": {
			snap(200, testNow.Add(-20*time.Hour)),
			snap(150, testNow.Add(-1*time.Hour)),
		},
	}}

	tests := []struct {
		videoID string
		want    float64
	}{
		{"grew", 50},
		{"zero-baseline", 0}, // never divides by zero
		{"shrank", -25},
		{"missing", 0},
	}
	rk, err := DefaultRegistry().Get("growth_percent_24h")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	for _, tc := range tests {
		t.Run(tc.videoID, func(t *testing.T) {
			got, err := rk.Score(video(tc.videoID, nil), hist, testNow)
			if err != nil {
				t.Fatalf("Score() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("Score() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWindowedRankChangeStubbed(t *testing.T) {
	hist := fakeHistory{snaps: map[string][]collections.Snapshot{
		"a": {
			snap(100, testNow.Add(-20*time.Hour)),
			snap(150, testNow.Add(-1*time.Hour)),
		},
	}}
	rk, err := DefaultRegistry().Get("rank_change_24h")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	_, err = rk.Score(video("a", nil), hist, testNow)
	if err == nil {
		t.Fatal("Score() error = nil, want T16 stub error")
	}
	if errors.Is(err, ErrHistoryRequired) {
		t.Errorf("Score() with available history = ErrHistoryRequired, want T16 stub error")
	}
	if !strings.Contains(err.Error(), "T16") {
		t.Errorf("Score() error = %q, want mention of T16", err)
	}
}

func TestWindowedPropagatesStoreErrors(t *testing.T) {
	boom := errors.New("boom")
	rk, err := DefaultRegistry().Get("views_24h")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	_, err = rk.Score(video("a", nil), fakeHistory{err: boom}, testNow)
	if !errors.Is(err, boom) {
		t.Errorf("Score() error = %v, want wrapped boom", err)
	}
	if errors.Is(err, ErrHistoryRequired) {
		t.Errorf("Score() error = ErrHistoryRequired, want only unavailability translated")
	}
}

func TestWindowedNilVideo(t *testing.T) {
	rk, err := DefaultRegistry().Get("views_24h")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	got, err := rk.Score(nil, fakeHistory{}, testNow)
	if err != nil {
		t.Fatalf("Score(nil video) error = %v", err)
	}
	if got != 0 {
		t.Errorf("Score(nil video) = %v, want 0", got)
	}
}
