package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/denver/discovery/internal/collections"
)

// UpsertCollection creates or replaces a collection's editorial content in
// one transaction: the collection row is upserted by slug, videos are
// upserted by YouTube ID (shared across collections), membership rows are
// replaced (removed entries deleted, order rewritten via position — the
// deferrable UNIQUE (collection_id, position) lets reorders settle at
// commit), and the video's global speakers/topics/organizations are
// rewritten (last import wins). last_synced_at is preserved.
//
// Entries without a resolved YouTube ID are skipped (see package doc);
// duplicate IDs within one file keep the first occurrence.
func (s *Store) UpsertCollection(ctx context.Context, c *collections.Collection) error {
	if c == nil || c.Slug == "" {
		return fmt.Errorf("upsert collection: slug is required")
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var collectionID int64
		var authorName, authorURL, sourceType, sourceHomepage sql.NullString
		if c.Author != nil {
			authorName = sql.NullString{String: c.Author.Name, Valid: true}
			authorURL = sql.NullString{String: c.Author.URL, Valid: true}
		}
		if c.Source != nil {
			sourceType = sql.NullString{String: c.Source.Type, Valid: true}
			sourceHomepage = sql.NullString{String: c.Source.Homepage, Valid: true}
		}
		err := tx.QueryRowContext(ctx, `
			INSERT INTO collections (slug, schema_version, title, description,
				author_name, author_url, source_type, source_homepage,
				refresh_interval, default_ranking)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (slug) DO UPDATE SET
				schema_version   = EXCLUDED.schema_version,
				title            = EXCLUDED.title,
				description      = EXCLUDED.description,
				author_name      = EXCLUDED.author_name,
				author_url       = EXCLUDED.author_url,
				source_type      = EXCLUDED.source_type,
				source_homepage  = EXCLUDED.source_homepage,
				refresh_interval = EXCLUDED.refresh_interval,
				default_ranking  = EXCLUDED.default_ranking,
				updated_at       = now()
			RETURNING id`,
			c.Slug, c.SchemaVersion, c.Title, c.Description,
			authorName, authorURL, sourceType, sourceHomepage,
			nullString(c.RefreshInterval), nullString(c.DefaultRanking),
		).Scan(&collectionID)
		if err != nil {
			return fmt.Errorf("upsert collection %s: %w", c.Slug, err)
		}

		// Upsert videos and membership in editorial order. The dummy
		// DO UPDATE makes RETURNING id work on conflict too.
		videoIDs := make([]int64, 0, len(c.Videos))
		seen := make(map[string]bool, len(c.Videos))
		position := 0
		for i := range c.Videos {
			e := &c.Videos[i]
			if e.YouTubeID == "" || seen[e.YouTubeID] {
				continue
			}
			seen[e.YouTubeID] = true

			var videoID int64
			if err := tx.QueryRowContext(ctx, `
				INSERT INTO videos (youtube_id) VALUES ($1)
				ON CONFLICT (youtube_id) DO UPDATE SET youtube_id = EXCLUDED.youtube_id
				RETURNING id`, e.YouTubeID,
			).Scan(&videoID); err != nil {
				return fmt.Errorf("upsert video %s: %w", e.YouTubeID, err)
			}
			videoIDs = append(videoIDs, videoID)

			if err := upsertMembership(ctx, tx, collectionID, videoID, position, e); err != nil {
				return fmt.Errorf("upsert membership %s/%s: %w", c.Slug, e.YouTubeID, err)
			}
			if err := replaceVideoEditorial(ctx, tx, videoID, e); err != nil {
				return fmt.Errorf("upsert editorial %s/%s: %w", c.Slug, e.YouTubeID, err)
			}
			position++
		}

		// Drop memberships for entries no longer in the file. The videos
		// rows themselves remain (shared across collections; snapshots
		// keep their history).
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM collection_videos
			WHERE collection_id = $1 AND NOT (video_id = ANY($2))`,
			collectionID, videoIDs,
		); err != nil {
			return fmt.Errorf("prune memberships %s: %w", c.Slug, err)
		}
		return nil
	})
}

func upsertMembership(ctx context.Context, tx *sql.Tx, collectionID, videoID int64, position int, e *collections.VideoEntry) error {
	var eventName, eventCity, eventVenue sql.NullString
	var eventYear sql.NullInt64
	if e.Event != nil {
		eventName = sql.NullString{String: e.Event.Name, Valid: true}
		eventCity = sql.NullString{String: e.Event.City, Valid: true}
		eventVenue = sql.NullString{String: e.Event.Venue, Valid: true}
		eventYear = sql.NullInt64{Int64: int64(e.Event.Year), Valid: true}
	}
	var addedAt sql.NullTime
	if t, ok := e.AddedAtTime(); ok {
		addedAt = sql.NullTime{Time: t, Valid: true}
	}
	var titleOverride, descriptionOverride, notes sql.NullString
	if e.TitleOverride != nil {
		titleOverride = sql.NullString{String: *e.TitleOverride, Valid: true}
	}
	if e.DescriptionOverride != nil {
		descriptionOverride = sql.NullString{String: *e.DescriptionOverride, Valid: true}
	}
	if e.Notes != nil {
		notes = sql.NullString{String: *e.Notes, Valid: true}
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO collection_videos (collection_id, video_id, position,
			title_override, description_override, track,
			event_name, event_year, event_city, event_venue,
			featured, published, added_at, notes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (collection_id, video_id) DO UPDATE SET
			position             = EXCLUDED.position,
			title_override       = EXCLUDED.title_override,
			description_override = EXCLUDED.description_override,
			track                = EXCLUDED.track,
			event_name           = EXCLUDED.event_name,
			event_year           = EXCLUDED.event_year,
			event_city           = EXCLUDED.event_city,
			event_venue          = EXCLUDED.event_venue,
			featured             = EXCLUDED.featured,
			published            = EXCLUDED.published,
			added_at             = EXCLUDED.added_at,
			notes                = EXCLUDED.notes`,
		collectionID, videoID, position,
		titleOverride, descriptionOverride, nullString(e.Track),
		eventName, eventYear, eventCity, eventVenue,
		e.Featured, e.IsPublished(), addedAt, notes,
	)
	return err
}

// replaceVideoEditorial rewrites the video's global speaker/topic/
// organization joins from this entry. Global entities are upserted by
// slug/name; join rows carry position so per-video order round-trips.
func replaceVideoEditorial(ctx context.Context, tx *sql.Tx, videoID int64, e *collections.VideoEntry) error {
	for _, table := range []string{"video_speakers", "video_topics", "video_organizations"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE video_id = $1`, videoID); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	for i, sp := range e.Speakers {
		slug := sp.Slug
		if slug == "" {
			slug = slugify(sp.Name)
		}
		var speakerID int64
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO speakers (slug, name) VALUES ($1, $2)
			ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name
			RETURNING id`, slug, sp.Name,
		).Scan(&speakerID); err != nil {
			return fmt.Errorf("upsert speaker %s: %w", slug, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO video_speakers (video_id, speaker_id, position)
			VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
			videoID, speakerID, i,
		); err != nil {
			return fmt.Errorf("join speaker %s: %w", slug, err)
		}
	}
	for i, topic := range e.Topics {
		var topicID int64
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO topics (slug) VALUES ($1)
			ON CONFLICT (slug) DO UPDATE SET slug = EXCLUDED.slug
			RETURNING id`, topic,
		).Scan(&topicID); err != nil {
			return fmt.Errorf("upsert topic %s: %w", topic, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO video_topics (video_id, topic_id, position)
			VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
			videoID, topicID, i,
		); err != nil {
			return fmt.Errorf("join topic %s: %w", topic, err)
		}
	}
	for i, org := range e.Organizations {
		var orgID int64
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO organizations (name) VALUES ($1)
			ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
			RETURNING id`, org,
		).Scan(&orgID); err != nil {
			return fmt.Errorf("upsert organization %s: %w", org, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO video_organizations (video_id, organization_id, position)
			VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
			videoID, orgID, i,
		); err != nil {
			return fmt.Errorf("join organization %s: %w", org, err)
		}
	}
	return nil
}

// UpsertProviderData refreshes provider facts and the denormalized current
// stats on videos rows. Unknown or unresolved IDs are skipped: the sync
// engine only fetches IDs it has already upserted.
func (s *Store) UpsertProviderData(ctx context.Context, videos []collections.ProviderVideo) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, pv := range videos {
			if pv.ID == "" {
				continue
			}
			var publishedAt sql.NullTime
			if !pv.PublishedAt.IsZero() {
				publishedAt = sql.NullTime{Time: pv.PublishedAt, Valid: true}
			}
			capturedAt := pv.Stats.CapturedAt
			if capturedAt.IsZero() {
				capturedAt = time.Now().UTC()
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE videos SET
					title             = $2,
					description       = $3,
					thumbnail_url     = $4,
					channel_id        = $5,
					channel_name      = $6,
					published_at      = $7,
					duration_seconds  = $8,
					view_count        = $9,
					like_count        = $10,
					comment_count     = $11,
					stats_captured_at = $12,
					updated_at        = now()
				WHERE youtube_id = $1`,
				pv.ID, pv.Title, pv.Description, pv.ThumbnailURL,
				pv.ChannelID, pv.ChannelName, publishedAt, pv.DurationSeconds,
				pv.Stats.ViewCount, pv.Stats.LikeCount, pv.Stats.CommentCount, capturedAt,
			); err != nil {
				return fmt.Errorf("update provider data %s: %w", pv.ID, err)
			}
		}
		return nil
	})
}

// RecordSnapshots appends metric observations to video_snapshots. Rows are
// never updated (enforced by a trigger); snapshots for unknown video IDs
// are skipped.
func (s *Store) RecordSnapshots(ctx context.Context, snaps []collections.Snapshot) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, sn := range snaps {
			if sn.VideoID == "" {
				continue
			}
			capturedAt := sn.CapturedAt
			if capturedAt.IsZero() {
				capturedAt = time.Now().UTC()
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO video_snapshots (video_id, view_count, like_count, comment_count, captured_at)
				SELECT v.id, $2, $3, $4, $5 FROM videos v WHERE v.youtube_id = $1`,
				sn.VideoID, sn.ViewCount, sn.LikeCount, sn.CommentCount, capturedAt,
			); err != nil {
				return fmt.Errorf("record snapshot %s: %w", sn.VideoID, err)
			}
		}
		return nil
	})
}

// RecordRankings appends computed positions to rank_snapshots for a
// (collection, strategy) pair. ErrNotFound on unknown slug; positions for
// unknown video IDs are skipped.
func (s *Store) RecordRankings(ctx context.Context, slug, strategy string, positions map[string]int, at time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var collectionID int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM collections WHERE slug = $1`, slug).Scan(&collectionID)
		if errors.Is(err, sql.ErrNoRows) {
			return collections.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("record rankings %s: %w", slug, err)
		}
		if at.IsZero() {
			at = time.Now().UTC()
		}
		for videoID, position := range positions {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO rank_snapshots (collection_id, video_id, strategy, position, captured_at)
				SELECT $1, v.id, $3, $4, $5 FROM videos v WHERE v.youtube_id = $2`,
				collectionID, videoID, strategy, position, at,
			); err != nil {
				return fmt.Errorf("record ranking %s/%s: %w", slug, videoID, err)
			}
		}
		return nil
	})
}

// SetLastSyncedAt records when a collection last completed a sync.
// ErrNotFound on unknown slug.
func (s *Store) SetLastSyncedAt(ctx context.Context, slug string, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE collections SET last_synced_at = $2, updated_at = now() WHERE slug = $1`, slug, at)
	if err != nil {
		return fmt.Errorf("set last synced %s: %w", slug, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return collections.ErrNotFound
	}
	return nil
}

// inTx runs fn inside a transaction, committing on nil and rolling back on
// error.
func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// slugify derives a URL-safe slug for speakers declared without one:
// lowercase, non-alphanumeric runs collapsed to single hyphens.
func slugify(name string) string {
	var b strings.Builder
	lastHyphen := true // trims leading hyphens
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		case !lastHyphen:
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	slug := strings.TrimSuffix(b.String(), "-")
	if slug == "" {
		return name // degenerate input; keep something unique-ish
	}
	return slug
}
