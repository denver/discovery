package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/denver/discovery/internal/collections"
	"github.com/denver/discovery/internal/service"
)

// historyDefaultSince is how far back /videos/{id}/history reaches when
// ?since is omitted.
const historyDefaultSince = 30 * 24 * time.Hour

// moverStrategy is the ranking strategy movers are computed under.
const moverStrategy = "views"

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// collectionResponse projects CollectionInfo onto the contract's
// Collection schema. CollectionInfo embeds the raw source Collection,
// whose serialized form would leak schemaVersion and the raw video
// entries (including unpublished ones and editorial notes) — none of
// which are part of the API contract.
type collectionResponse struct {
	Slug           string              `json:"slug"`
	Title          string              `json:"title"`
	Description    string              `json:"description,omitempty"`
	Author         *collections.Author `json:"author,omitempty"`
	Source         *collections.Source `json:"source,omitempty"`
	DefaultRanking string              `json:"defaultRanking,omitempty"`
	VideoCount     int                 `json:"videoCount"`
	LastSyncedAt   *time.Time          `json:"lastSyncedAt,omitempty"`
}

func toCollectionResponse(info *collections.CollectionInfo) collectionResponse {
	return collectionResponse{
		Slug:           info.Slug,
		Title:          info.Title,
		Description:    info.Description,
		Author:         info.Author,
		Source:         info.Source,
		DefaultRanking: info.DefaultRanking,
		VideoCount:     info.VideoCount,
		LastSyncedAt:   info.LastSyncedAt,
	}
}

func (s *server) handleListCollections(w http.ResponseWriter, r *http.Request) {
	infos, err := s.svc.Collections(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := make([]collectionResponse, 0, len(infos))
	for _, info := range infos {
		out = append(out, toCollectionResponse(info))
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"collections": out})
}

func (s *server) handleGetCollection(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	info, err := s.svc.Collection(r.Context(), slug)
	if err != nil {
		s.writeError(w, r, wrapNotFound(err, "collection", slug))
		return
	}
	s.writeJSON(w, http.StatusOK, toCollectionResponse(info))
}

func (s *server) handleListVideos(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	f, err := parseFilters(r.URL.Query())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	page, err := s.svc.Videos(r.Context(), slug, f)
	if err != nil {
		s.writeError(w, r, wrapNotFound(err, "collection", slug))
		return
	}
	s.writeJSON(w, http.StatusOK, page)
}

// rankingsResponse is VideoPage plus the resolved strategy at top level.
type rankingsResponse struct {
	*service.VideoPage
	Strategy string `json:"strategy"`
}

func (s *server) handleRankings(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	f, err := parseFilters(r.URL.Query())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	page, strategy, err := s.svc.Rankings(r.Context(), slug, r.URL.Query().Get("sort"), f)
	if err != nil {
		s.writeError(w, r, wrapNotFound(err, "collection", slug))
		return
	}
	s.writeJSON(w, http.StatusOK, rankingsResponse{VideoPage: page, Strategy: strategy})
}

func (s *server) handleGetVideo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("youtubeId")
	if !youtubeIDPattern.MatchString(id) {
		s.writeError(w, r, fmt.Errorf("%w: youtubeId must match %s", service.ErrBadRequest, youtubeIDPattern))
		return
	}
	video, err := s.svc.Video(r.Context(), id)
	if err != nil {
		s.writeError(w, r, wrapNotFound(err, "video", id))
		return
	}
	s.writeJSON(w, http.StatusOK, video)
}

// historyResponse matches the contract's history payload.
type historyResponse struct {
	VideoID   string                 `json:"videoId"`
	Snapshots []collections.Snapshot `json:"snapshots"`
}

func (s *server) handleHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("youtubeId")
	if !youtubeIDPattern.MatchString(id) {
		s.writeError(w, r, fmt.Errorf("%w: youtubeId must match %s", service.ErrBadRequest, youtubeIDPattern))
		return
	}

	since := s.now().Add(-historyDefaultSince)
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			s.writeError(w, r, fmt.Errorf("%w: since must be RFC 3339 (e.g. 2026-07-01T00:00:00Z)", service.ErrBadRequest))
			return
		}
		since = t
	}

	// Unknown videos are a 404 even in file mode, where History itself
	// would only ever report 501.
	if _, err := s.svc.Video(r.Context(), id); err != nil {
		s.writeError(w, r, wrapNotFound(err, "video", id))
		return
	}

	snaps, err := s.svc.Store.History(r.Context(), id, since)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if snaps == nil {
		snaps = []collections.Snapshot{}
	}
	s.writeJSON(w, http.StatusOK, historyResponse{VideoID: id, Snapshots: snaps})
}

// moversResponse matches the contract's movers payload: plain Videos,
// each carrying its rank movement in the ranking object.
type moversResponse struct {
	Window string               `json:"window"`
	Movers []*collections.Video `json:"movers"`
}

func (s *server) handleMovers(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	windowParam := r.URL.Query().Get("window")
	var window time.Duration
	switch windowParam {
	case "", "24h":
		windowParam, window = "24h", 24*time.Hour
	case "7d":
		window = 7 * 24 * time.Hour
	default:
		s.writeError(w, r, fmt.Errorf("%w: window must be one of 24h, 7d", service.ErrBadRequest))
		return
	}
	limit, err := parseIntParam(r.URL.Query(), "limit", 1, service.MaxLimit)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	// Unknown collections are a 404 even in file mode, where Movers
	// itself would only ever report 501.
	if _, err := s.svc.Collection(r.Context(), slug); err != nil {
		s.writeError(w, r, wrapNotFound(err, "collection", slug))
		return
	}

	movers, err := s.svc.Movers(r.Context(), slug, moverStrategy, window, limit)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	videos := make([]*collections.Video, 0, len(movers))
	for _, m := range movers {
		v := *m.Video // shallow copy so we never mutate store-owned data
		pp, ch := m.PreviousPosition, m.Change
		v.Ranking = &collections.Ranking{
			Position:         m.Position,
			PreviousPosition: &pp,
			Change:           &ch,
			Score:            float64(ch),
			Strategy:         moverStrategy,
		}
		videos = append(videos, &v)
	}
	s.writeJSON(w, http.StatusOK, moversResponse{Window: windowParam, Movers: videos})
}
