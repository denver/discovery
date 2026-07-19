package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testKey = "SECRET-API-KEY-123"

// fakeItem builds a videos.list item for id with deterministic fields.
func fakeItem(id string, maxresThumb bool) map[string]any {
	thumbs := map[string]any{
		"default": map[string]any{"url": "https://i.ytimg.com/" + id + "/default.jpg"},
	}
	if maxresThumb {
		thumbs["medium"] = map[string]any{"url": "https://i.ytimg.com/" + id + "/medium.jpg"}
		thumbs["high"] = map[string]any{"url": "https://i.ytimg.com/" + id + "/high.jpg"}
		thumbs["maxres"] = map[string]any{"url": "https://i.ytimg.com/" + id + "/maxres.jpg"}
	}
	return map[string]any{
		"id": id,
		"snippet": map[string]any{
			"title":        "Title " + id,
			"description":  "Description " + id,
			"channelId":    "chan-" + id,
			"channelTitle": "Channel " + id,
			"publishedAt":  "2026-06-01T12:00:00Z",
			"thumbnails":   thumbs,
		},
		"contentDetails": map[string]any{"duration": "PT30M52S"},
		"statistics": map[string]any{
			"viewCount":    "12345",
			"likeCount":    "678",
			"commentCount": "90",
		},
	}
}

// fakeServer serves videos.list for every requested ID except those in
// missing, recording the ID list of each request.
func fakeServer(t *testing.T, missing map[string]bool, requests *[][]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("key"); got != testKey {
			t.Errorf("request key = %q, want %q", got, testKey)
		}
		ids := strings.Split(r.URL.Query().Get("id"), ",")
		*requests = append(*requests, ids)
		var items []map[string]any
		for _, id := range ids {
			if !missing[id] {
				items = append(items, fakeItem(id, id == ids[0]))
			}
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"items": items}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
}

func newTestClient(baseURL string) *Client {
	return NewClient(testKey,
		WithBaseURL(baseURL),
		WithBackoffBase(time.Millisecond),
		WithHTTPClient(&http.Client{Timeout: 5 * time.Second}),
	)
}

func genIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("vid%08d", i) // 11 chars, valid ID shape
	}
	return ids
}

func TestFetchMultiBatchDedupes(t *testing.T) {
	var requests [][]string
	srv := fakeServer(t, nil, &requests)
	defer srv.Close()

	ids := genIDs(60)
	ids = append(ids, ids[0], ids[59], "") // duplicates and empties are dropped

	videos, failed, err := newTestClient(srv.URL).Fetch(context.Background(), ids)
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	if len(failed) != 0 {
		t.Fatalf("failed = %v, want none", failed)
	}
	if len(videos) != 60 {
		t.Fatalf("got %d videos, want 60", len(videos))
	}
	if len(requests) != 2 {
		t.Fatalf("got %d requests, want 2", len(requests))
	}
	if len(requests[0]) != 50 || len(requests[1]) != 10 {
		t.Fatalf("batch sizes = %d, %d, want 50, 10", len(requests[0]), len(requests[1]))
	}

	v := videos[0]
	if v.ID != "vid00000000" || v.Title != "Title vid00000000" {
		t.Errorf("unexpected first video: %+v", v)
	}
	if v.DurationSeconds != 30*60+52 {
		t.Errorf("DurationSeconds = %d, want 1852", v.DurationSeconds)
	}
	if want := "https://i.ytimg.com/vid00000000/maxres.jpg"; v.ThumbnailURL != want {
		t.Errorf("ThumbnailURL = %q, want maxres %q", v.ThumbnailURL, want)
	}
	if v.Stats.ViewCount != 12345 || v.Stats.LikeCount != 678 || v.Stats.CommentCount != 90 {
		t.Errorf("unexpected stats: %+v", v.Stats)
	}
	if v.Stats.CapturedAt.IsZero() {
		t.Error("CapturedAt is zero")
	}
	if v.PublishedAt.IsZero() {
		t.Error("PublishedAt is zero")
	}
	if v.ChannelID != "chan-vid00000000" || v.ChannelName != "Channel vid00000000" {
		t.Errorf("unexpected channel: %q %q", v.ChannelID, v.ChannelName)
	}

	// Second video in a batch only has a default thumbnail.
	if want := "https://i.ytimg.com/vid00000001/default.jpg"; videos[1].ThumbnailURL != want {
		t.Errorf("fallback ThumbnailURL = %q, want %q", videos[1].ThumbnailURL, want)
	}
}

func TestFetchReportsMissingVideos(t *testing.T) {
	var requests [][]string
	srv := fakeServer(t, map[string]bool{"vid00000001": true}, &requests)
	defer srv.Close()

	videos, failed, err := newTestClient(srv.URL).Fetch(context.Background(), genIDs(3))
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	if len(videos) != 2 {
		t.Fatalf("got %d videos, want 2", len(videos))
	}
	if len(failed) != 1 || failed[0] != "vid00000001" {
		t.Fatalf("failed = %v, want [vid00000001]", failed)
	}
}

func TestFetchRetriesTransientErrors(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusTooManyRequests} {
		t.Run(fmt.Sprintf("status %d", status), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					w.WriteHeader(status)
					return
				}
				items := []map[string]any{fakeItem("vid00000000", false)}
				_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
			}))
			defer srv.Close()

			videos, failed, err := newTestClient(srv.URL).Fetch(context.Background(), []string{"vid00000000"})
			if err != nil {
				t.Fatalf("Fetch error: %v", err)
			}
			if calls != 2 {
				t.Fatalf("calls = %d, want 2 (one failure, one retry)", calls)
			}
			if len(videos) != 1 || len(failed) != 0 {
				t.Fatalf("videos = %d, failed = %v", len(videos), failed)
			}
		})
	}
}

func TestFetchGivesUpAfterMaxAttempts(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, _, err := newTestClient(srv.URL).Fetch(context.Background(), []string{"vid00000000"})
	if err == nil {
		t.Fatal("Fetch succeeded, want error")
	}
	if calls != 4 {
		t.Fatalf("calls = %d, want 4 (maxAttempts)", calls)
	}
}

func TestFetchQuotaError(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, `{"error":{"message":"quota exceeded for key %s","errors":[{"reason":"quotaExceeded"}]}}`, testKey)
	}))
	defer srv.Close()

	_, _, err := newTestClient(srv.URL).Fetch(context.Background(), []string{"vid00000000"})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("err = %v, want ErrQuotaExceeded", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (403 must not be retried)", calls)
	}
	if strings.Contains(err.Error(), testKey) {
		t.Fatalf("error leaks API key: %v", err)
	}
	if !strings.Contains(err.Error(), "quotaExceeded") {
		t.Errorf("error missing API reason: %v", err)
	}
}

func TestAPIKeyNeverInNetworkErrors(t *testing.T) {
	// A server that is immediately closed forces a connection error; the
	// resulting *url.Error would normally embed the full request URL,
	// API key included.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	_, _, err := newTestClient(srv.URL).Fetch(context.Background(), []string{"vid00000000"})
	if err == nil {
		t.Fatal("Fetch succeeded against closed server, want error")
	}
	if strings.Contains(err.Error(), testKey) {
		t.Fatalf("error leaks API key: %v", err)
	}
	for unwrapped := err; unwrapped != nil; unwrapped = errors.Unwrap(unwrapped) {
		if strings.Contains(unwrapped.Error(), testKey) {
			t.Fatalf("wrapped error leaks API key: %v", unwrapped)
		}
	}
}

func TestFetchEmptyKey(t *testing.T) {
	if _, _, err := NewClient("").Fetch(context.Background(), []string{"vid00000000"}); err == nil {
		t.Fatal("Fetch with empty key succeeded, want error")
	}
}

func TestFetchContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError) // force retry loop
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := NewClient(testKey, WithBaseURL(srv.URL), WithBackoffBase(time.Hour)).
		Fetch(ctx, []string{"vid00000000"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if strings.Contains(err.Error(), testKey) {
		t.Fatalf("error leaks API key: %v", err)
	}
}

func TestParseISO8601Duration(t *testing.T) {
	valid := map[string]int{
		"PT30M52S":   1852,
		"PT1H2M3S":   3723,
		"PT45S":      45,
		"PT2H":       7200,
		"P1DT2H":     93600,
		"P2D":        172800,
		"PT0S":       0,
		"P0D":        0, // live/upcoming streams
		"PT1H0M0S":   3600,
		"PT100M":     6000,
		"P1DT1H1M1S": 90061,
	}
	for in, want := range valid {
		got, err := parseISO8601Duration(in)
		if err != nil {
			t.Errorf("parseISO8601Duration(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseISO8601Duration(%q) = %d, want %d", in, got, want)
		}
	}

	for _, in := range []string{"", "30m", "PT", "P", "1852", "PT1.5S", "-PT30S", "PT30M52"} {
		if got, err := parseISO8601Duration(in); err == nil {
			t.Errorf("parseISO8601Duration(%q) = %d, want error", in, got)
		}
	}
}
