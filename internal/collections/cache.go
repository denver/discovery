package collections

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// cacheFormatVersion identifies the cache file layout. Bump it on any
// incompatible change; mismatched caches are discarded (and refetched by
// the next sync), never migrated.
const cacheFormatVersion = 1

// cacheFile is the on-disk layout of the file-mode cache. It stores
// provider facts and sync-run state only; editorial content always comes
// from the collection source file, never from the cache.
type cacheFile struct {
	Version    int                       `json:"version"`
	SavedAt    time.Time                 `json:"savedAt"`
	Provider   map[string]ProviderVideo  `json:"provider,omitempty"`
	Snapshots  map[string]cacheSnapshots `json:"snapshots,omitempty"`
	Rankings   []cacheRankings           `json:"rankings,omitempty"`
	LastSynced map[string]time.Time      `json:"lastSynced,omitempty"`
}

// cacheSnapshots holds the latest and previous metric observation for one
// video (all file mode retains).
type cacheSnapshots struct {
	Latest   *Snapshot `json:"latest,omitempty"`
	Previous *Snapshot `json:"previous,omitempty"`
}

// cacheRankings holds the latest and previous position maps for one
// (collection, strategy) pair.
type cacheRankings struct {
	Slug     string         `json:"slug"`
	Strategy string         `json:"strategy"`
	Latest   map[string]int `json:"latest,omitempty"`
	Previous map[string]int `json:"previous,omitempty"`
}

// loadCacheFile reads and decodes a cache file. Any failure — unreadable
// file, invalid JSON, version mismatch — logs a warning and returns nil so
// the store starts empty; a missing file is silent (first run).
func loadCacheFile(path string, logger *slog.Logger) *cacheFile {
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			logger.Warn("discovery cache unreadable, starting empty", "path", path, "error", err)
		}
		return nil
	}
	var cf cacheFile
	if err := json.Unmarshal(data, &cf); err != nil {
		logger.Warn("discovery cache corrupt, discarding", "path", path, "error", err)
		return nil
	}
	if cf.Version != cacheFormatVersion {
		logger.Warn("discovery cache version mismatch, discarding",
			"path", path, "got", cf.Version, "want", cacheFormatVersion)
		return nil
	}
	return &cf
}

// applyCache loads decoded cache contents into the store's maps. Called
// from NewMemStore before the store is shared, so no locking is needed.
func (s *MemStore) applyCache(cf *cacheFile) {
	for id, pv := range cf.Provider {
		if id == "" {
			continue
		}
		pv.ID = id // the map key is authoritative
		s.provider[id] = pv
	}
	for id, pair := range cf.Snapshots {
		if id == "" || pair.Latest == nil {
			continue
		}
		s.snapshots[id] = &snapshotPair{Latest: pair.Latest, Previous: pair.Previous}
	}
	for _, r := range cf.Rankings {
		if r.Slug == "" || r.Strategy == "" || r.Latest == nil {
			continue
		}
		s.rankings[rankKey{Slug: r.Slug, Strategy: r.Strategy}] = &rankingPair{
			Latest:   r.Latest,
			Previous: r.Previous,
		}
	}
	for slug, at := range cf.LastSynced {
		s.lastSynced[slug] = at
	}
}

// buildCacheLocked assembles the on-disk form of the current state.
// Callers must hold at least a read lock. Rankings are sorted for stable
// file output.
func (s *MemStore) buildCacheLocked() *cacheFile {
	cf := &cacheFile{
		Version:    cacheFormatVersion,
		SavedAt:    time.Now().UTC(),
		Provider:   maps.Clone(s.provider),
		Snapshots:  make(map[string]cacheSnapshots, len(s.snapshots)),
		LastSynced: maps.Clone(s.lastSynced),
	}
	for id, pair := range s.snapshots {
		cf.Snapshots[id] = cacheSnapshots{Latest: pair.Latest, Previous: pair.Previous}
	}
	keys := slices.SortedFunc(maps.Keys(s.rankings), func(a, b rankKey) int {
		if c := strings.Compare(a.Slug, b.Slug); c != 0 {
			return c
		}
		return strings.Compare(a.Strategy, b.Strategy)
	})
	for _, k := range keys {
		pair := s.rankings[k]
		cf.Rankings = append(cf.Rankings, cacheRankings{
			Slug:     k.Slug,
			Strategy: k.Strategy,
			Latest:   pair.Latest,
			Previous: pair.Previous,
		})
	}
	return cf
}

// persistLocked writes the cache after a mutating operation. Cache writes
// are best-effort: a failure is logged, not returned, because the
// in-memory state is already updated and remains authoritative. Close
// surfaces write errors instead. Callers must hold the write lock.
func (s *MemStore) persistLocked() {
	if s.cachePath == "" {
		return
	}
	if err := writeCacheAtomic(s.cachePath, s.buildCacheLocked()); err != nil {
		s.logger.Warn("discovery cache write failed, serving from memory only",
			"path", s.cachePath, "error", err)
	}
}

// writeCacheAtomic writes the cache via a temp file in the same directory
// plus rename, so readers never observe a partially written cache.
func writeCacheAtomic(path string, cf *cacheFile) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cf); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
