package rankings

import (
	"testing"
	"time"

	"github.com/denver/discovery/internal/collections"
)

var testNow = time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

func video(id string, stats *collections.Statistics) *collections.Video {
	return &collections.Video{ID: id, Statistics: stats}
}

func videoPublished(id string, stats *collections.Statistics, published time.Time) *collections.Video {
	v := video(id, stats)
	v.PublishedAt = &published
	return v
}

func TestStatStrategies(t *testing.T) {
	full := &collections.Statistics{ViewCount: 1000, LikeCount: 50, CommentCount: 7}

	tests := []struct {
		strategy Ranker
		wantName string
		want     float64
	}{
		{Views{}, "views", 1000},
		{Likes{}, "likes", 50},
		{Comments{}, "comments", 7},
		{Engagement{}, "engagement", 50 + 7*3},
	}
	for _, tc := range tests {
		t.Run(tc.wantName, func(t *testing.T) {
			if got := tc.strategy.Name(); got != tc.wantName {
				t.Errorf("Name() = %q, want %q", got, tc.wantName)
			}
			got, err := tc.strategy.Score(video("a", full), NoHistory{}, testNow)
			if err != nil {
				t.Fatalf("Score() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("Score() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStatStrategiesNilSafety(t *testing.T) {
	strategies := []Ranker{Views{}, Likes{}, Comments{}, Engagement{}}
	for _, s := range strategies {
		t.Run(s.Name(), func(t *testing.T) {
			got, err := s.Score(video("a", nil), NoHistory{}, testNow)
			if err != nil {
				t.Fatalf("Score(nil stats) error = %v", err)
			}
			if got != 0 {
				t.Errorf("Score(nil stats) = %v, want 0", got)
			}
			got, err = s.Score(nil, NoHistory{}, testNow)
			if err != nil {
				t.Fatalf("Score(nil video) error = %v", err)
			}
			if got != 0 {
				t.Errorf("Score(nil video) = %v, want 0", got)
			}
		})
	}
}

func TestStatStrategiesZeroCounts(t *testing.T) {
	zero := &collections.Statistics{}
	strategies := []Ranker{Views{}, Likes{}, Comments{}, Engagement{}}
	for _, s := range strategies {
		got, err := s.Score(video("a", zero), NoHistory{}, testNow)
		if err != nil {
			t.Fatalf("%s: Score(zero stats) error = %v", s.Name(), err)
		}
		if got != 0 {
			t.Errorf("%s: Score(zero stats) = %v, want 0", s.Name(), got)
		}
	}
}
