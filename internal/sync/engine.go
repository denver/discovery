// Package sync implements the single sync path shared by the CLI,
// POST /api/v1/sync, and the scheduler (ADR-001, decision 4): load and
// validate collection files, resolve entries to canonical YouTube IDs,
// dedupe, batch-fetch provider data, upsert into the Store, record metric
// snapshots, and record rankings for the non-windowed strategies.
//
// Partial failure philosophy: per-entry and per-video problems (an
// unresolvable URL, a url/id disagreement, a private or deleted video) are
// logged and reported in SyncResult.Failed, never fatal to the run. Fatal
// errors are limited to unreadable or invalid collection files, store
// failures, and the fetcher's batch-level error — and even then everything
// already fetched is persisted before the error is returned.
package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/denver/discovery/internal/collections"
	"github.com/denver/discovery/internal/rankings"
	"github.com/denver/discovery/internal/youtube"
)

// ErrSyncInProgress is returned by Run when another sync is already
// running. The API layer maps it to HTTP 429.
var ErrSyncInProgress = errors.New("sync already in progress")

// syncStrategies are the ranking strategies recorded at sync time.
// Windowed strategies (views_24h, ...) are computed at read time from
// stored snapshots and are never recorded here.
var syncStrategies = []string{"views", "likes", "comments", "engagement"}

// Fetcher retrieves provider data for a batch of video IDs. It matches
// *youtube.Client's Fetch signature: IDs absent from the provider (private,
// deleted) are returned in failed rather than aborting; a non-nil error
// means the batch run stopped, and videos fetched before the failure are
// still returned.
type Fetcher interface {
	Fetch(ctx context.Context, ids []string) (videos []collections.ProviderVideo, failed []string, err error)
}

// The real YouTube client must satisfy Fetcher.
var _ Fetcher = (*youtube.Client)(nil)

// Options configures an Engine.
type Options struct {
	// CollectionPaths are the collection source files to sync, in order.
	// At least one is required.
	CollectionPaths []string

	// Now is the clock used for StartedAt, snapshot CapturedAt, ranking
	// times, and lastSyncedAt. Defaults to time.Now; tests inject a fixed
	// clock. The engine never calls time.Now directly.
	Now func() time.Time

	// Logger receives per-entry warnings and run summaries. Defaults to
	// slog.Default().
	Logger *slog.Logger
}

// Engine performs one full synchronization per Run call. It is safe for
// concurrent use: overlapping Run calls never interleave — the second
// caller gets ErrSyncInProgress immediately.
type Engine struct {
	store    collections.Store
	fetcher  Fetcher
	registry *rankings.Registry
	paths    []string
	now      func() time.Time
	logger   *slog.Logger

	running atomic.Bool
}

// New returns an Engine. store, fetcher, and registry are required; see
// Options for defaults.
func New(store collections.Store, fetcher Fetcher, registry *rankings.Registry, opts Options) *Engine {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		store:    store,
		fetcher:  fetcher,
		registry: registry,
		paths:    opts.CollectionPaths,
		now:      now,
		logger:   logger,
	}
}

// Run performs one sync: load + validate every collection file, resolve
// entries to canonical IDs, upsert collections, fetch each unique video
// exactly once, upsert provider data, record snapshots, record rankings
// for the non-windowed strategies, and stamp lastSyncedAt.
//
// The returned SyncResult is non-nil whenever any work was attempted;
// Failed is always a non-nil slice for stable JSON encoding. On a fetcher
// batch-level error the partial result is returned together with the
// error, and rankings/lastSyncedAt are not updated.
func (e *Engine) Run(ctx context.Context) (*collections.SyncResult, error) {
	if !e.running.CompareAndSwap(false, true) {
		return nil, ErrSyncInProgress
	}
	defer e.running.Store(false)

	if len(e.paths) == 0 {
		return nil, errors.New("sync: no collection paths configured")
	}

	startedAt := e.now()
	result := &collections.SyncResult{StartedAt: startedAt, Failed: []string{}}
	failedSeen := map[string]bool{}
	addFailed := func(label string) {
		if label == "" || failedSeen[label] {
			return
		}
		failedSeen[label] = true
		result.Failed = append(result.Failed, label)
	}

	// Load, validate, and resolve every file before writing anything, so
	// one invalid file aborts the run without partial upserts.
	var loaded []*collections.Collection
	var ids []string // unique fetch IDs, insertion-ordered
	idSeen := map[string]bool{}
	for _, path := range e.paths {
		c, err := collections.LoadFile(path)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", path, err)
		}
		for i := range c.Videos {
			entry := &c.Videos[i]
			id, err := resolveEntry(entry)
			if err != nil {
				e.logger.Warn("sync: entry failed resolution",
					"collection", c.Slug,
					"youtubeUrl", entry.YouTubeURL,
					"youtubeId", entry.YouTubeID,
					"error", err)
				addFailed(entryLabel(entry))
				continue
			}
			entry.YouTubeID = id // key the store by the canonical ID
			if !idSeen[id] {
				idSeen[id] = true
				ids = append(ids, id)
			}
		}
		loaded = append(loaded, c)
	}

	for _, c := range loaded {
		if err := e.store.UpsertCollection(ctx, c); err != nil {
			return nil, fmt.Errorf("upsert collection %s: %w", c.Slug, err)
		}
	}

	// Fetch each unique video exactly once per run, across all collections.
	videos, fetchFailed, fetchErr := e.fetcher.Fetch(ctx, ids)
	result.Fetched = len(videos)
	for _, id := range fetchFailed {
		e.logger.Warn("sync: video not returned by provider", "youtubeId", id)
		addFailed(id)
	}

	// Persist whatever was fetched, even when the batch run stopped early.
	if len(videos) > 0 {
		if err := e.store.UpsertProviderData(ctx, videos); err != nil {
			return nil, fmt.Errorf("upsert provider data: %w", err)
		}
		snaps := make([]collections.Snapshot, 0, len(videos))
		for _, v := range videos {
			snaps = append(snaps, collections.Snapshot{
				VideoID:      v.ID,
				ViewCount:    v.Stats.ViewCount,
				LikeCount:    v.Stats.LikeCount,
				CommentCount: v.Stats.CommentCount,
				CapturedAt:   startedAt,
			})
		}
		if err := e.store.RecordSnapshots(ctx, snaps); err != nil {
			return nil, fmt.Errorf("record snapshots: %w", err)
		}
	}

	if fetchErr != nil {
		e.finish(result)
		return result, fmt.Errorf("fetch videos: %w", fetchErr)
	}

	for _, c := range loaded {
		if err := e.recordRankings(ctx, c.Slug, startedAt); err != nil {
			return nil, err
		}
		if err := e.store.SetLastSyncedAt(ctx, c.Slug, startedAt); err != nil {
			return nil, fmt.Errorf("set lastSyncedAt for %s: %w", c.Slug, err)
		}
	}

	e.finish(result)
	e.logger.Info("sync: run complete",
		"collections", len(loaded),
		"fetched", result.Fetched,
		"failed", len(result.Failed),
		"duration", result.Duration)
	return result, nil
}

// recordRankings computes and persists positions for every non-windowed
// strategy for one collection.
func (e *Engine) recordRankings(ctx context.Context, slug string, at time.Time) error {
	videos, err := e.store.ListVideos(ctx, slug)
	if err != nil {
		return fmt.Errorf("list videos for %s: %w", slug, err)
	}
	for _, name := range syncStrategies {
		ranker, err := e.registry.Get(name)
		if err != nil {
			return fmt.Errorf("rank %s: %w", slug, err)
		}
		ranked, err := rankings.Rank(videos, ranker, rankings.NoHistory{}, at)
		if err != nil {
			return fmt.Errorf("rank %s by %s: %w", slug, name, err)
		}
		positions := make(map[string]int, len(ranked))
		for _, r := range ranked {
			if r.Video.ID != "" { // unresolved entries have no stable key
				positions[r.Video.ID] = r.Position
			}
		}
		if err := e.store.RecordRankings(ctx, slug, name, positions, at); err != nil {
			return fmt.Errorf("record rankings for %s/%s: %w", slug, name, err)
		}
	}
	return nil
}

// finish stamps the run duration on the result.
func (e *Engine) finish(r *collections.SyncResult) {
	r.Duration = e.now().Sub(r.StartedAt)
	r.DurationS = r.Duration.Seconds()
}

// resolveEntry resolves one collection entry to its canonical video ID.
// When both youtubeUrl and youtubeId are set they must agree; a
// disagreement is a per-entry failure, not fatal to the run.
func resolveEntry(entry *collections.VideoEntry) (string, error) {
	switch {
	case entry.YouTubeURL != "" && entry.YouTubeID != "":
		id, err := youtube.ResolveID(entry.YouTubeURL)
		if err != nil {
			return "", err
		}
		if id != entry.YouTubeID {
			return "", fmt.Errorf("youtubeUrl resolves to %q but youtubeId is %q", id, entry.YouTubeID)
		}
		return id, nil
	case entry.YouTubeURL != "":
		return youtube.ResolveID(entry.YouTubeURL)
	case entry.YouTubeID != "":
		return youtube.ResolveID(entry.YouTubeID)
	default:
		// Validation rejects such entries; kept for defense in depth.
		return "", errors.New("entry has neither youtubeUrl nor youtubeId")
	}
}

// entryLabel is the identifier reported in SyncResult.Failed for an entry
// that failed resolution: the declared ID when one is known, otherwise the
// URL string as written.
func entryLabel(entry *collections.VideoEntry) string {
	if entry.YouTubeID != "" {
		return entry.YouTubeID
	}
	return entry.YouTubeURL
}
