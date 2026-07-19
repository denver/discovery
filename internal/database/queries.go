package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/denver/discovery/internal/collections"
)

// videoColumns is the shared select list for one videos + collection_videos
// join row, scanned by scanVideoRecord.
const videoColumns = `
	v.id, v.youtube_id, v.title, v.description, v.thumbnail_url,
	v.channel_id, v.channel_name, v.published_at, v.duration_seconds,
	v.view_count, v.like_count, v.comment_count, v.stats_captured_at,
	cv.title_override, cv.description_override, cv.track,
	cv.event_name, cv.event_year, cv.event_city, cv.event_venue,
	cv.featured, cv.published, cv.added_at, cv.notes`

// videoRecord is one videos row joined with one membership row.
type videoRecord struct {
	dbID            int64
	youtubeID       string
	title           string
	description     string
	thumbnailURL    string
	channelID       string
	channelName     string
	publishedAt     sql.NullTime
	durationSeconds int
	viewCount       sql.NullInt64
	likeCount       sql.NullInt64
	commentCount    sql.NullInt64
	statsCapturedAt sql.NullTime

	titleOverride       sql.NullString
	descriptionOverride sql.NullString
	track               sql.NullString
	eventName           sql.NullString
	eventYear           sql.NullInt64
	eventCity           sql.NullString
	eventVenue          sql.NullString
	featured            bool
	published           bool
	addedAt             sql.NullTime
	notes               sql.NullString
}

func scanVideoRecord(rows interface{ Scan(...any) error }) (videoRecord, error) {
	var r videoRecord
	err := rows.Scan(
		&r.dbID, &r.youtubeID, &r.title, &r.description, &r.thumbnailURL,
		&r.channelID, &r.channelName, &r.publishedAt, &r.durationSeconds,
		&r.viewCount, &r.likeCount, &r.commentCount, &r.statsCapturedAt,
		&r.titleOverride, &r.descriptionOverride, &r.track,
		&r.eventName, &r.eventYear, &r.eventCity, &r.eventVenue,
		&r.featured, &r.published, &r.addedAt, &r.notes,
	)
	return r, err
}

// editorialJoins holds batch-loaded speakers/topics/organizations keyed by
// videos.id, avoiding per-video queries.
type editorialJoins struct {
	speakers map[int64][]collections.Speaker
	topics   map[int64][]string
	orgs     map[int64][]string
}

// loadEditorialJoins batch-loads the three editorial joins for a set of
// video row IDs (three queries total, regardless of video count).
func (s *Store) loadEditorialJoins(ctx context.Context, videoIDs []int64) (*editorialJoins, error) {
	j := &editorialJoins{
		speakers: map[int64][]collections.Speaker{},
		topics:   map[int64][]string{},
		orgs:     map[int64][]string{},
	}
	if len(videoIDs) == 0 {
		return j, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT vs.video_id, sp.name, sp.slug
		FROM video_speakers vs JOIN speakers sp ON sp.id = vs.speaker_id
		WHERE vs.video_id = ANY($1)
		ORDER BY vs.video_id, vs.position, sp.slug`, videoIDs)
	if err != nil {
		return nil, fmt.Errorf("load speakers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var videoID int64
		var sp collections.Speaker
		if err := rows.Scan(&videoID, &sp.Name, &sp.Slug); err != nil {
			return nil, err
		}
		j.speakers[videoID] = append(j.speakers[videoID], sp)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT vt.video_id, t.slug
		FROM video_topics vt JOIN topics t ON t.id = vt.topic_id
		WHERE vt.video_id = ANY($1)
		ORDER BY vt.video_id, vt.position, t.slug`, videoIDs)
	if err != nil {
		return nil, fmt.Errorf("load topics: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var videoID int64
		var topic string
		if err := rows.Scan(&videoID, &topic); err != nil {
			return nil, err
		}
		j.topics[videoID] = append(j.topics[videoID], topic)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT vo.video_id, o.name
		FROM video_organizations vo JOIN organizations o ON o.id = vo.organization_id
		WHERE vo.video_id = ANY($1)
		ORDER BY vo.video_id, vo.position, o.name`, videoIDs)
	if err != nil {
		return nil, fmt.Errorf("load organizations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var videoID int64
		var org string
		if err := rows.Scan(&videoID, &org); err != nil {
			return nil, err
		}
		j.orgs[videoID] = append(j.orgs[videoID], org)
	}
	return j, rows.Err()
}

// mergeVideo builds the normalized Video from one record plus its joins,
// mirroring MemStore's merge: provider facts only when provider data
// exists (stats_captured_at set), overrides win when non-NULL and
// non-empty, canonical watch URL always.
func mergeVideo(r videoRecord, j *editorialJoins) *collections.Video {
	speakers := j.speakers[r.dbID]
	if speakers == nil {
		speakers = []collections.Speaker{}
	}
	topics := j.topics[r.dbID]
	if topics == nil {
		topics = []string{}
	}
	v := &collections.Video{
		ID:       r.youtubeID,
		Provider: "youtube",
		URL:      "https://www.youtube.com/watch?v=" + r.youtubeID,
		Editorial: collections.Editorial{
			Speakers:      speakers,
			Topics:        topics,
			Track:         strValue(r.track),
			Event:         eventOf(r),
			Organizations: j.orgs[r.dbID],
			Featured:      r.featured,
			Notes:         strPtr(r.notes),
		},
	}
	if r.statsCapturedAt.Valid { // provider data has arrived
		v.Title = r.title
		v.Description = r.description
		v.ThumbnailURL = r.thumbnailURL
		v.Channel = collections.Channel{ID: r.channelID, Name: r.channelName}
		if r.publishedAt.Valid {
			pa := r.publishedAt.Time
			v.PublishedAt = &pa
		}
		v.DurationSeconds = r.durationSeconds
		v.Statistics = &collections.Statistics{
			ViewCount:    r.viewCount.Int64,
			LikeCount:    r.likeCount.Int64,
			CommentCount: r.commentCount.Int64,
			CapturedAt:   r.statsCapturedAt.Time,
		}
	}
	if r.titleOverride.Valid && r.titleOverride.String != "" {
		v.Title = r.titleOverride.String
	}
	if r.descriptionOverride.Valid && r.descriptionOverride.String != "" {
		v.Description = r.descriptionOverride.String
	}
	return v
}

func eventOf(r videoRecord) *collections.Event {
	if !r.eventName.Valid && !r.eventYear.Valid && !r.eventCity.Valid && !r.eventVenue.Valid {
		return nil
	}
	return &collections.Event{
		Name:  strValue(r.eventName),
		Year:  int(r.eventYear.Int64),
		City:  strValue(r.eventCity),
		Venue: strValue(r.eventVenue),
	}
}

// ListVideos returns the collection's published videos in editorial order
// with provider data merged when available.
func (s *Store) ListVideos(ctx context.Context, slug string) ([]*collections.Video, error) {
	collectionID, err := s.collectionID(ctx, slug)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+videoColumns+`
		FROM collection_videos cv JOIN videos v ON v.id = cv.video_id
		WHERE cv.collection_id = $1 AND cv.published
		ORDER BY cv.position`, collectionID)
	if err != nil {
		return nil, fmt.Errorf("list videos %s: %w", slug, err)
	}
	defer rows.Close()

	var records []videoRecord
	var videoIDs []int64
	for rows.Next() {
		r, err := scanVideoRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, r)
		videoIDs = append(videoIDs, r.dbID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	joins, err := s.loadEditorialJoins(ctx, videoIDs)
	if err != nil {
		return nil, err
	}
	videos := make([]*collections.Video, 0, len(records))
	for _, r := range records {
		videos = append(videos, mergeVideo(r, joins))
	}
	return videos, nil
}

// GetVideo returns one video by YouTube ID across all collections, or
// ErrNotFound. Unpublished entries are reachable; the first membership in
// collection-slug order supplies the editorial facts (matching MemStore).
func (s *Store) GetVideo(ctx context.Context, youtubeID string) (*collections.Video, error) {
	if youtubeID == "" {
		return nil, collections.ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT `+videoColumns+`
		FROM videos v
		JOIN collection_videos cv ON cv.video_id = v.id
		JOIN collections c ON c.id = cv.collection_id
		WHERE v.youtube_id = $1
		ORDER BY c.slug, cv.position
		LIMIT 1`, youtubeID)
	r, err := scanVideoRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, collections.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get video %s: %w", youtubeID, err)
	}
	joins, err := s.loadEditorialJoins(ctx, []int64{r.dbID})
	if err != nil {
		return nil, err
	}
	return mergeVideo(r, joins), nil
}

// History returns time-ordered snapshots for a video since the given time
// (inclusive). An unknown or never-snapshotted video yields an empty
// slice.
func (s *Store) History(ctx context.Context, youtubeID string, since time.Time) ([]collections.Snapshot, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT v.youtube_id, sn.view_count, sn.like_count, sn.comment_count, sn.captured_at
		FROM video_snapshots sn JOIN videos v ON v.id = sn.video_id
		WHERE v.youtube_id = $1 AND sn.captured_at >= $2
		ORDER BY sn.captured_at, sn.id`, youtubeID, since)
	if err != nil {
		return nil, fmt.Errorf("history %s: %w", youtubeID, err)
	}
	defer rows.Close()
	var snaps []collections.Snapshot
	for rows.Next() {
		var sn collections.Snapshot
		if err := rows.Scan(&sn.VideoID, &sn.ViewCount, &sn.LikeCount, &sn.CommentCount, &sn.CapturedAt); err != nil {
			return nil, err
		}
		snaps = append(snaps, sn)
	}
	return snaps, rows.Err()
}

// PreviousRankings returns the positions from the RecordRankings run
// before the most recent one for (slug, strategy); runs are grouped by
// captured_at. Fewer than two runs (or an unknown slug) yields an empty
// map with a nil error, matching MemStore.
func (s *Store) PreviousRankings(ctx context.Context, slug, strategy string) (map[string]int, error) {
	prev := map[string]int{}
	var collectionID int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM collections WHERE slug = $1`, slug).Scan(&collectionID)
	if errors.Is(err, sql.ErrNoRows) {
		return prev, nil
	}
	if err != nil {
		return nil, fmt.Errorf("previous rankings %s: %w", slug, err)
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH runs AS (
			SELECT DISTINCT captured_at FROM rank_snapshots
			WHERE collection_id = $1 AND strategy = $2
			ORDER BY captured_at DESC
			LIMIT 2
		)
		SELECT v.youtube_id, r.position
		FROM rank_snapshots r JOIN videos v ON v.id = r.video_id
		WHERE r.collection_id = $1 AND r.strategy = $2
			AND (SELECT count(*) FROM runs) = 2
			AND r.captured_at = (SELECT min(captured_at) FROM runs)`,
		collectionID, strategy)
	if err != nil {
		return nil, fmt.Errorf("previous rankings %s/%s: %w", slug, strategy, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var position int
		if err := rows.Scan(&id, &position); err != nil {
			return nil, err
		}
		prev[id] = position
	}
	return prev, rows.Err()
}

// collectionID resolves a slug, or ErrNotFound.
func (s *Store) collectionID(ctx context.Context, slug string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM collections WHERE slug = $1`, slug).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, collections.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("resolve collection %s: %w", slug, err)
	}
	return id, nil
}
