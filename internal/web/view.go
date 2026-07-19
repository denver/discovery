package web

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/denver/discovery/internal/collections"
)

// sortOptions are the strategies offered as tabs. Windowed strategies are
// reachable by URL but not advertised; in file mode they only produce the
// "needs database mode" notice.
var sortOptions = []struct{ name, label string }{
	{"views", "Views"},
	{"likes", "Likes"},
	{"comments", "Comments"},
	{"engagement", "Engagement"},
}

// navLink is one sort tab or filter chip.
type navLink struct {
	Label  string
	URL    string
	Active bool
}

// indexData feeds templates/index.html.
type indexData struct {
	Title       string
	Collections []collectionCard
}

// collectionCard is one row on the collections index.
type collectionCard struct {
	Slug        string
	Title       string
	Description string
	VideoCount  int
	LastSynced  string // relative, empty when never synced
}

// leaderboardData feeds templates/leaderboard.html.
type leaderboardData struct {
	Title         string
	Collection    *collections.CollectionInfo
	LastRefreshed string
	SortLinks     []navLink
	TrackLinks    []navLink
	TopicLinks    []navLink
	Cards         []videoCard
	Notice        string
}

// errorData feeds templates/error.html.
type errorData struct {
	Title   string
	Status  int
	Heading string
	Message string
}

// videoCard is one ranked entry, fully formatted for the template.
type videoCard struct {
	Rank         int
	HasMove      bool
	MoveClass    string // "up", "down", "same"
	MoveLabel    string // "↑3", "↓2", "—"
	Title        string
	URL          string
	ThumbnailURL string
	Speakers     string
	Channel      string
	Published    string
	HasStats     bool
	Views        string
	Likes        string
	Comments     string
	Featured     bool
}

// newVideoCard formats one ranked video for rendering.
func newVideoCard(v *collections.Video) videoCard {
	c := videoCard{
		Title:        v.Title,
		URL:          v.URL,
		ThumbnailURL: v.ThumbnailURL,
		Channel:      v.Channel.Name,
		Featured:     v.Editorial.Featured,
	}
	if c.Title == "" {
		c.Title = "Untitled video"
	}
	if len(v.Editorial.Speakers) > 0 {
		names := make([]string, len(v.Editorial.Speakers))
		for i, sp := range v.Editorial.Speakers {
			names[i] = sp.Name
		}
		c.Speakers = strings.Join(names, ", ")
	}
	if v.PublishedAt != nil {
		c.Published = v.PublishedAt.Format("Jan 2, 2006")
	}
	if v.Statistics != nil {
		c.HasStats = true
		c.Views = compactCount(v.Statistics.ViewCount)
		c.Likes = compactCount(v.Statistics.LikeCount)
		c.Comments = compactCount(v.Statistics.CommentCount)
	}
	if r := v.Ranking; r != nil {
		c.Rank = r.Position
		if r.Change != nil {
			c.HasMove = true
			switch ch := *r.Change; {
			case ch > 0:
				c.MoveClass, c.MoveLabel = "up", "↑"+strconv.Itoa(ch)
			case ch < 0:
				c.MoveClass, c.MoveLabel = "down", "↓"+strconv.Itoa(-ch)
			default:
				c.MoveClass, c.MoveLabel = "same", "—"
			}
		}
	}
	return c
}

// leaderboardURL builds /c/{slug} with only the non-empty query params,
// so switching one control preserves the others.
func leaderboardURL(slug, sortName, track, topic string) string {
	q := url.Values{}
	if sortName != "" {
		q.Set("sort", sortName)
	}
	if track != "" {
		q.Set("track", track)
	}
	if topic != "" {
		q.Set("topic", topic)
	}
	u := "/c/" + url.PathEscape(slug)
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	return u
}

// sortLinks builds the sort tabs. Each tab carries an explicit ?sort=
// while preserving the current track/topic; resolved is the strategy the
// service actually used and drives the active state.
func sortLinks(slug, resolved, track, topic string) []navLink {
	links := make([]navLink, 0, len(sortOptions))
	for _, o := range sortOptions {
		links = append(links, navLink{
			Label:  o.label,
			URL:    leaderboardURL(slug, o.name, track, topic),
			Active: o.name == resolved,
		})
	}
	return links
}

// filterLinks builds one filter chip row: an "All" chip plus one chip per
// value. Clicking the active value's chip keeps it (idempotent); "All"
// clears the dimension. build receives the candidate value ("" for All).
func filterLinks(values []string, current string, build func(value string) string) []navLink {
	if len(values) == 0 {
		return nil
	}
	links := make([]navLink, 0, len(values)+1)
	links = append(links, navLink{Label: "All", URL: build(""), Active: current == ""})
	for _, v := range values {
		links = append(links, navLink{
			Label:  v,
			URL:    build(v),
			Active: strings.EqualFold(v, current),
		})
	}
	return links
}

// distinctFilterValues extracts the sorted, case-insensitively distinct
// track and topic values present in a collection's videos. The first
// spelling seen wins.
func distinctFilterValues(videos []*collections.Video) (tracks, topics []string) {
	seenTrack := map[string]bool{}
	seenTopic := map[string]bool{}
	for _, v := range videos {
		if t := v.Editorial.Track; t != "" && !seenTrack[strings.ToLower(t)] {
			seenTrack[strings.ToLower(t)] = true
			tracks = append(tracks, t)
		}
		for _, t := range v.Editorial.Topics {
			if t != "" && !seenTopic[strings.ToLower(t)] {
				seenTopic[strings.ToLower(t)] = true
				topics = append(topics, t)
			}
		}
	}
	sort.Strings(tracks)
	sort.Strings(topics)
	return tracks, topics
}

// lastSyncedLabel renders a collection's last sync time relative to now,
// or "" when it has never synced.
func lastSyncedLabel(at *time.Time, now time.Time) string {
	if at == nil {
		return ""
	}
	return relTime(*at, now)
}
