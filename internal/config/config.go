// Package config loads Discovery Engine configuration from environment
// variables (with ./.env as a fallback for unset variables) and selects
// the operating mode: file mode by default, database mode when
// DISCOVERY_DATABASE_URL is set. The unprefixed DATABASE_URL is
// deliberately ignored: ambient values from other projects' shells must
// never flip the mode. See .env.example for the full list of variables.
package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Mode is the storage operating mode.
type Mode string

const (
	// FileMode serves from the collection file with an in-memory store
	// and a local cache file. No database required.
	FileMode Mode = "file"

	// DatabaseMode persists to PostgreSQL, enabling snapshots, history,
	// and rank movement. Selected by DISCOVERY_DATABASE_URL presence.
	DatabaseMode Mode = "database"
)

// Defaults for optional variables.
const (
	DefaultPort      = 8080
	DefaultCachePath = "./.discovery-cache.json"
)

// Config is the loaded, validated configuration. Treat YouTubeAPIKey and
// DatabaseURL as secrets: never log them; use String/Redacted for output.
type Config struct {
	// YouTubeAPIKey is the YouTube Data API v3 key (YOUTUBE_API_KEY).
	// Required. Server-side only, never exposed to clients or logs.
	YouTubeAPIKey string

	// CollectionPath is the collection file to serve
	// (DISCOVERY_COLLECTION_PATH). Required in file mode.
	CollectionPath string

	// DatabaseURL is the PostgreSQL connection string
	// (DISCOVERY_DATABASE_URL). Optional; presence selects database mode.
	DatabaseURL string

	// Port is the HTTP listen port (PORT, default 8080).
	Port int

	// CachePath is the file-mode metadata cache file
	// (DISCOVERY_CACHE_PATH, default ./.discovery-cache.json).
	CachePath string

	// RefreshInterval overrides the collection file's refreshInterval
	// when set (DISCOVERY_REFRESH_INTERVAL). Zero when unset.
	RefreshInterval time.Duration
}

// Load reads configuration from the environment. Empty variables are
// treated as unset. The returned error names every missing or invalid
// variable so it can be fixed in one pass.
func Load() (*Config, error) {
	cfg := &Config{
		YouTubeAPIKey:  EnvLookup("YOUTUBE_API_KEY"),
		CollectionPath: EnvLookup("DISCOVERY_COLLECTION_PATH"),
		DatabaseURL:    EnvLookup("DISCOVERY_DATABASE_URL"),
		Port:           DefaultPort,
		CachePath:      DefaultCachePath,
	}

	var problems []string
	if cfg.YouTubeAPIKey == "" {
		problems = append(problems,
			"YOUTUBE_API_KEY is required: set it to a YouTube Data API v3 key (see .env.example and README)")
	}
	if cfg.DatabaseURL == "" && cfg.CollectionPath == "" {
		problems = append(problems,
			"DISCOVERY_COLLECTION_PATH is required in file mode: set it to your collection file (e.g. ./collections/example.json), or set DISCOVERY_DATABASE_URL to run in database mode")
	}
	if v := EnvLookup("PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			problems = append(problems,
				fmt.Sprintf("PORT %q is not a valid TCP port (must be 1-65535)", v))
		} else {
			cfg.Port = p
		}
	}
	if v := EnvLookup("DISCOVERY_CACHE_PATH"); v != "" {
		cfg.CachePath = v
	}
	if v := EnvLookup("DISCOVERY_REFRESH_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		switch {
		case err != nil:
			problems = append(problems,
				fmt.Sprintf("DISCOVERY_REFRESH_INTERVAL %q is not a valid Go duration (e.g. \"6h\", \"30m\")", v))
		case d <= 0:
			problems = append(problems,
				fmt.Sprintf("DISCOVERY_REFRESH_INTERVAL %q must be positive", v))
		default:
			cfg.RefreshInterval = d
		}
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid configuration:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return cfg, nil
}

// Mode returns DatabaseMode when DISCOVERY_DATABASE_URL is set, FileMode
// otherwise.
func (c *Config) Mode() Mode {
	if c.DatabaseURL != "" {
		return DatabaseMode
	}
	return FileMode
}

// String returns the redacted form; Config is safe to pass to loggers.
func (c *Config) String() string { return c.Redacted() }

// Redacted returns a loggable one-line summary that never includes the
// API key or DATABASE_URL credentials.
func (c *Config) Redacted() string {
	var b strings.Builder
	fmt.Fprintf(&b, "mode=%s port=%d", c.Mode(), c.Port)
	if c.Mode() == DatabaseMode {
		fmt.Fprintf(&b, " database=%s", redactDatabaseURL(c.DatabaseURL))
	}
	if c.CollectionPath != "" {
		fmt.Fprintf(&b, " collection=%s", c.CollectionPath)
	}
	fmt.Fprintf(&b, " cache=%s", c.CachePath)
	if c.RefreshInterval > 0 {
		fmt.Fprintf(&b, " refresh=%s", c.RefreshInterval)
	}
	fmt.Fprintf(&b, " youtube_api_key=%s", presence(c.YouTubeAPIKey))
	return b.String()
}

// redactDatabaseURL keeps only scheme, host, and database name — userinfo
// and query parameters (which may carry credentials) are dropped. Values
// that do not parse as a URL are fully masked.
func redactDatabaseURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "[set]"
	}
	return u.Scheme + "://" + u.Host + u.Path
}

func presence(secret string) string {
	if secret == "" {
		return "[unset]"
	}
	return "[set]"
}
