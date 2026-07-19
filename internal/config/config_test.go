package config

import (
	"strings"
	"testing"
	"time"
)

// setEnv pins every config variable so ambient environment (a developer's
// real .env) cannot leak into tests. Unlisted variables are set empty,
// which Load treats as unset.
func setEnv(t *testing.T, vars map[string]string) {
	t.Helper()
	all := []string{
		"YOUTUBE_API_KEY",
		"DISCOVERY_COLLECTION_PATH",
		"DATABASE_URL",
		"PORT",
		"DISCOVERY_CACHE_PATH",
		"DISCOVERY_REFRESH_INTERVAL",
	}
	for _, k := range all {
		t.Setenv(k, vars[k])
	}
}

func TestLoadFileMode(t *testing.T) {
	setEnv(t, map[string]string{
		"YOUTUBE_API_KEY":            "test-key",
		"DISCOVERY_COLLECTION_PATH":  "./collections/example.json",
		"PORT":                       "9090",
		"DISCOVERY_CACHE_PATH":       "/tmp/cache.json",
		"DISCOVERY_REFRESH_INTERVAL": "6h",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Mode() != FileMode {
		t.Errorf("Mode = %s, want file", cfg.Mode())
	}
	if cfg.YouTubeAPIKey != "test-key" {
		t.Errorf("YouTubeAPIKey = %q", cfg.YouTubeAPIKey)
	}
	if cfg.CollectionPath != "./collections/example.json" {
		t.Errorf("CollectionPath = %q", cfg.CollectionPath)
	}
	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want 9090", cfg.Port)
	}
	if cfg.CachePath != "/tmp/cache.json" {
		t.Errorf("CachePath = %q", cfg.CachePath)
	}
	if cfg.RefreshInterval != 6*time.Hour {
		t.Errorf("RefreshInterval = %s, want 6h", cfg.RefreshInterval)
	}
}

func TestLoadDefaults(t *testing.T) {
	setEnv(t, map[string]string{
		"YOUTUBE_API_KEY":           "test-key",
		"DISCOVERY_COLLECTION_PATH": "./collections/example.json",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != DefaultPort {
		t.Errorf("Port = %d, want default %d", cfg.Port, DefaultPort)
	}
	if cfg.CachePath != DefaultCachePath {
		t.Errorf("CachePath = %q, want default %q", cfg.CachePath, DefaultCachePath)
	}
	if cfg.RefreshInterval != 0 {
		t.Errorf("RefreshInterval = %s, want 0 (unset)", cfg.RefreshInterval)
	}
}

func TestLoadDatabaseMode(t *testing.T) {
	setEnv(t, map[string]string{
		"YOUTUBE_API_KEY": "test-key",
		"DATABASE_URL":    "postgres://user:secretpass@localhost:5432/discovery?sslmode=disable",
		// No DISCOVERY_COLLECTION_PATH: optional in database mode.
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Mode() != DatabaseMode {
		t.Errorf("Mode = %s, want database", cfg.Mode())
	}
}

func TestLoadMissingAPIKey(t *testing.T) {
	setEnv(t, map[string]string{
		"DISCOVERY_COLLECTION_PATH": "./collections/example.json",
	})
	_, err := Load()
	if err == nil {
		t.Fatal("Load succeeded without YOUTUBE_API_KEY")
	}
	if !strings.Contains(err.Error(), "YOUTUBE_API_KEY") {
		t.Errorf("error does not name YOUTUBE_API_KEY: %v", err)
	}
}

func TestLoadMissingCollectionPathInFileMode(t *testing.T) {
	setEnv(t, map[string]string{
		"YOUTUBE_API_KEY": "test-key",
	})
	_, err := Load()
	if err == nil {
		t.Fatal("Load succeeded without DISCOVERY_COLLECTION_PATH in file mode")
	}
	if !strings.Contains(err.Error(), "DISCOVERY_COLLECTION_PATH") {
		t.Errorf("error does not name DISCOVERY_COLLECTION_PATH: %v", err)
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error does not mention the DATABASE_URL alternative: %v", err)
	}
}

func TestLoadCollectsAllProblems(t *testing.T) {
	setEnv(t, map[string]string{
		"PORT":                       "not-a-port",
		"DISCOVERY_REFRESH_INTERVAL": "soon",
	})
	_, err := Load()
	if err == nil {
		t.Fatal("Load succeeded with multiple invalid variables")
	}
	for _, name := range []string{"YOUTUBE_API_KEY", "DISCOVERY_COLLECTION_PATH", "PORT", "DISCOVERY_REFRESH_INTERVAL"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not name %s: %v", name, err)
		}
	}
}

func TestLoadBadPort(t *testing.T) {
	for _, bad := range []string{"abc", "0", "-1", "70000"} {
		setEnv(t, map[string]string{
			"YOUTUBE_API_KEY":           "test-key",
			"DISCOVERY_COLLECTION_PATH": "x.json",
			"PORT":                      bad,
		})
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "PORT") {
			t.Errorf("PORT=%q: error = %v, want PORT named", bad, err)
		}
	}
}

func TestLoadBadRefreshInterval(t *testing.T) {
	for _, bad := range []string{"tomorrow", "-1h", "0s"} {
		setEnv(t, map[string]string{
			"YOUTUBE_API_KEY":            "test-key",
			"DISCOVERY_COLLECTION_PATH":  "x.json",
			"DISCOVERY_REFRESH_INTERVAL": bad,
		})
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "DISCOVERY_REFRESH_INTERVAL") {
			t.Errorf("DISCOVERY_REFRESH_INTERVAL=%q: error = %v, want variable named", bad, err)
		}
	}
}

func TestRedactedNeverLeaksSecrets(t *testing.T) {
	setEnv(t, map[string]string{
		"YOUTUBE_API_KEY":            "super-secret-key",
		"DISCOVERY_COLLECTION_PATH":  "./collections/example.json",
		"DATABASE_URL":               "postgres://dbuser:dbpass@db.internal:5432/discovery?password=alsopass",
		"DISCOVERY_REFRESH_INTERVAL": "30m",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, out := range []string{cfg.Redacted(), cfg.String()} {
		for _, secret := range []string{"super-secret-key", "dbuser", "dbpass", "alsopass"} {
			if strings.Contains(out, secret) {
				t.Errorf("redacted output leaks %q: %s", secret, out)
			}
		}
		if !strings.Contains(out, "db.internal:5432/discovery") {
			t.Errorf("redacted output should keep host/db for operators: %s", out)
		}
		if !strings.Contains(out, "mode=database") {
			t.Errorf("redacted output missing mode: %s", out)
		}
	}
}

func TestRedactedUnparseableDatabaseURL(t *testing.T) {
	setEnv(t, map[string]string{
		"YOUTUBE_API_KEY": "k",
		"DATABASE_URL":    "host=db password=hunter2 dbname=discovery",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out := cfg.Redacted()
	if strings.Contains(out, "hunter2") {
		t.Errorf("redacted output leaks DSN password: %s", out)
	}
	if !strings.Contains(out, "database=[set]") {
		t.Errorf("unparseable DATABASE_URL should be fully masked: %s", out)
	}
}
