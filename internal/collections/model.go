package collections

import "time"

// Video is the normalized API model: provider facts resolved from YouTube
// merged with editorial facts from the collection file. This is the shape
// returned by the REST API and consumed by the web UI.
type Video struct {
	ID              string      `json:"id"`
	Provider        string      `json:"provider"` // always "youtube" in MVP
	URL             string      `json:"url"`
	Title           string      `json:"title"` // titleOverride applied when set
	Description     string      `json:"description"`
	ThumbnailURL    string      `json:"thumbnailUrl"`
	Channel         Channel     `json:"channel"`
	PublishedAt     *time.Time  `json:"publishedAt,omitempty"`
	DurationSeconds int         `json:"durationSeconds"`
	Statistics      *Statistics `json:"statistics,omitempty"` // nil until first successful sync
	Editorial       Editorial   `json:"editorial"`
	Ranking         *Ranking    `json:"ranking,omitempty"` // set on ranking responses
}

// Channel identifies the YouTube channel a video belongs to.
type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Statistics are point-in-time popularity counters from YouTube.
type Statistics struct {
	ViewCount    int64     `json:"viewCount"`
	LikeCount    int64     `json:"likeCount"`
	CommentCount int64     `json:"commentCount"`
	CapturedAt   time.Time `json:"capturedAt"`
}

// Editorial carries curator-supplied metadata from the collection file.
type Editorial struct {
	Speakers      []Speaker `json:"speakers"`
	Topics        []string  `json:"topics"`
	Track         string    `json:"track,omitempty"`
	Event         *Event    `json:"event,omitempty"`
	Organizations []string  `json:"organizations,omitempty"`
	Featured      bool      `json:"featured,omitempty"`
	Notes         *string   `json:"notes,omitempty"`
}

// Ranking is a video's position under a specific strategy.
// PreviousPosition and Change are nil when no prior ranking exists (first
// sync, or a video newly added to the collection).
type Ranking struct {
	Position         int     `json:"position"`
	PreviousPosition *int    `json:"previousPosition,omitempty"`
	Change           *int    `json:"change,omitempty"`
	Score            float64 `json:"score"`
	Strategy         string  `json:"strategy"`
}

// ProviderVideo is what a provider client (YouTube) returns for one video.
// Stores merge it with editorial metadata to produce Video.
type ProviderVideo struct {
	ID              string
	Title           string
	Description     string
	ThumbnailURL    string
	ChannelID       string
	ChannelName     string
	PublishedAt     time.Time
	DurationSeconds int
	Stats           Statistics
}

// Snapshot is one append-only metrics observation for a video.
type Snapshot struct {
	VideoID      string    `json:"videoId"`
	ViewCount    int64     `json:"viewCount"`
	LikeCount    int64     `json:"likeCount"`
	CommentCount int64     `json:"commentCount"`
	CapturedAt   time.Time `json:"capturedAt"`
}

// CollectionInfo is a stored collection plus serving metadata.
type CollectionInfo struct {
	Collection
	VideoCount   int        `json:"videoCount"`
	LastSyncedAt *time.Time `json:"lastSyncedAt,omitempty"`
}

// SyncResult summarizes one synchronization run.
type SyncResult struct {
	StartedAt time.Time     `json:"startedAt"`
	Duration  time.Duration `json:"-"`
	DurationS float64       `json:"durationSeconds"`
	Fetched   int           `json:"fetched"`
	Failed    []string      `json:"failed"` // YouTube IDs that could not be fetched
}
