package rankings

import (
	"time"

	"github.com/denver/discovery/internal/collections"
)

// Views is the "views" strategy: score = viewCount.
type Views struct{}

// Name implements Ranker.
func (Views) Name() string { return "views" }

// Score implements Ranker. Videos with nil Statistics score 0.
func (Views) Score(v *collections.Video, _ History, _ time.Time) (float64, error) {
	if s := stats(v); s != nil {
		return float64(s.ViewCount), nil
	}
	return 0, nil
}

// Likes is the "likes" strategy: score = likeCount.
type Likes struct{}

// Name implements Ranker.
func (Likes) Name() string { return "likes" }

// Score implements Ranker. Videos with nil Statistics score 0.
func (Likes) Score(v *collections.Video, _ History, _ time.Time) (float64, error) {
	if s := stats(v); s != nil {
		return float64(s.LikeCount), nil
	}
	return 0, nil
}

// Comments is the "comments" strategy: score = commentCount.
type Comments struct{}

// Name implements Ranker.
func (Comments) Name() string { return "comments" }

// Score implements Ranker. Videos with nil Statistics score 0.
func (Comments) Score(v *collections.Video, _ History, _ time.Time) (float64, error) {
	if s := stats(v); s != nil {
		return float64(s.CommentCount), nil
	}
	return 0, nil
}

// Engagement is the "engagement" strategy.
//
// Formula: score = likeCount + commentCount*3. A comment is a stronger
// engagement signal than a like (it costs the viewer real effort), so
// comments weigh 3x.
type Engagement struct{}

// Name implements Ranker.
func (Engagement) Name() string { return "engagement" }

// Score implements Ranker. Videos with nil Statistics score 0.
func (Engagement) Score(v *collections.Video, _ History, _ time.Time) (float64, error) {
	if s := stats(v); s != nil {
		return float64(s.LikeCount) + float64(s.CommentCount)*3, nil
	}
	return 0, nil
}

// stats returns the video's statistics, or nil when the video has none
// (not yet synced) or the video itself is nil. Keeps stat-based
// strategies panic-free on partial data.
func stats(v *collections.Video) *collections.Statistics {
	if v == nil {
		return nil
	}
	return v.Statistics
}
