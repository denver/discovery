package collections

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"
)

// providerName is the only video provider in the MVP.
const providerName = "youtube"

// youtubeWatchURL returns the canonical public watch URL for a video ID.
func youtubeWatchURL(id string) string {
	return "https://www.youtube.com/watch?v=" + id
}

// MemStoreOptions configures NewMemStore.
type MemStoreOptions struct {
	// CachePath is a JSON file persisting provider data, metric snapshots
	// (latest + previous), rankings (latest + previous), and last-sync
	// times across restarts, so a restart serves metadata without
	// refetching from YouTube. Empty disables persistence.
	CachePath string

	// Logger receives cache warnings (corrupt cache discarded, failed
	// writes). Defaults to slog.Default().
	Logger *slog.Logger
}

// MemStore is the file-mode Store: collections live in memory (loaded from
// the collection source file and upserted by the sync engine); provider
// facts and the latest + previous sync run are persisted to a local JSON
// cache file.
//
// Mode-specific semantics (see Store):
//
//   - Snapshots: only the latest and previous observation per video are
//     retained. History always returns ErrHistoryUnavailable.
//   - Rankings: PreviousRankings returns the positions recorded by the
//     RecordRankings call before the most recent one for a
//     (slug, strategy) pair; before that, an empty map with a nil error.
//
// Entries whose youtubeUrl has not been resolved to a video ID yet (the
// sync engine resolves IDs before upserting, see T09) still appear in
// ListVideos with best-effort data: empty ID, the URL as written in the
// collection file, title/description from overrides only, and no provider
// data. Such entries are not reachable via GetVideo.
//
// MemStore is safe for concurrent use: reads proceed under a shared lock
// while a sync run writes.
type MemStore struct {
	logger    *slog.Logger
	cachePath string

	mu          sync.RWMutex
	collections map[string]*Collection
	provider    map[string]ProviderVideo
	snapshots   map[string]*snapshotPair
	rankings    map[rankKey]*rankingPair
	lastSynced  map[string]time.Time
}

// snapshotPair keeps the two most recent metric observations for a video.
type snapshotPair struct {
	Latest   *Snapshot
	Previous *Snapshot
}

type rankKey struct {
	Slug     string
	Strategy string
}

// rankingPair keeps the two most recent position maps for a
// (collection, strategy) pair.
type rankingPair struct {
	Latest   map[string]int
	Previous map[string]int
}

var _ Store = (*MemStore)(nil)

// NewMemStore creates a file-mode store. When opts.CachePath names an
// existing cache file, its contents are loaded; a corrupt, unreadable, or
// version-mismatched cache is discarded with a logged warning, never
// fatally — the sync engine simply refetches.
func NewMemStore(opts MemStoreOptions) *MemStore {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &MemStore{
		logger:      logger,
		cachePath:   opts.CachePath,
		collections: map[string]*Collection{},
		provider:    map[string]ProviderVideo{},
		snapshots:   map[string]*snapshotPair{},
		rankings:    map[rankKey]*rankingPair{},
		lastSynced:  map[string]time.Time{},
	}
	if s.cachePath != "" {
		if cf := loadCacheFile(s.cachePath, logger); cf != nil {
			s.applyCache(cf)
		}
	}
	return s
}

// UpsertCollection creates or replaces a collection's editorial content.
// The collection is deep-copied, so later caller mutations are invisible
// to the store. Unpublished entries are stored (they keep their editorial
// position for future re-publishing) but excluded from ListVideos.
func (s *MemStore) UpsertCollection(_ context.Context, c *Collection) error {
	if c == nil || c.Slug == "" {
		return fmt.Errorf("upsert collection: slug is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.collections[c.Slug] = cloneCollection(c)
	return nil
}

// ListCollections returns all collections ordered by slug.
func (s *MemStore) ListCollections(_ context.Context) ([]*CollectionInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	slugs := slices.Sorted(maps.Keys(s.collections))
	infos := make([]*CollectionInfo, 0, len(slugs))
	for _, slug := range slugs {
		infos = append(infos, s.collectionInfoLocked(slug))
	}
	return infos, nil
}

// GetCollection returns one collection by slug, or ErrNotFound.
func (s *MemStore) GetCollection(_ context.Context, slug string) (*CollectionInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.collections[slug]; !ok {
		return nil, ErrNotFound
	}
	return s.collectionInfoLocked(slug), nil
}

// ListVideos returns the collection's published videos in editorial order
// with provider data merged when available.
func (s *MemStore) ListVideos(_ context.Context, slug string) ([]*Video, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.collections[slug]
	if !ok {
		return nil, ErrNotFound
	}
	videos := make([]*Video, 0, len(c.Videos))
	for i := range c.Videos {
		if !c.Videos[i].IsPublished() {
			continue
		}
		videos = append(videos, s.mergeVideoLocked(&c.Videos[i]))
	}
	return videos, nil
}

// GetVideo returns one video by YouTube ID across all collections, or
// ErrNotFound. Lookup is by direct ID, so unpublished entries are
// reachable; collections are scanned in slug order and the first matching
// entry supplies the editorial facts.
func (s *MemStore) GetVideo(_ context.Context, youtubeID string) (*Video, error) {
	if youtubeID == "" {
		return nil, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, slug := range slices.Sorted(maps.Keys(s.collections)) {
		c := s.collections[slug]
		for i := range c.Videos {
			if c.Videos[i].YouTubeID == youtubeID {
				return s.mergeVideoLocked(&c.Videos[i]), nil
			}
		}
	}
	return nil, ErrNotFound
}

// UpsertProviderData stores refreshed provider facts. Entries with an
// empty ID are skipped (the sync engine resolves IDs before calling).
func (s *MemStore) UpsertProviderData(_ context.Context, videos []ProviderVideo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, pv := range videos {
		if pv.ID == "" {
			continue
		}
		s.provider[pv.ID] = pv
	}
	s.persistLocked()
	return nil
}

// RecordSnapshots retains at most the two most recent observations per
// video: the incoming snapshot becomes the latest, the prior latest
// becomes the previous.
func (s *MemStore) RecordSnapshots(_ context.Context, snaps []Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sn := range snaps {
		if sn.VideoID == "" {
			continue
		}
		cur := sn
		if pair, ok := s.snapshots[sn.VideoID]; ok {
			pair.Previous = pair.Latest
			pair.Latest = &cur
		} else {
			s.snapshots[sn.VideoID] = &snapshotPair{Latest: &cur}
		}
	}
	s.persistLocked()
	return nil
}

// History always returns ErrHistoryUnavailable: file mode retains no time
// series, only the latest and previous sync run.
func (s *MemStore) History(_ context.Context, _ string, _ time.Time) ([]Snapshot, error) {
	return nil, ErrHistoryUnavailable
}

// RecordRankings persists computed positions for a (slug, strategy) pair.
// The at time is stored semantics-free in file mode: "previous" always
// means the run before the most recent RecordRankings call.
func (s *MemStore) RecordRankings(_ context.Context, slug, strategy string, positions map[string]int, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := rankKey{Slug: slug, Strategy: strategy}
	cl := maps.Clone(positions)
	if pair, ok := s.rankings[key]; ok {
		pair.Previous = pair.Latest
		pair.Latest = cl
	} else {
		s.rankings[key] = &rankingPair{Latest: cl}
	}
	s.persistLocked()
	return nil
}

// PreviousRankings returns the positions recorded by the sync before the
// most recent one, or an empty map (nil error) when fewer than two runs
// have been recorded.
func (s *MemStore) PreviousRankings(_ context.Context, slug, strategy string) (map[string]int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pair, ok := s.rankings[rankKey{Slug: slug, Strategy: strategy}]
	if !ok || pair.Previous == nil {
		return map[string]int{}, nil
	}
	return maps.Clone(pair.Previous), nil
}

// SetLastSyncedAt records when a collection last completed a sync.
// ErrNotFound when the collection does not exist.
func (s *MemStore) SetLastSyncedAt(_ context.Context, slug string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.collections[slug]; !ok {
		return ErrNotFound
	}
	s.lastSynced[slug] = at
	s.persistLocked()
	return nil
}

// Close flushes the cache file. Safe to call multiple times. A nil error
// when no cache path is configured.
func (s *MemStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cachePath == "" {
		return nil
	}
	if err := writeCacheAtomic(s.cachePath, s.buildCacheLocked()); err != nil {
		return fmt.Errorf("flush cache %s: %w", s.cachePath, err)
	}
	return nil
}

// collectionInfoLocked builds serving metadata for a stored collection.
// VideoCount counts published (served) entries. Callers must hold at
// least a read lock, and the slug must exist.
func (s *MemStore) collectionInfoLocked(slug string) *CollectionInfo {
	c := s.collections[slug]
	info := &CollectionInfo{Collection: *cloneCollection(c)}
	for i := range c.Videos {
		if c.Videos[i].IsPublished() {
			info.VideoCount++
		}
	}
	if at, ok := s.lastSynced[slug]; ok {
		info.LastSyncedAt = &at
	}
	return info
}

// mergeVideoLocked builds the normalized Video for one entry: provider
// facts (when the entry's ID is resolved and synced) merged with editorial
// facts. titleOverride/descriptionOverride win over provider values when
// non-nil and non-empty. Callers must hold at least a read lock.
func (s *MemStore) mergeVideoLocked(e *VideoEntry) *Video {
	speakers := slices.Clone(e.Speakers)
	if speakers == nil {
		speakers = []Speaker{}
	}
	topics := slices.Clone(e.Topics)
	if topics == nil {
		topics = []string{}
	}
	v := &Video{
		ID:       e.YouTubeID,
		Provider: providerName,
		URL:      e.YouTubeURL, // best-effort for unresolved entries
		Editorial: Editorial{
			Speakers:      speakers,
			Topics:        topics,
			Track:         e.Track,
			Event:         clonePtr(e.Event),
			Organizations: slices.Clone(e.Organizations),
			Featured:      e.Featured,
			Notes:         clonePtr(e.Notes),
		},
	}
	if e.YouTubeID != "" {
		v.URL = youtubeWatchURL(e.YouTubeID)
		if pv, ok := s.provider[e.YouTubeID]; ok {
			v.Title = pv.Title
			v.Description = pv.Description
			v.ThumbnailURL = pv.ThumbnailURL
			v.Channel = Channel{ID: pv.ChannelID, Name: pv.ChannelName}
			if !pv.PublishedAt.IsZero() {
				pa := pv.PublishedAt
				v.PublishedAt = &pa
			}
			v.DurationSeconds = pv.DurationSeconds
			stats := pv.Stats
			v.Statistics = &stats
		}
	}
	if e.TitleOverride != nil && *e.TitleOverride != "" {
		v.Title = *e.TitleOverride
	}
	if e.DescriptionOverride != nil && *e.DescriptionOverride != "" {
		v.Description = *e.DescriptionOverride
	}
	return v
}

// cloneCollection deep-copies a collection so store state never aliases
// caller memory (and vice versa).
func cloneCollection(c *Collection) *Collection {
	cp := *c
	cp.Author = clonePtr(c.Author)
	cp.Source = clonePtr(c.Source)
	cp.Videos = make([]VideoEntry, len(c.Videos))
	for i, v := range c.Videos {
		cp.Videos[i] = cloneEntry(v)
	}
	return &cp
}

func cloneEntry(v VideoEntry) VideoEntry {
	v.TitleOverride = clonePtr(v.TitleOverride)
	v.DescriptionOverride = clonePtr(v.DescriptionOverride)
	v.Published = clonePtr(v.Published)
	v.Notes = clonePtr(v.Notes)
	v.Event = clonePtr(v.Event)
	v.Speakers = slices.Clone(v.Speakers)
	v.Topics = slices.Clone(v.Topics)
	v.Organizations = slices.Clone(v.Organizations)
	return v
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
