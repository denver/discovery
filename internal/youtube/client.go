package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/denver/discovery/internal/collections"
)

const (
	defaultBaseURL = "https://www.googleapis.com/youtube/v3"
	batchSize      = 50 // videos.list maximum IDs per request
)

// ErrQuotaExceeded marks a 403 from the API: quota exhausted or key not
// authorized. Never retried.
var ErrQuotaExceeded = errors.New("youtube: quota exceeded or access forbidden")

// Client fetches video metadata from the YouTube Data API v3.
type Client struct {
	apiKey      string
	baseURL     string
	httpClient  *http.Client
	maxAttempts int
	backoffBase time.Duration
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API base URL (tests point this at a fake server).
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimSuffix(u, "/") }
}

// WithHTTPClient overrides the HTTP client (and with it the request timeout).
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// WithBackoffBase overrides the first retry delay. Delays double per attempt
// with up to one base of jitter added.
func WithBackoffBase(d time.Duration) Option {
	return func(c *Client) { c.backoffBase = d }
}

// NewClient returns a Data API v3 client. The key is sent only as a query
// parameter to the API host and is redacted from every error the client
// returns.
func NewClient(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:      apiKey,
		baseURL:     defaultBaseURL,
		httpClient:  &http.Client{Timeout: 15 * time.Second},
		maxAttempts: 4,
		backoffBase: 500 * time.Millisecond,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Fetch retrieves snippet, contentDetails, and statistics for the given
// video IDs in batches of 50, deduplicating the input. IDs absent from the
// API response (private, deleted) are returned in failed rather than
// aborting the run. A non-nil error means a request failed after retries
// and the run stopped; videos fetched by earlier batches are still returned.
func (c *Client) Fetch(ctx context.Context, ids []string) (videos []collections.ProviderVideo, failed []string, err error) {
	if c.apiKey == "" {
		return nil, nil, errors.New("youtube: API key is empty")
	}

	unique := dedupe(ids)
	found := make(map[string]bool, len(unique))

	for start := 0; start < len(unique); start += batchSize {
		batch := unique[start:min(start+batchSize, len(unique))]
		items, err := c.fetchBatch(ctx, batch)
		if err != nil {
			return videos, nil, err
		}
		for _, item := range items {
			found[item.ID] = true
			videos = append(videos, item.toProviderVideo(time.Now().UTC()))
		}
	}

	for _, id := range unique {
		if !found[id] {
			failed = append(failed, id)
		}
	}
	return videos, failed, nil
}

func (c *Client) fetchBatch(ctx context.Context, ids []string) ([]videoItem, error) {
	q := url.Values{
		"part":       {"snippet,contentDetails,statistics"},
		"id":         {strings.Join(ids, ",")},
		"maxResults": {strconv.Itoa(batchSize)},
		"key":        {c.apiKey},
	}
	reqURL := c.baseURL + "/videos?" + q.Encode()

	var lastErr error
	for attempt := 0; attempt < c.maxAttempts; attempt++ {
		if attempt > 0 {
			if err := sleepBackoff(ctx, c.backoffBase, attempt-1); err != nil {
				return nil, fmt.Errorf("youtube videos.list: %w", err)
			}
		}

		items, retryable, err := c.doRequest(ctx, reqURL)
		if err == nil {
			return items, nil
		}
		if !retryable {
			return nil, err
		}
		lastErr = err
	}
	return nil, fmt.Errorf("giving up after %d attempts: %w", c.maxAttempts, lastErr)
}

// doRequest performs one videos.list call. retryable reports whether the
// failure is transient (network error, 429, 5xx).
func (c *Client) doRequest(ctx context.Context, reqURL string) (items []videoItem, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, false, c.redactErr(err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Context ends are not transient; everything else here is a
		// network-level failure worth retrying.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, false, c.redactErr(err)
		}
		return nil, true, c.redactErr(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, true, c.redactErr(err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		var parsed videosListResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, false, fmt.Errorf("youtube videos.list: decode response: %w", err)
		}
		return parsed.Items, false, nil
	case resp.StatusCode == http.StatusForbidden:
		return nil, false, fmt.Errorf("%w (%s)", ErrQuotaExceeded, c.redact(apiErrorReason(body)))
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, true, fmt.Errorf("youtube videos.list: HTTP %d (%s)", resp.StatusCode, c.redact(apiErrorReason(body)))
	default:
		return nil, false, fmt.Errorf("youtube videos.list: HTTP %d (%s)", resp.StatusCode, c.redact(apiErrorReason(body)))
	}
}

// redactErr flattens an error to a string with the API key removed. URL
// errors from net/http embed the full request URL, key included, so the
// original error is never wrapped; context sentinels are preserved for
// errors.Is.
func (c *Client) redactErr(err error) error {
	msg := "youtube videos.list: " + c.redact(err.Error())
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("%s: %w", msg, context.Canceled)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s: %w", msg, context.DeadlineExceeded)
	default:
		return errors.New(msg)
	}
}

func (c *Client) redact(s string) string {
	if c.apiKey == "" {
		return s
	}
	s = strings.ReplaceAll(s, c.apiKey, "REDACTED")
	return strings.ReplaceAll(s, url.QueryEscape(c.apiKey), "REDACTED")
}

// sleepBackoff waits base * 2^attempt plus up to one base of jitter, or
// until ctx ends.
func sleepBackoff(ctx context.Context, base time.Duration, attempt int) error {
	delay := base<<attempt + rand.N(base)
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func dedupe(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// videosListResponse mirrors the subset of the videos.list response the
// engine consumes.
type videosListResponse struct {
	Items []videoItem `json:"items"`
}

type videoItem struct {
	ID      string `json:"id"`
	Snippet struct {
		Title        string               `json:"title"`
		Description  string               `json:"description"`
		ChannelID    string               `json:"channelId"`
		ChannelTitle string               `json:"channelTitle"`
		PublishedAt  string               `json:"publishedAt"`
		Thumbnails   map[string]thumbnail `json:"thumbnails"`
	} `json:"snippet"`
	ContentDetails struct {
		Duration string `json:"duration"`
	} `json:"contentDetails"`
	Statistics struct {
		ViewCount    string `json:"viewCount"`
		LikeCount    string `json:"likeCount"`
		CommentCount string `json:"commentCount"`
	} `json:"statistics"`
}

type thumbnail struct {
	URL string `json:"url"`
}

func (v videoItem) toProviderVideo(capturedAt time.Time) collections.ProviderVideo {
	publishedAt, _ := time.Parse(time.RFC3339, v.Snippet.PublishedAt)
	duration, _ := parseISO8601Duration(v.ContentDetails.Duration)
	return collections.ProviderVideo{
		ID:              v.ID,
		Title:           v.Snippet.Title,
		Description:     v.Snippet.Description,
		ThumbnailURL:    bestThumbnail(v.Snippet.Thumbnails),
		ChannelID:       v.Snippet.ChannelID,
		ChannelName:     v.Snippet.ChannelTitle,
		PublishedAt:     publishedAt,
		DurationSeconds: duration,
		Stats: collections.Statistics{
			ViewCount:    parseCount(v.Statistics.ViewCount),
			LikeCount:    parseCount(v.Statistics.LikeCount),
			CommentCount: parseCount(v.Statistics.CommentCount),
			CapturedAt:   capturedAt,
		},
	}
}

// thumbnailPreference is highest-resolution first, per the API's size tiers.
var thumbnailPreference = []string{"maxres", "standard", "high", "medium", "default"}

func bestThumbnail(thumbs map[string]thumbnail) string {
	for _, key := range thumbnailPreference {
		if t, ok := thumbs[key]; ok && t.URL != "" {
			return t.URL
		}
	}
	return ""
}

// parseCount parses the API's string-encoded counters. Absent counters
// (hidden likes, disabled comments) become 0.
func parseCount(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

var iso8601Duration = regexp.MustCompile(`^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// parseISO8601Duration converts the API's contentDetails.duration
// (e.g. "PT30M52S", "P1DT2H") to whole seconds.
func parseISO8601Duration(s string) (int, error) {
	m := iso8601Duration.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid ISO 8601 duration %q", s)
	}
	secs, components := 0, 0
	for i, mult := range []int{86400, 3600, 60, 1} {
		if m[i+1] == "" {
			continue
		}
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return 0, fmt.Errorf("invalid ISO 8601 duration %q", s)
		}
		secs += n * mult
		components++
	}
	if components == 0 {
		return 0, fmt.Errorf("invalid ISO 8601 duration %q", s)
	}
	return secs, nil
}

func apiErrorReason(body []byte) string {
	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Errors  []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil {
		if len(parsed.Error.Errors) > 0 && parsed.Error.Errors[0].Reason != "" {
			return parsed.Error.Errors[0].Reason
		}
		if parsed.Error.Message != "" {
			return parsed.Error.Message
		}
	}
	return "no error detail"
}
