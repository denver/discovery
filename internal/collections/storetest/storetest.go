// Package storetest is the Store conformance suite shared by every Store
// implementation (file-mode MemStore, database-mode postgres store). It
// exercises the documented semantics on the Store interface; anything a
// mode is allowed to vary (History availability) is checked leniently.
package storetest

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/denver/discovery/internal/collections"
)

// Fixture video IDs (11 characters, like real YouTube IDs).
const (
	vidA       = "AAAAAAAAAAA" // title override + rich editorial
	vidB       = "BBBBBBBBBBB" // plain entry, provider data wins
	vidC       = "CCCCCCCCCCC" // unpublished
	vidD       = "DDDDDDDDDDD" // empty-string overrides (must not win)
	vidMissing = "ZZZZZZZZZZZ"
)

var baseTime = time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)

// Run exercises every Store method against the documented contract.
// newStore must return a fresh, empty store each call; Run closes each
// store itself via t.Cleanup, so newStore should not.
//
// Contract points asserted here (both modes):
//   - ErrNotFound from GetCollection/ListVideos/GetVideo for unknown keys
//   - editorial ordering preserved by ListVideos
//   - unpublished entries stored but excluded from ListVideos;
//     CollectionInfo.VideoCount counts published entries only
//   - title/description overrides win only when non-nil and non-empty;
//     URL is the canonical watch URL; Provider is "youtube"
//   - PreviousRankings: empty map + nil error before two recorded runs,
//     then always the run before the most recent one
//   - History: either real time-ordered history (database mode) or
//     ErrHistoryUnavailable (file mode) is conformant
func Run(t *testing.T, newStore func(t *testing.T) collections.Store) {
	ctx := context.Background()

	open := func(t *testing.T) collections.Store {
		t.Helper()
		s := newStore(t)
		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		})
		return s
	}

	upsert := func(t *testing.T, s collections.Store, c *collections.Collection) {
		t.Helper()
		if err := s.UpsertCollection(ctx, c); err != nil {
			t.Fatalf("UpsertCollection(%s): %v", c.Slug, err)
		}
	}

	t.Run("NotFound", func(t *testing.T) {
		s := open(t)
		if _, err := s.GetCollection(ctx, "nope"); !errors.Is(err, collections.ErrNotFound) {
			t.Errorf("GetCollection(unknown) error = %v, want ErrNotFound", err)
		}
		if _, err := s.ListVideos(ctx, "nope"); !errors.Is(err, collections.ErrNotFound) {
			t.Errorf("ListVideos(unknown) error = %v, want ErrNotFound", err)
		}
		if _, err := s.GetVideo(ctx, vidMissing); !errors.Is(err, collections.ErrNotFound) {
			t.Errorf("GetVideo(unknown) error = %v, want ErrNotFound", err)
		}
	})

	t.Run("UpsertAndGetCollection", func(t *testing.T) {
		s := open(t)
		upsert(t, s, fixture())
		info, err := s.GetCollection(ctx, "go-talks")
		if err != nil {
			t.Fatalf("GetCollection: %v", err)
		}
		if info.Slug != "go-talks" || info.Title != "Go Talks" {
			t.Errorf("got slug=%q title=%q, want go-talks / Go Talks", info.Slug, info.Title)
		}
		if info.VideoCount != 3 {
			t.Errorf("VideoCount = %d, want 3 (published entries only)", info.VideoCount)
		}
		if info.LastSyncedAt != nil {
			t.Errorf("LastSyncedAt = %v, want nil before first sync", info.LastSyncedAt)
		}
	})

	t.Run("ListCollectionsOrderedBySlug", func(t *testing.T) {
		s := open(t)
		upsert(t, s, minimalCollection("zeta", vidA))
		upsert(t, s, minimalCollection("alpha", vidB))
		infos, err := s.ListCollections(ctx)
		if err != nil {
			t.Fatalf("ListCollections: %v", err)
		}
		if len(infos) != 2 || infos[0].Slug != "alpha" || infos[1].Slug != "zeta" {
			t.Errorf("got %s, want [alpha zeta]", slugsOf(infos))
		}
	})

	t.Run("EditorialOrderingAndPublishedFilter", func(t *testing.T) {
		s := open(t)
		upsert(t, s, fixture())
		videos, err := s.ListVideos(ctx, "go-talks")
		if err != nil {
			t.Fatalf("ListVideos: %v", err)
		}
		got := idsOf(videos)
		want := []string{vidA, vidB, vidD} // vidC unpublished, order preserved
		if !equalStrings(got, want) {
			t.Errorf("ListVideos IDs = %v, want %v", got, want)
		}
	})

	t.Run("OverrideMerging", func(t *testing.T) {
		s := open(t)
		upsert(t, s, fixture())

		// Before provider data: overrides only, no statistics.
		videos, err := s.ListVideos(ctx, "go-talks")
		if err != nil {
			t.Fatalf("ListVideos: %v", err)
		}
		a := findVideo(t, videos, vidA)
		if a.Title != "Curated Title A" {
			t.Errorf("pre-sync Title = %q, want override", a.Title)
		}
		if a.Statistics != nil {
			t.Errorf("pre-sync Statistics = %+v, want nil", a.Statistics)
		}

		if err := s.UpsertProviderData(ctx, providerVideos()); err != nil {
			t.Fatalf("UpsertProviderData: %v", err)
		}
		videos, err = s.ListVideos(ctx, "go-talks")
		if err != nil {
			t.Fatalf("ListVideos: %v", err)
		}

		a = findVideo(t, videos, vidA)
		if a.Title != "Curated Title A" {
			t.Errorf("A.Title = %q, want title override to win over provider", a.Title)
		}
		if a.Description != "Provider description A" {
			t.Errorf("A.Description = %q, want provider value (no override)", a.Description)
		}
		if a.Provider != "youtube" {
			t.Errorf("A.Provider = %q, want youtube", a.Provider)
		}
		if want := "https://www.youtube.com/watch?v=" + vidA; a.URL != want {
			t.Errorf("A.URL = %q, want %q", a.URL, want)
		}
		if a.Channel.Name != "GopherCon" || a.Channel.ID != "UCchannel-A" {
			t.Errorf("A.Channel = %+v, want provider channel", a.Channel)
		}
		if a.DurationSeconds != 1800 {
			t.Errorf("A.DurationSeconds = %d, want 1800", a.DurationSeconds)
		}
		if a.Statistics == nil || a.Statistics.ViewCount != 1000 {
			t.Errorf("A.Statistics = %+v, want ViewCount 1000", a.Statistics)
		}
		if a.PublishedAt == nil || !a.PublishedAt.Equal(baseTime.AddDate(0, -1, 0)) {
			t.Errorf("A.PublishedAt = %v, want provider publish time", a.PublishedAt)
		}
		if len(a.Editorial.Speakers) != 1 || a.Editorial.Speakers[0].Name != "Ada Lovelace" {
			t.Errorf("A.Editorial.Speakers = %+v, want Ada Lovelace", a.Editorial.Speakers)
		}
		if a.Editorial.Track != "engineering" || !a.Editorial.Featured {
			t.Errorf("A.Editorial track/featured = %q/%v, want engineering/true", a.Editorial.Track, a.Editorial.Featured)
		}

		b := findVideo(t, videos, vidB)
		if b.Title != "Provider Title B" {
			t.Errorf("B.Title = %q, want provider title (no override)", b.Title)
		}

		d := findVideo(t, videos, vidD)
		if d.Title != "Provider Title D" {
			t.Errorf("D.Title = %q, want provider title (empty override must not win)", d.Title)
		}
		if d.Description != "Provider description D" {
			t.Errorf("D.Description = %q, want provider description (empty override must not win)", d.Description)
		}
	})

	t.Run("GetVideo", func(t *testing.T) {
		s := open(t)
		upsert(t, s, fixture())
		if err := s.UpsertProviderData(ctx, providerVideos()); err != nil {
			t.Fatalf("UpsertProviderData: %v", err)
		}
		v, err := s.GetVideo(ctx, vidA)
		if err != nil {
			t.Fatalf("GetVideo(%s): %v", vidA, err)
		}
		if v.ID != vidA || v.Title != "Curated Title A" || v.Statistics == nil {
			t.Errorf("GetVideo = id %q title %q stats %v, want merged video", v.ID, v.Title, v.Statistics)
		}
	})

	t.Run("UpsertReplacesEditorialContent", func(t *testing.T) {
		s := open(t)
		upsert(t, s, fixture())
		replacement := minimalCollection("go-talks", vidB, vidA) // drops C and D, reorders
		replacement.Title = "Go Talks v2"
		upsert(t, s, replacement)

		videos, err := s.ListVideos(ctx, "go-talks")
		if err != nil {
			t.Fatalf("ListVideos: %v", err)
		}
		if got, want := idsOf(videos), []string{vidB, vidA}; !equalStrings(got, want) {
			t.Errorf("after replace, IDs = %v, want %v", got, want)
		}
		info, err := s.GetCollection(ctx, "go-talks")
		if err != nil {
			t.Fatalf("GetCollection: %v", err)
		}
		if info.Title != "Go Talks v2" || info.VideoCount != 2 {
			t.Errorf("after replace, title=%q count=%d, want Go Talks v2 / 2", info.Title, info.VideoCount)
		}
	})

	t.Run("SnapshotsAndHistory", func(t *testing.T) {
		s := open(t)
		upsert(t, s, fixture())
		snap := func(views int64, at time.Time) collections.Snapshot {
			return collections.Snapshot{VideoID: vidA, ViewCount: views, LikeCount: views / 10, CapturedAt: at}
		}
		if err := s.RecordSnapshots(ctx, []collections.Snapshot{snap(100, baseTime)}); err != nil {
			t.Fatalf("RecordSnapshots(1): %v", err)
		}
		if err := s.RecordSnapshots(ctx, []collections.Snapshot{snap(200, baseTime.Add(time.Hour))}); err != nil {
			t.Fatalf("RecordSnapshots(2): %v", err)
		}

		hist, err := s.History(ctx, vidA, baseTime.Add(-time.Hour))
		if errors.Is(err, collections.ErrHistoryUnavailable) {
			t.Log("History unavailable (file mode): conformant")
			return
		}
		if err != nil {
			t.Fatalf("History: %v", err)
		}
		if len(hist) < 2 {
			t.Fatalf("History returned %d snapshots, want >= 2", len(hist))
		}
		for i := 1; i < len(hist); i++ {
			if hist[i].CapturedAt.Before(hist[i-1].CapturedAt) {
				t.Errorf("History not time-ordered at index %d", i)
			}
		}
		if last := hist[len(hist)-1]; last.ViewCount != 200 {
			t.Errorf("latest snapshot ViewCount = %d, want 200", last.ViewCount)
		}
	})

	t.Run("PreviousRankingsAcrossThreeSyncs", func(t *testing.T) {
		s := open(t)
		upsert(t, s, fixture())

		assertPrev := func(step string, want map[string]int) {
			t.Helper()
			got, err := s.PreviousRankings(ctx, "go-talks", "views")
			if err != nil {
				t.Fatalf("%s: PreviousRankings: %v", step, err)
			}
			if len(want) == 0 {
				if len(got) != 0 {
					t.Errorf("%s: PreviousRankings = %v, want empty", step, got)
				}
				return
			}
			if !maps.Equal(got, want) {
				t.Errorf("%s: PreviousRankings = %v, want %v", step, got, want)
			}
		}

		r1 := map[string]int{vidA: 1, vidB: 2, vidD: 3}
		r2 := map[string]int{vidB: 1, vidA: 2, vidD: 3}
		r3 := map[string]int{vidD: 1, vidA: 2, vidB: 3}

		assertPrev("before any run", nil)
		record := func(n int, pos map[string]int, at time.Time) {
			t.Helper()
			if err := s.RecordRankings(ctx, "go-talks", "views", pos, at); err != nil {
				t.Fatalf("RecordRankings(%d): %v", n, err)
			}
		}
		record(1, r1, baseTime)
		assertPrev("after run 1", nil) // first run: no prior ranking
		record(2, r2, baseTime.Add(time.Hour))
		assertPrev("after run 2", r1)
		record(3, r3, baseTime.Add(2*time.Hour))
		assertPrev("after run 3", r2)

		// Other strategies and collections are unaffected.
		if got, err := s.PreviousRankings(ctx, "go-talks", "likes"); err != nil || len(got) != 0 {
			t.Errorf("PreviousRankings(other strategy) = %v, %v; want empty, nil", got, err)
		}
		if got, err := s.PreviousRankings(ctx, "other-collection", "views"); err != nil || len(got) != 0 {
			t.Errorf("PreviousRankings(other collection) = %v, %v; want empty, nil", got, err)
		}
	})

	t.Run("LastSyncedAt", func(t *testing.T) {
		s := open(t)
		upsert(t, s, fixture())
		syncTime := baseTime.Add(30 * time.Minute)
		if err := s.SetLastSyncedAt(ctx, "go-talks", syncTime); err != nil {
			t.Fatalf("SetLastSyncedAt: %v", err)
		}
		info, err := s.GetCollection(ctx, "go-talks")
		if err != nil {
			t.Fatalf("GetCollection: %v", err)
		}
		if info.LastSyncedAt == nil || !info.LastSyncedAt.Equal(syncTime) {
			t.Errorf("LastSyncedAt = %v, want %v", info.LastSyncedAt, syncTime)
		}
	})
}

// fixture is the standard conformance collection: one rich entry, one
// plain entry, one unpublished entry, one entry with empty-string
// overrides.
func fixture() *collections.Collection {
	return &collections.Collection{
		SchemaVersion: "1.0",
		Slug:          "go-talks",
		Title:         "Go Talks",
		Description:   "Curated Go conference talks",
		Videos: []collections.VideoEntry{
			{
				YouTubeID:     vidA,
				TitleOverride: ptr("Curated Title A"),
				Speakers:      []collections.Speaker{{Name: "Ada Lovelace", Slug: "ada-lovelace"}},
				Topics:        []string{"go", "performance"},
				Track:         "engineering",
				Featured:      true,
			},
			{YouTubeID: vidB},
			{YouTubeID: vidC, Published: ptr(false)},
			{YouTubeID: vidD, TitleOverride: ptr(""), DescriptionOverride: ptr("")},
		},
	}
}

func minimalCollection(slug string, ids ...string) *collections.Collection {
	c := &collections.Collection{
		SchemaVersion: "1.0",
		Slug:          slug,
		Title:         "Collection " + slug,
	}
	for _, id := range ids {
		c.Videos = append(c.Videos, collections.VideoEntry{YouTubeID: id})
	}
	return c
}

func providerVideos() []collections.ProviderVideo {
	pv := func(id, suffix, channelID, channelName string, views int64, duration int) collections.ProviderVideo {
		return collections.ProviderVideo{
			ID:              id,
			Title:           "Provider Title " + suffix,
			Description:     "Provider description " + suffix,
			ThumbnailURL:    "https://i.ytimg.com/vi/" + id + "/hq720.jpg",
			ChannelID:       channelID,
			ChannelName:     channelName,
			PublishedAt:     baseTime.AddDate(0, -1, 0),
			DurationSeconds: duration,
			Stats: collections.Statistics{
				ViewCount:    views,
				LikeCount:    views / 10,
				CommentCount: views / 100,
				CapturedAt:   baseTime,
			},
		}
	}
	return []collections.ProviderVideo{
		pv(vidA, "A", "UCchannel-A", "GopherCon", 1000, 1800),
		pv(vidB, "B", "UCchannel-B", "Go Devroom", 2000, 2400),
		pv(vidD, "D", "UCchannel-D", "GopherCon", 500, 900),
	}
}

func findVideo(t *testing.T, videos []*collections.Video, id string) *collections.Video {
	t.Helper()
	for _, v := range videos {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("video %s not in ListVideos result %v", id, idsOf(videos))
	return nil
}

func idsOf(videos []*collections.Video) []string {
	ids := make([]string, len(videos))
	for i, v := range videos {
		ids[i] = v.ID
	}
	return ids
}

func slugsOf(infos []*collections.CollectionInfo) []string {
	slugs := make([]string, len(infos))
	for i, in := range infos {
		slugs[i] = in.Slug
	}
	return slugs
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func ptr[T any](v T) *T { return &v }
