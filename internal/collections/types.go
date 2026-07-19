package collections

import "time"

// SupportedSchemaVersions lists collection file schema versions this build
// can parse.
var SupportedSchemaVersions = []string{"1.0"}

// Collection is the parsed form of a collection source file. It contains
// editorial facts only; provider facts (titles, stats) are resolved from
// YouTube at sync time.
type Collection struct {
	SchemaVersion   string       `json:"schemaVersion"`
	Slug            string       `json:"slug"`
	Title           string       `json:"title"`
	Description     string       `json:"description,omitempty"`
	Author          *Author      `json:"author,omitempty"`
	Source          *Source      `json:"source,omitempty"`
	RefreshInterval string       `json:"refreshInterval,omitempty"`
	DefaultRanking  string       `json:"defaultRanking,omitempty"`
	Videos          []VideoEntry `json:"videos"`
}

// Author identifies the curator of a collection.
type Author struct {
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
}

// Source describes where a collection comes from.
type Source struct {
	Type     string `json:"type,omitempty"`
	Homepage string `json:"homepage,omitempty"`
}

// VideoEntry is one curated video in a collection file. Exactly one of
// YouTubeURL or YouTubeID must be set (both is allowed if consistent).
type VideoEntry struct {
	YouTubeURL          string    `json:"youtubeUrl,omitempty"`
	YouTubeID           string    `json:"youtubeId,omitempty"`
	TitleOverride       *string   `json:"titleOverride,omitempty"`
	DescriptionOverride *string   `json:"descriptionOverride,omitempty"`
	Speakers            []Speaker `json:"speakers,omitempty"`
	Event               *Event    `json:"event,omitempty"`
	Track               string    `json:"track,omitempty"`
	Topics              []string  `json:"topics,omitempty"`
	Organizations       []string  `json:"organizations,omitempty"`
	Featured            bool      `json:"featured,omitempty"`
	Published           *bool     `json:"published,omitempty"`
	AddedAt             string    `json:"addedAt,omitempty"`
	Notes               *string   `json:"notes,omitempty"`
}

// Speaker is a person appearing in a video.
type Speaker struct {
	Name string `json:"name"`
	Slug string `json:"slug,omitempty"`
}

// Event is the event a talk was given at.
type Event struct {
	Name  string `json:"name,omitempty"`
	Year  int    `json:"year,omitempty"`
	City  string `json:"city,omitempty"`
	Venue string `json:"venue,omitempty"`
}

// IsPublished reports whether the entry should be served. Published
// defaults to true when omitted.
func (v *VideoEntry) IsPublished() bool {
	return v.Published == nil || *v.Published
}

// AddedAtTime returns the parsed addedAt timestamp. ok is false when the
// field is empty. Call after validation; invalid values also return ok=false.
func (v *VideoEntry) AddedAtTime() (t time.Time, ok bool) {
	if v.AddedAt == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, v.AddedAt)
	return t, err == nil
}

// RefreshIntervalDuration returns the parsed refresh interval. ok is false
// when the field is empty or invalid.
func (c *Collection) RefreshIntervalDuration() (d time.Duration, ok bool) {
	if c.RefreshInterval == "" {
		return 0, false
	}
	d, err := time.ParseDuration(c.RefreshInterval)
	return d, err == nil
}
