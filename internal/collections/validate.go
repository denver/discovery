package collections

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ValidationError describes one problem in a collection file, anchored to an
// exact field path such as "videos[3].youtubeUrl".
type ValidationError struct {
	Path    string
	Message string
}

func (e ValidationError) Error() string {
	return e.Path + ": " + e.Message
}

// ValidationErrors aggregates every problem found in one pass.
type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string {
	msgs := make([]string, len(e))
	for i, ve := range e {
		msgs[i] = ve.Error()
	}
	return fmt.Sprintf("collection invalid (%d problems):\n  %s", len(e), strings.Join(msgs, "\n  "))
}

var (
	slugPattern      = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	youtubeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

	// allowedYouTubeHosts are the only hosts a youtubeUrl may use. The
	// engine never fetches arbitrary URLs.
	allowedYouTubeHosts = []string{
		"youtube.com", "www.youtube.com", "m.youtube.com",
		"music.youtube.com", "youtu.be",
	}
)

// LoadFile reads, parses, and validates a collection file. JSON is the
// canonical format; .yaml/.yml files are converted to JSON before decoding
// so both formats share one code path.
func LoadFile(path string) (*Collection, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read collection: %w", err)
	}
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".json":
		return Parse(data)
	case ".yaml", ".yml":
		jsonData, err := yamlToJSON(data)
		if err != nil {
			return nil, fmt.Errorf("parse YAML: %w", err)
		}
		return Parse(jsonData)
	default:
		return nil, fmt.Errorf("unsupported collection file extension %q (use .json, .yaml, or .yml)", ext)
	}
}

// Parse decodes JSON bytes into a Collection and validates it.
func Parse(data []byte) (*Collection, error) {
	var c Collection
	if err := json.Unmarshal(data, &c); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return nil, ValidationErrors{{
				Path:    typeErr.Field,
				Message: fmt.Sprintf("expected %s, got %s", typeErr.Type, typeErr.Value),
			}}
		}
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	if errs := Validate(&c); len(errs) > 0 {
		return nil, errs
	}
	return &c, nil
}

// Validate checks a decoded Collection against schema rules and returns
// every problem found, each with an exact field path.
func Validate(c *Collection) ValidationErrors {
	var errs ValidationErrors
	add := func(path, format string, args ...any) {
		errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf(format, args...)})
	}

	switch {
	case c.SchemaVersion == "":
		add("schemaVersion", "required")
	case !slices.Contains(SupportedSchemaVersions, c.SchemaVersion):
		add("schemaVersion", "unsupported version %q (supported: %s)",
			c.SchemaVersion, strings.Join(SupportedSchemaVersions, ", "))
	}

	switch {
	case c.Slug == "":
		add("slug", "required")
	case !slugPattern.MatchString(c.Slug):
		add("slug", "must be lowercase letters, digits, and hyphens (e.g. \"ai-worlds-fair-2026\")")
	}

	if c.Title == "" {
		add("title", "required")
	}

	if c.RefreshInterval != "" {
		if _, err := time.ParseDuration(c.RefreshInterval); err != nil {
			add("refreshInterval", "invalid duration %q (use Go duration syntax, e.g. \"6h\", \"30m\")", c.RefreshInterval)
		}
	}

	if c.Author != nil && c.Author.URL != "" {
		if !isHTTPURL(c.Author.URL) {
			add("author.url", "must be an http(s) URL")
		}
	}

	if len(c.Videos) == 0 {
		add("videos", "required: at least one video")
	}

	seen := map[string]int{} // youtube ID or URL -> first index
	for i, v := range c.Videos {
		p := func(field string) string { return fmt.Sprintf("videos[%d].%s", i, field) }

		if v.YouTubeURL == "" && v.YouTubeID == "" {
			add(fmt.Sprintf("videos[%d]", i), "either youtubeUrl or youtubeId is required")
		}
		if v.YouTubeID != "" && !youtubeIDPattern.MatchString(v.YouTubeID) {
			add(p("youtubeId"), "must be an 11-character YouTube video ID")
		}
		if v.YouTubeURL != "" {
			if host, ok := youtubeHost(v.YouTubeURL); !ok {
				if host == "" {
					add(p("youtubeUrl"), "must be a valid http(s) URL")
				} else {
					add(p("youtubeUrl"), "host %q is not YouTube (allowed: %s)", host, strings.Join(allowedYouTubeHosts, ", "))
				}
			}
		}

		if key := firstNonEmpty(v.YouTubeID, canonicalURLKey(v.YouTubeURL)); key != "" {
			if first, dup := seen[key]; dup {
				add(fmt.Sprintf("videos[%d]", i), "duplicate of videos[%d]", first)
			} else {
				seen[key] = i
			}
		}

		for j, s := range v.Speakers {
			sp := func(field string) string { return fmt.Sprintf("videos[%d].speakers[%d].%s", i, j, field) }
			if s.Name == "" {
				add(sp("name"), "required")
			}
			if s.Slug != "" && !slugPattern.MatchString(s.Slug) {
				add(sp("slug"), "must be lowercase letters, digits, and hyphens")
			}
		}

		if v.Event != nil && v.Event.Year != 0 && (v.Event.Year < 1990 || v.Event.Year > 2100) {
			add(p("event.year"), "implausible year %d", v.Event.Year)
		}

		if v.AddedAt != "" {
			if _, err := time.Parse(time.RFC3339, v.AddedAt); err != nil {
				add(p("addedAt"), "invalid RFC 3339 timestamp %q (e.g. \"2026-07-18T00:00:00Z\")", v.AddedAt)
			}
		}
	}

	return errs
}

func yamlToJSON(data []byte) ([]byte, error) {
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// youtubeHost returns the URL's host and whether it is an allowed YouTube
// host. An empty host means the value was not a parseable http(s) URL.
func youtubeHost(raw string) (host string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	return u.Hostname(), slices.Contains(allowedYouTubeHosts, strings.ToLower(u.Hostname()))
}

// canonicalURLKey normalizes a URL enough for duplicate detection within a
// single file. Full ID resolution happens in the youtube package.
func canonicalURLKey(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return strings.ToLower(raw)
	}
	if id := u.Query().Get("v"); id != "" {
		return id
	}
	return strings.ToLower(u.Hostname()) + strings.TrimSuffix(u.Path, "/")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
