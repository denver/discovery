// Package service is the shared read side consumed by both the REST API
// and the web UI: filtering, pagination, and ranking assembly over a
// Store. Handlers translate its errors; they do not reimplement its logic.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/denver/discovery/internal/collections"
	"github.com/denver/discovery/internal/rankings"
)

// DefaultLimit and MaxLimit bound pagination.
const (
	DefaultLimit = 25
	MaxLimit     = 100
)

// ErrBadRequest wraps caller errors (unknown strategy, bad pagination);
// the API maps it to 400.
var ErrBadRequest = errors.New("bad request")

// Service assembles API responses from a Store and a strategy registry.
type Service struct {
	Store    collections.Store
	Registry *rankings.Registry
	// Now is the reference clock for windowed strategies. Defaults to
	// time.Now when nil.
	Now func() time.Time
}

// Filters narrow and paginate video listings.
type Filters struct {
	Topic   string
	Track   string
	Speaker string
	Limit   int // 0 means DefaultLimit
	Offset  int
}

// VideoPage is one page of results plus pagination metadata.
type VideoPage struct {
	Videos []*collections.Video `json:"videos"`
	Total  int                  `json:"total"`
	Limit  int                  `json:"limit"`
	Offset int                  `json:"offset"`
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Collections lists all collections.
func (s *Service) Collections(ctx context.Context) ([]*collections.CollectionInfo, error) {
	return s.Store.ListCollections(ctx)
}

// Collection returns one collection by slug.
func (s *Service) Collection(ctx context.Context, slug string) (*collections.CollectionInfo, error) {
	return s.Store.GetCollection(ctx, slug)
}

// Video returns one video by YouTube ID.
func (s *Service) Video(ctx context.Context, youtubeID string) (*collections.Video, error) {
	return s.Store.GetVideo(ctx, youtubeID)
}

// Videos returns a collection's videos in editorial order, filtered and
// paginated, without ranking info.
func (s *Service) Videos(ctx context.Context, slug string, f Filters) (*VideoPage, error) {
	limit, offset, err := f.bounds()
	if err != nil {
		return nil, err
	}
	videos, err := s.Store.ListVideos(ctx, slug)
	if err != nil {
		return nil, err
	}
	videos = applyFilters(videos, f)
	return paginate(videos, limit, offset), nil
}

// Rankings returns a collection's videos ordered by the named strategy,
// each carrying a Ranking with previousPosition/change when a prior
// recording exists. An empty strategy resolves to the collection's
// defaultRanking, then "views". The returned string is the resolved
// strategy name.
//
// rank_change_24h is served through the MoverStore capability rather than
// a scoring strategy; without that capability the store's history errors
// surface (file mode → ErrHistoryRequired → 501 upstream).
func (s *Service) Rankings(ctx context.Context, slug, strategy string, f Filters) (*VideoPage, string, error) {
	limit, offset, err := f.bounds()
	if err != nil {
		return nil, "", err
	}

	info, err := s.Store.GetCollection(ctx, slug)
	if err != nil {
		return nil, "", err
	}
	if strategy == "" {
		strategy = info.DefaultRanking
	}
	if strategy == "" {
		strategy = "views"
	}

	if strategy == "rank_change_24h" {
		page, err := s.rankChangePage(ctx, slug, f, limit, offset)
		return page, strategy, err
	}

	ranker, err := s.Registry.Get(strategy)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %s", ErrBadRequest, err)
	}
	videos, err := s.Store.ListVideos(ctx, slug)
	if err != nil {
		return nil, "", err
	}
	videos = applyFilters(videos, f)

	ranked, err := rankings.Rank(videos, ranker, storeHistory{ctx: ctx, store: s.Store}, s.now())
	if err != nil {
		return nil, "", err
	}

	prev, err := s.Store.PreviousRankings(ctx, slug, strategy)
	if err != nil {
		return nil, "", err
	}
	out := make([]*collections.Video, len(ranked))
	for i, r := range ranked {
		v := *r.Video // shallow copy so we never mutate store-owned data
		v.Ranking = &collections.Ranking{
			Position: r.Position,
			Score:    r.Score,
			Strategy: strategy,
		}
		if p, ok := prev[v.ID]; ok {
			pp, ch := p, p-r.Position
			v.Ranking.PreviousPosition = &pp
			v.Ranking.Change = &ch
		}
		out[i] = &v
	}
	return paginate(out, limit, offset), strategy, nil
}

// Movers returns the biggest rank changes over a window, database mode
// only. Stores without the MoverStore capability get ErrHistoryUnavailable.
func (s *Service) Movers(ctx context.Context, slug, strategy string, window time.Duration, limit int) ([]collections.Mover, error) {
	ms, ok := s.Store.(collections.MoverStore)
	if !ok {
		return nil, collections.ErrHistoryUnavailable
	}
	if strategy == "" {
		strategy = "views"
	}
	if limit <= 0 || limit > MaxLimit {
		limit = DefaultLimit
	}
	return ms.Movers(ctx, slug, strategy, window, limit)
}

func (s *Service) rankChangePage(ctx context.Context, slug string, f Filters, limit, offset int) (*VideoPage, error) {
	movers, err := s.Movers(ctx, slug, "views", 24*time.Hour, MaxLimit)
	if err != nil {
		if errors.Is(err, collections.ErrHistoryUnavailable) {
			return nil, rankings.ErrHistoryRequired
		}
		return nil, err
	}
	videos := make([]*collections.Video, 0, len(movers))
	for _, m := range movers {
		v := *m.Video
		pp, ch := m.PreviousPosition, m.Change
		v.Ranking = &collections.Ranking{
			Position:         m.Position,
			PreviousPosition: &pp,
			Change:           &ch,
			Score:            float64(ch),
			Strategy:         "rank_change_24h",
		}
		videos = append(videos, &v)
	}
	videos = applyFilters(videos, f)
	return paginate(videos, limit, offset), nil
}

func (f Filters) bounds() (limit, offset int, err error) {
	limit, offset = f.Limit, f.Offset
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 0 || limit > MaxLimit {
		return 0, 0, fmt.Errorf("%w: limit must be 1-%d", ErrBadRequest, MaxLimit)
	}
	if offset < 0 {
		return 0, 0, fmt.Errorf("%w: offset must be >= 0", ErrBadRequest)
	}
	return limit, offset, nil
}

func applyFilters(videos []*collections.Video, f Filters) []*collections.Video {
	if f.Topic == "" && f.Track == "" && f.Speaker == "" {
		return videos
	}
	out := videos[:0:0]
	for _, v := range videos {
		if f.Topic != "" && !containsFold(v.Editorial.Topics, f.Topic) {
			continue
		}
		if f.Track != "" && !strings.EqualFold(v.Editorial.Track, f.Track) {
			continue
		}
		if f.Speaker != "" && !hasSpeaker(v.Editorial.Speakers, f.Speaker) {
			continue
		}
		out = append(out, v)
	}
	return out
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

func hasSpeaker(speakers []collections.Speaker, slug string) bool {
	for _, sp := range speakers {
		if strings.EqualFold(sp.Slug, slug) {
			return true
		}
	}
	return false
}

func paginate(videos []*collections.Video, limit, offset int) *VideoPage {
	total := len(videos)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := videos[offset:end]
	if page == nil {
		page = []*collections.Video{}
	}
	return &VideoPage{Videos: page, Total: total, Limit: limit, Offset: offset}
}

// storeHistory adapts Store.History to the rankings.History interface for
// windowed strategies.
type storeHistory struct {
	ctx   context.Context
	store collections.Store
}

func (h storeHistory) Snapshots(videoID string, since time.Time) ([]collections.Snapshot, error) {
	return h.store.History(h.ctx, videoID, since)
}
