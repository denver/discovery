// Package youtube resolves YouTube URLs to canonical video IDs and fetches
// video metadata from the YouTube Data API v3. Normalization is pure and
// never touches the network; the engine only ever fetches from the official
// API, never from arbitrary URLs.
package youtube

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

var (
	// ErrNotYouTube marks input that is neither a raw video ID nor an
	// http(s) URL on an allowed YouTube host.
	ErrNotYouTube = errors.New("not a YouTube URL or video ID")

	// ErrNoVideoID marks a YouTube URL from which no valid 11-character
	// video ID could be extracted.
	ErrNoVideoID = errors.New("no video ID in YouTube URL")
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// allowedHosts mirrors allowedYouTubeHosts in internal/collections; this
// package is the authoritative resolver behind that policy.
var allowedHosts = []string{
	"youtube.com", "www.youtube.com", "m.youtube.com",
	"music.youtube.com", "youtu.be",
}

// pathPrefixes are the youtube.com path forms that carry the video ID as
// the next path segment.
var pathPrefixes = []string{"shorts", "embed", "live", "v"}

// ResolveID resolves a raw 11-character video ID or any supported YouTube
// URL form (watch, youtu.be, shorts, embed, live, /v/) to the canonical
// video ID. It is pure: no network access, ever.
func ResolveID(urlOrID string) (string, error) {
	s := strings.TrimSpace(urlOrID)
	if s == "" {
		return "", fmt.Errorf("empty input: %w", ErrNotYouTube)
	}
	if idPattern.MatchString(s) {
		return s, nil
	}

	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("unparseable URL: %w", ErrNotYouTube)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("scheme %q is not http(s): %w", u.Scheme, ErrNotYouTube)
	}
	host := strings.ToLower(u.Hostname())
	if !slices.Contains(allowedHosts, host) {
		return "", fmt.Errorf("host %q is not YouTube: %w", host, ErrNotYouTube)
	}

	id, err := extractID(host, u)
	if err != nil {
		return "", err
	}
	if !idPattern.MatchString(id) {
		return "", fmt.Errorf("%q is not an 11-character video ID: %w", id, ErrNoVideoID)
	}
	return id, nil
}

// WatchURL returns the canonical watch URL for a video ID.
func WatchURL(id string) string {
	return "https://www.youtube.com/watch?v=" + url.QueryEscape(id)
}

func extractID(host string, u *url.URL) (string, error) {
	segs := pathSegments(u.Path)

	if host == "youtu.be" {
		if len(segs) == 0 {
			return "", fmt.Errorf("youtu.be URL has no path: %w", ErrNoVideoID)
		}
		return segs[0], nil
	}

	if len(segs) == 0 {
		return "", fmt.Errorf("URL has no path: %w", ErrNoVideoID)
	}
	switch {
	case segs[0] == "watch":
		id := u.Query().Get("v")
		if id == "" {
			return "", fmt.Errorf("watch URL missing v parameter: %w", ErrNoVideoID)
		}
		return id, nil
	case slices.Contains(pathPrefixes, segs[0]):
		if len(segs) < 2 {
			return "", fmt.Errorf("/%s/ URL missing video ID: %w", segs[0], ErrNoVideoID)
		}
		return segs[1], nil
	default:
		return "", fmt.Errorf("unrecognized YouTube path %q: %w", u.Path, ErrNoVideoID)
	}
}

func pathSegments(p string) []string {
	var segs []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	return segs
}
