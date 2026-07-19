package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/denver/discovery/internal/collections"
)

// GetCollection returns one collection by slug, or ErrNotFound.
func (s *Store) GetCollection(ctx context.Context, slug string) (*collections.CollectionInfo, error) {
	infos, err := s.collectionInfos(ctx, slug)
	if err != nil {
		return nil, err
	}
	if len(infos) == 0 {
		return nil, collections.ErrNotFound
	}
	return infos[0], nil
}

// ListCollections returns all collections ordered by slug.
func (s *Store) ListCollections(ctx context.Context) ([]*collections.CollectionInfo, error) {
	return s.collectionInfos(ctx, "")
}

// collectionInfos reconstructs CollectionInfo values — including the full
// editorial Videos list, so export round-trips — for one slug, or all
// collections when slug is empty. Memberships and editorial joins are
// batch-loaded (constant query count, no per-video queries).
func (s *Store) collectionInfos(ctx context.Context, slug string) ([]*collections.CollectionInfo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, slug, schema_version, title, description,
			author_name, author_url, source_type, source_homepage,
			refresh_interval, default_ranking, last_synced_at
		FROM collections
		WHERE $1 = '' OR slug = $1
		ORDER BY slug`, slug)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	defer rows.Close()

	var infos []*collections.CollectionInfo
	byID := map[int64]*collections.CollectionInfo{}
	var collectionIDs []int64
	for rows.Next() {
		var id int64
		var authorName, authorURL, sourceType, sourceHomepage, refreshInterval, defaultRanking sql.NullString
		var lastSyncedAt sql.NullTime
		info := &collections.CollectionInfo{}
		if err := rows.Scan(&id, &info.Slug, &info.SchemaVersion, &info.Title, &info.Description,
			&authorName, &authorURL, &sourceType, &sourceHomepage,
			&refreshInterval, &defaultRanking, &lastSyncedAt,
		); err != nil {
			return nil, err
		}
		if authorName.Valid || authorURL.Valid {
			info.Author = &collections.Author{Name: strValue(authorName), URL: strValue(authorURL)}
		}
		if sourceType.Valid || sourceHomepage.Valid {
			info.Source = &collections.Source{Type: strValue(sourceType), Homepage: strValue(sourceHomepage)}
		}
		info.RefreshInterval = strValue(refreshInterval)
		info.DefaultRanking = strValue(defaultRanking)
		if lastSyncedAt.Valid {
			at := lastSyncedAt.Time
			info.LastSyncedAt = &at
		}
		infos = append(infos, info)
		byID[id] = info
		collectionIDs = append(collectionIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(infos) == 0 {
		return infos, nil
	}

	// All memberships for these collections, in editorial order.
	memberRows, err := s.db.QueryContext(ctx, `
		SELECT cv.collection_id, v.id, v.youtube_id,
			cv.title_override, cv.description_override, cv.track,
			cv.event_name, cv.event_year, cv.event_city, cv.event_venue,
			cv.featured, cv.published, cv.added_at, cv.notes
		FROM collection_videos cv JOIN videos v ON v.id = cv.video_id
		WHERE cv.collection_id = ANY($1)
		ORDER BY cv.collection_id, cv.position`, collectionIDs)
	if err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	defer memberRows.Close()

	type member struct {
		collectionID int64
		videoID      int64
		entry        collections.VideoEntry
	}
	var members []member
	videoIDSet := map[int64]bool{}
	for memberRows.Next() {
		var m member
		var titleOverride, descriptionOverride, track, notes sql.NullString
		var eventName, eventCity, eventVenue sql.NullString
		var eventYear sql.NullInt64
		var featured, published bool
		var addedAt sql.NullTime
		if err := memberRows.Scan(&m.collectionID, &m.videoID, &m.entry.YouTubeID,
			&titleOverride, &descriptionOverride, &track,
			&eventName, &eventYear, &eventCity, &eventVenue,
			&featured, &published, &addedAt, &notes,
		); err != nil {
			return nil, err
		}
		m.entry.TitleOverride = strPtr(titleOverride)
		m.entry.DescriptionOverride = strPtr(descriptionOverride)
		m.entry.Track = strValue(track)
		if eventName.Valid || eventYear.Valid || eventCity.Valid || eventVenue.Valid {
			m.entry.Event = &collections.Event{
				Name:  strValue(eventName),
				Year:  int(eventYear.Int64),
				City:  strValue(eventCity),
				Venue: strValue(eventVenue),
			}
		}
		m.entry.Featured = featured
		if !published {
			f := false
			m.entry.Published = &f
		}
		if addedAt.Valid {
			m.entry.AddedAt = addedAt.Time.UTC().Format(time.RFC3339)
		}
		m.entry.Notes = strPtr(notes)
		members = append(members, m)
		videoIDSet[m.videoID] = true
	}
	if err := memberRows.Err(); err != nil {
		return nil, err
	}

	videoIDs := make([]int64, 0, len(videoIDSet))
	for id := range videoIDSet {
		videoIDs = append(videoIDs, id)
	}
	joins, err := s.loadEditorialJoins(ctx, videoIDs)
	if err != nil {
		return nil, err
	}

	for _, m := range members {
		info := byID[m.collectionID]
		e := m.entry
		if sp := joins.speakers[m.videoID]; len(sp) > 0 {
			e.Speakers = sp
		}
		if tp := joins.topics[m.videoID]; len(tp) > 0 {
			e.Topics = tp
		}
		if og := joins.orgs[m.videoID]; len(og) > 0 {
			e.Organizations = og
		}
		info.Videos = append(info.Videos, e)
		if e.IsPublished() {
			info.VideoCount++
		}
	}
	return infos, nil
}
