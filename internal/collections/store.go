package collections

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors shared by all Store implementations.
var (
	// ErrNotFound is returned when a collection or video does not exist.
	ErrNotFound = errors.New("not found")

	// ErrHistoryUnavailable is returned by history-dependent methods in
	// file mode, which only retains the previous sync run, not a time
	// series. API handlers map it to 501 Not Implemented.
	ErrHistoryUnavailable = errors.New("history requires database mode")
)

// Store is the persistence contract shared by file mode (in-memory +
// local cache file) and database mode (PostgreSQL). Both implementations
// must pass the shared conformance test suite.
//
// Mode-specific semantics:
//
//   - Snapshots: database mode appends every snapshot to an append-only
//     table. File mode retains only the latest and the previous sync's
//     values (enough to compute previousPosition) and returns
//     ErrHistoryUnavailable from History.
//
//   - Rankings: RecordRankings persists computed positions for a
//     (collection, strategy) pair at sync time. PreviousRankings returns
//     the positions recorded by the sync before the most recent one in
//     file mode, and the most recent positions strictly before a given
//     time in database mode. A nil/empty map (with nil error) means no
//     prior ranking exists; callers must leave PreviousPosition nil.
//
// Filtering (topic/track/speaker) and pagination are applied by the
// service layer on top of ListVideos, not by stores.
type Store interface {
	// UpsertCollection creates or replaces a collection's editorial
	// content from a parsed source file. Videos shared with other
	// collections are deduplicated by YouTube ID (database mode).
	// Editorial ordering of entries is preserved.
	UpsertCollection(ctx context.Context, c *Collection) error

	// ListCollections returns all collections, ordered by slug.
	ListCollections(ctx context.Context) ([]*CollectionInfo, error)

	// GetCollection returns one collection by slug, or ErrNotFound.
	GetCollection(ctx context.Context, slug string) (*CollectionInfo, error)

	// ListVideos returns a collection's published videos in editorial
	// order, with provider data merged when available. ErrNotFound when
	// the collection does not exist.
	ListVideos(ctx context.Context, slug string) ([]*Video, error)

	// GetVideo returns one video by YouTube ID across all collections,
	// or ErrNotFound. Unpublished entries are reachable by direct ID;
	// only listings filter them.
	GetVideo(ctx context.Context, youtubeID string) (*Video, error)

	// UpsertProviderData stores refreshed provider facts for videos.
	// Called by the sync engine after fetching from YouTube.
	UpsertProviderData(ctx context.Context, videos []ProviderVideo) error

	// RecordSnapshots persists metric observations from a sync run.
	RecordSnapshots(ctx context.Context, snaps []Snapshot) error

	// History returns time-ordered snapshots for a video since the given
	// time. File mode returns ErrHistoryUnavailable.
	History(ctx context.Context, youtubeID string, since time.Time) ([]Snapshot, error)

	// RecordRankings persists computed positions (videoID -> position)
	// for a collection and strategy at the given time.
	RecordRankings(ctx context.Context, slug, strategy string, positions map[string]int, at time.Time) error

	// PreviousRankings returns the positions recorded by the
	// RecordRankings call before the most recent one for this
	// (collection, strategy) pair, in both modes. A nil/empty map with
	// nil error means no prior ranking exists.
	PreviousRankings(ctx context.Context, slug, strategy string) (map[string]int, error)

	// SetLastSyncedAt records when a collection last completed a sync.
	// Unknown slug returns ErrNotFound.
	SetLastSyncedAt(ctx context.Context, slug string, at time.Time) error

	// Close releases underlying resources (cache file flush, DB pool).
	Close() error
}
