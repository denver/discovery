package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeDotenv(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"YOUTUBE_API_KEY", "DISCOVERY_COLLECTION_PATH",
		"DISCOVERY_DATABASE_URL", "DATABASE_URL", "PORT",
		"DISCOVERY_CACHE_PATH", "DISCOVERY_REFRESH_INTERVAL"} {
		t.Setenv(k, "")
	}
}

func TestLoadReadsDotenvFile(t *testing.T) {
	clearEnv(t)
	writeDotenv(t, `
# comment line
YOUTUBE_API_KEY=key-from-file
export DISCOVERY_COLLECTION_PATH="./collections/test.json"
PORT='8085'

`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.YouTubeAPIKey != "key-from-file" {
		t.Errorf("YouTubeAPIKey = %q", cfg.YouTubeAPIKey)
	}
	if cfg.CollectionPath != "./collections/test.json" {
		t.Errorf("CollectionPath = %q (quotes should be stripped)", cfg.CollectionPath)
	}
	if cfg.Port != 8085 {
		t.Errorf("Port = %d (single quotes should be stripped)", cfg.Port)
	}
}

func TestProcessEnvWinsOverDotenv(t *testing.T) {
	clearEnv(t)
	writeDotenv(t, "YOUTUBE_API_KEY=file-key\nDISCOVERY_COLLECTION_PATH=./from-file.json\n")
	t.Setenv("YOUTUBE_API_KEY", "env-key")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.YouTubeAPIKey != "env-key" {
		t.Errorf("YouTubeAPIKey = %q, want process env to win", cfg.YouTubeAPIKey)
	}
	if cfg.CollectionPath != "./from-file.json" {
		t.Errorf("CollectionPath = %q, want .env to fill the gap", cfg.CollectionPath)
	}
}

func TestNoDotenvFileIsFine(t *testing.T) {
	clearEnv(t)
	t.Chdir(t.TempDir())
	t.Setenv("YOUTUBE_API_KEY", "k")
	t.Setenv("DISCOVERY_COLLECTION_PATH", "./c.json")
	if _, err := Load(); err != nil {
		t.Fatalf("Load without .env: %v", err)
	}
}

func TestModeSwitchIsNamespaced(t *testing.T) {
	clearEnv(t)
	t.Chdir(t.TempDir())
	t.Setenv("YOUTUBE_API_KEY", "k")
	t.Setenv("DISCOVERY_COLLECTION_PATH", "./c.json")

	// Ambient DATABASE_URL from an unrelated project must NOT flip modes.
	t.Setenv("DATABASE_URL", "postgres://other-project/db")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Mode() != FileMode {
		t.Fatalf("mode = %s; ambient DATABASE_URL must be ignored", cfg.Mode())
	}

	// The namespaced variable selects database mode.
	t.Setenv("DISCOVERY_DATABASE_URL", "postgres://localhost/discovery")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Mode() != DatabaseMode {
		t.Fatalf("mode = %s, want database via DISCOVERY_DATABASE_URL", cfg.Mode())
	}
	if cfg.DatabaseURL != "postgres://localhost/discovery" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
}

func TestEnvLookupHelper(t *testing.T) {
	clearEnv(t)
	writeDotenv(t, "DISCOVERY_DATABASE_URL=postgres://from-file/db\n")
	if got := EnvLookup("DISCOVERY_DATABASE_URL"); got != "postgres://from-file/db" {
		t.Errorf("EnvLookup from file = %q", got)
	}
	t.Setenv("DISCOVERY_DATABASE_URL", "postgres://from-env/db")
	if got := EnvLookup("DISCOVERY_DATABASE_URL"); got != "postgres://from-env/db" {
		t.Errorf("EnvLookup precedence = %q, want process env", got)
	}
}
