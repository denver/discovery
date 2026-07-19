package rankings

import (
	"fmt"
	"sort"
	"time"

	"github.com/denver/discovery/internal/collections"
)

// Rank scores every video under one strategy and returns them ordered
// best-first with dense 1-based positions. Ties break deterministically:
// score descending, then PublishedAt descending (nil PublishedAt last),
// then ID ascending — the same input always produces the same order.
// Videos with nil Statistics score 0 and land after scored videos.
//
// Any Score error aborts the ranking as a whole; ErrHistoryRequired is
// preserved for errors.Is.
func Rank(videos []*collections.Video, r Ranker, hist History, now time.Time) ([]Ranked, error) {
	ranked := make([]Ranked, 0, len(videos))
	for _, v := range videos {
		score, err := r.Score(v, hist, now)
		if err != nil {
			return nil, fmt.Errorf("strategy %q: %w", r.Name(), err)
		}
		ranked = append(ranked, Ranked{Video: v, Score: score})
	}
	sort.Slice(ranked, func(i, j int) bool { return rankedLess(ranked[i], ranked[j]) })
	for i := range ranked {
		ranked[i].Position = i + 1
	}
	return ranked, nil
}

// rankedLess reports whether a ranks before b under the deterministic
// tie-break rules documented on Rank.
func rankedLess(a, b Ranked) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	pa, pb := publishedAt(a.Video), publishedAt(b.Video)
	switch {
	case pa != nil && pb == nil:
		return true
	case pa == nil && pb != nil:
		return false
	case pa != nil && pb != nil && !pa.Equal(*pb):
		return pa.After(*pb)
	}
	return videoID(a.Video) < videoID(b.Video)
}

func publishedAt(v *collections.Video) *time.Time {
	if v == nil {
		return nil
	}
	return v.PublishedAt
}

func videoID(v *collections.Video) string {
	if v == nil {
		return ""
	}
	return v.ID
}
