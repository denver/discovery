package database

import (
	"context"
	"fmt"
	"time"

	"github.com/denver/discovery/internal/collections"
)

// Movers implements collections.MoverStore: the latest recorded positions
// for (slug, strategy) compared with each video's closest position at or
// before now-window, ordered by absolute change descending (YouTube ID as
// the tiebreak). Videos with no baseline in range — typically newly added
// — are omitted, as are videos no longer in the collection. limit <= 0
// means no limit. ErrNotFound on unknown slug.
func (s *Store) Movers(ctx context.Context, slug, strategy string, window time.Duration, limit int) ([]collections.Mover, error) {
	collectionID, err := s.collectionID(ctx, slug)
	if err != nil {
		return nil, err
	}
	var limitArg any // NULL = LIMIT ALL
	if limit > 0 {
		limitArg = limit
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH latest AS (
			SELECT video_id, position FROM rank_snapshots
			WHERE collection_id = $1 AND strategy = $2
				AND captured_at = (
					SELECT max(captured_at) FROM rank_snapshots
					WHERE collection_id = $1 AND strategy = $2
				)
		),
		baseline AS (
			SELECT DISTINCT ON (video_id) video_id, position
			FROM rank_snapshots
			WHERE collection_id = $1 AND strategy = $2 AND captured_at <= $3
			ORDER BY video_id, captured_at DESC
		)
		SELECT v.youtube_id, l.position, b.position
		FROM latest l
		JOIN baseline b ON b.video_id = l.video_id
		JOIN videos v ON v.id = l.video_id
		ORDER BY abs(b.position - l.position) DESC, v.youtube_id
		LIMIT $4`,
		collectionID, strategy, time.Now().Add(-window), limitArg)
	if err != nil {
		return nil, fmt.Errorf("movers %s/%s: %w", slug, strategy, err)
	}
	defer rows.Close()

	var movers []collections.Mover
	byID := map[string]int{} // youtube_id -> index in movers
	for rows.Next() {
		var id string
		var position, previous int
		if err := rows.Scan(&id, &position, &previous); err != nil {
			return nil, err
		}
		byID[id] = len(movers)
		movers = append(movers, collections.Mover{
			Position:         position,
			PreviousPosition: previous,
			Change:           previous - position, // positive = moved up
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(movers) == 0 {
		return movers, nil
	}

	// Attach the full normalized Video for each mover, with editorial
	// facts from this collection's membership. Videos since removed from
	// the collection have no membership and are dropped.
	videos, err := s.ListVideos(ctx, slug)
	if err != nil {
		return nil, err
	}
	for _, v := range videos {
		if i, ok := byID[v.ID]; ok {
			movers[i].Video = v
		}
	}
	kept := movers[:0]
	for _, m := range movers {
		if m.Video != nil {
			kept = append(kept, m)
		}
	}
	return kept, nil
}
