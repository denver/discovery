package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/denver/discovery/internal/collections"
	syncengine "github.com/denver/discovery/internal/sync"
)

// goodCollection has two valid entries; IDs are 11 chars.
const goodCollection = `{
  "schemaVersion": "1.0",
  "slug": "test-talks",
  "title": "Test Talks",
  "description": "Fixture collection",
  "videos": [
    {
      "youtubeId": "dQw4w9WgXcQ",
      "speakers": [{"name": "Ada Lovelace", "slug": "ada-lovelace"}],
      "topics": ["ai", "agents"],
      "track": "keynote",
      "featured": true
    },
    {
      "youtubeId": "abcdefghijk",
      "titleOverride": "Better Title",
      "event": {"name": "Test Conf", "year": 2026, "city": "SF"}
    }
  ]
}`

const badCollection = `{
  "schemaVersion": "1.0",
  "slug": "Bad Slug",
  "videos": [
    {"speakers": [{"name": ""}]}
  ]
}`

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeFetcher is the sync test seam target: a canned Fetch response
// injected through the newFetcher package variable.
type fakeFetcher struct {
	videos []collections.ProviderVideo
	failed []string
	err    error
	gotIDs []string
}

func (f *fakeFetcher) Fetch(_ context.Context, ids []string) ([]collections.ProviderVideo, []string, error) {
	f.gotIDs = ids
	return f.videos, f.failed, f.err
}

// setFetcher swaps the provider-client seam for the test's lifetime.
func setFetcher(t *testing.T, f syncengine.Fetcher) {
	t.Helper()
	orig := newFetcher
	newFetcher = func(string) syncengine.Fetcher { return f }
	t.Cleanup(func() { newFetcher = orig })
}

// setOpenDatabase swaps the database-mode seam for the test's lifetime.
func setOpenDatabase(t *testing.T, store collections.Store, err error) {
	t.Helper()
	orig := openDatabase
	openDatabase = func(context.Context, string) (collections.Store, error) { return store, err }
	t.Cleanup(func() { openDatabase = orig })
}

// setSyncEnv configures a clean file-mode environment for config.Load.
func setSyncEnv(t *testing.T, collectionPath, cachePath string) {
	t.Helper()
	t.Setenv("YOUTUBE_API_KEY", "test-key")
	t.Setenv("DISCOVERY_COLLECTION_PATH", collectionPath)
	t.Setenv("DISCOVERY_CACHE_PATH", cachePath)
	t.Setenv("DISCOVERY_DATABASE_URL", "")
	t.Setenv("DISCOVERY_REFRESH_INTERVAL", "")
	t.Setenv("PORT", "")
}

func providerVideo(id string, views int64) collections.ProviderVideo {
	return collections.ProviderVideo{
		ID:          id,
		Title:       "Video " + id,
		ChannelID:   "chan",
		ChannelName: "Channel",
		PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Stats:       collections.Statistics{ViewCount: views, LikeCount: 10, CommentCount: 2},
	}
}

// --- dispatch and usage ---

func TestUsageListsAllSubcommands(t *testing.T) {
	for _, args := range [][]string{nil, {"-h"}, {"--help"}, {"help"}, {"bogus"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != exitUsage {
			t.Errorf("run(%v) = %d, want %d", args, code, exitUsage)
		}
		out := stdout.String() + stderr.String()
		for _, cmd := range []string{"validate", "sync", "serve", "import", "export", "version"} {
			if !strings.Contains(out, cmd) {
				t.Errorf("run(%v) usage output missing subcommand %q:\n%s", args, cmd, out)
			}
		}
	}
}

func TestUnknownSubcommandNamesIt(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"bogus"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("exit = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), `unknown command "bogus"`) {
		t.Errorf("stderr missing unknown-command message:\n%s", stderr.String())
	}
}

func TestVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit = %d, want %d", code, exitOK)
	}
	if !strings.Contains(stdout.String(), version) {
		t.Errorf("stdout = %q, want it to contain %q", stdout.String(), version)
	}
}

// --- validate ---

func TestValidateGoodFile(t *testing.T) {
	path := writeFile(t, "good.json", goodCollection)
	var stdout, stderr bytes.Buffer
	if code := runValidate([]string{path}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitOK, stderr.String())
	}
	if got, want := stdout.String(), "valid: test-talks (2 videos)\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestValidateBadFilePrintsFieldPaths(t *testing.T) {
	path := writeFile(t, "bad.json", badCollection)
	var stdout, stderr bytes.Buffer
	if code := runValidate([]string{path}, &stdout, &stderr); code != exitErr {
		t.Fatalf("exit = %d, want %d", code, exitErr)
	}
	for _, want := range []string{
		"slug: must be lowercase",
		"title: required",
		"videos[0]: either youtubeUrl or youtubeId is required",
		"videos[0].speakers[0].name: required",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr.String())
		}
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestValidateUnreadableFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "missing.json")
	if code := runValidate([]string{path}, &stdout, &stderr); code != exitErr {
		t.Fatalf("exit = %d, want %d", code, exitErr)
	}
	if !strings.Contains(stderr.String(), "read collection") {
		t.Errorf("stderr = %q, want a read error", stderr.String())
	}
}

func TestValidateArgCount(t *testing.T) {
	for _, args := range [][]string{nil, {"a.json", "b.json"}} {
		var stdout, stderr bytes.Buffer
		if code := runValidate(args, &stdout, &stderr); code != exitUsage {
			t.Errorf("runValidate(%v) = %d, want %d", args, code, exitUsage)
		}
	}
}

// --- sync ---

func TestSyncSuccess(t *testing.T) {
	path := writeFile(t, "good.json", goodCollection)
	cache := filepath.Join(t.TempDir(), "cache.json")
	setSyncEnv(t, path, cache)
	fake := &fakeFetcher{videos: []collections.ProviderVideo{
		providerVideo("dQw4w9WgXcQ", 100),
		providerVideo("abcdefghijk", 50),
	}}
	setFetcher(t, fake)

	var stdout, stderr bytes.Buffer
	if code := runSync(nil, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "fetched=2 failed=0") {
		t.Errorf("stdout = %q, want fetched=2 failed=0", stdout.String())
	}
	if want := []string{"dQw4w9WgXcQ", "abcdefghijk"}; !reflect.DeepEqual(fake.gotIDs, want) {
		t.Errorf("fetched IDs = %v, want %v", fake.gotIDs, want)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Errorf("cache file not written: %v", err)
	}
}

func TestSyncPartialFailureExitsThree(t *testing.T) {
	path := writeFile(t, "good.json", goodCollection)
	setSyncEnv(t, path, filepath.Join(t.TempDir(), "cache.json"))
	setFetcher(t, &fakeFetcher{
		videos: []collections.ProviderVideo{providerVideo("dQw4w9WgXcQ", 100)},
		failed: []string{"abcdefghijk"},
	})

	var stdout, stderr bytes.Buffer
	if code := runSync(nil, &stdout, &stderr); code != exitPartial {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitPartial, stderr.String())
	}
	if !strings.Contains(stdout.String(), "failed IDs: abcdefghijk") {
		t.Errorf("stdout = %q, want failed IDs listed", stdout.String())
	}
}

func TestSyncFetchError(t *testing.T) {
	path := writeFile(t, "good.json", goodCollection)
	setSyncEnv(t, path, filepath.Join(t.TempDir(), "cache.json"))
	setFetcher(t, &fakeFetcher{err: errors.New("api exploded")})

	var stdout, stderr bytes.Buffer
	if code := runSync(nil, &stdout, &stderr); code != exitErr {
		t.Fatalf("exit = %d, want %d", code, exitErr)
	}
	if !strings.Contains(stderr.String(), "api exploded") {
		t.Errorf("stderr = %q, want the fetch error", stderr.String())
	}
}

func TestSyncInvalidCollectionFile(t *testing.T) {
	path := writeFile(t, "bad.json", badCollection)
	setSyncEnv(t, path, filepath.Join(t.TempDir(), "cache.json"))
	setFetcher(t, &fakeFetcher{})

	var stdout, stderr bytes.Buffer
	if code := runSync(nil, &stdout, &stderr); code != exitErr {
		t.Fatalf("exit = %d, want %d", code, exitErr)
	}
	if !strings.Contains(stderr.String(), "collection invalid") {
		t.Errorf("stderr = %q, want validation failure", stderr.String())
	}
}

func TestSyncConfigError(t *testing.T) {
	t.Setenv("YOUTUBE_API_KEY", "")
	t.Setenv("DISCOVERY_COLLECTION_PATH", "")
	t.Setenv("DISCOVERY_DATABASE_URL", "")

	var stdout, stderr bytes.Buffer
	if code := runSync(nil, &stdout, &stderr); code != exitErr {
		t.Fatalf("exit = %d, want %d", code, exitErr)
	}
	if !strings.Contains(stderr.String(), "YOUTUBE_API_KEY") {
		t.Errorf("stderr = %q, want it to name YOUTUBE_API_KEY", stderr.String())
	}
}

func TestSyncDatabaseModeOpenErrorIsClean(t *testing.T) {
	// Unreachable database: the CLI must fail with exit 1 and surface a
	// sanitized error (no credentials), regardless of local postgres.
	path := writeFile(t, "good.json", goodCollection)
	setSyncEnv(t, path, filepath.Join(t.TempDir(), "cache.json"))
	t.Setenv("DISCOVERY_DATABASE_URL", "postgres://user:sekret@127.0.0.1:1/discovery?connect_timeout=1")

	var stdout, stderr bytes.Buffer
	if code := runSync(nil, &stdout, &stderr); code != exitErr {
		t.Fatalf("exit = %d, want %d", code, exitErr)
	}
	if !strings.Contains(stderr.String(), "database") {
		t.Errorf("stderr = %q, want a database error", stderr.String())
	}
	if strings.Contains(stderr.String(), "sekret") {
		t.Errorf("stderr leaks the database password: %q", stderr.String())
	}
}

func TestSyncBadFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runSync([]string{"-bogus"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("exit = %d, want %d", code, exitUsage)
	}
}

// --- import / export ---

func TestImportFileModeValidatesAndNotes(t *testing.T) {
	path := writeFile(t, "good.json", goodCollection)
	t.Setenv("DISCOVERY_DATABASE_URL", "")

	var stdout, stderr bytes.Buffer
	if code := runImport([]string{path}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "valid: test-talks (2 videos)") {
		t.Errorf("stdout = %q, want validation confirmation", stdout.String())
	}
	if !strings.Contains(stdout.String(), "file mode reads the collection file directly") {
		t.Errorf("stdout = %q, want file-mode note", stdout.String())
	}
}

func TestImportInvalidFile(t *testing.T) {
	path := writeFile(t, "bad.json", badCollection)
	t.Setenv("DISCOVERY_DATABASE_URL", "")

	var stdout, stderr bytes.Buffer
	if code := runImport([]string{path}, &stdout, &stderr); code != exitErr {
		t.Fatalf("exit = %d, want %d", code, exitErr)
	}
	if !strings.Contains(stderr.String(), "title: required") {
		t.Errorf("stderr = %q, want field-path errors", stderr.String())
	}
}

func TestImportDatabaseStoreError(t *testing.T) {
	path := writeFile(t, "good.json", goodCollection)
	t.Setenv("DISCOVERY_DATABASE_URL", "postgres://fake/db")
	setOpenDatabase(t, nil, errors.New("connection refused"))

	var stdout, stderr bytes.Buffer
	if code := runImport([]string{path}, &stdout, &stderr); code != exitErr {
		t.Fatalf("exit = %d, want %d", code, exitErr)
	}
	if !strings.Contains(stderr.String(), "connection refused") {
		t.Errorf("stderr = %q, want the open error", stderr.String())
	}
}

func TestImportExportRoundTripPreservesEditorialContent(t *testing.T) {
	path := writeFile(t, "good.json", goodCollection)
	t.Setenv("DISCOVERY_DATABASE_URL", "postgres://fake/db")
	// One shared store stands in for the database across both commands.
	store := collections.NewMemStore(collections.MemStoreOptions{})
	setOpenDatabase(t, store, nil)

	var stdout, stderr bytes.Buffer
	if code := runImport([]string{path}, &stdout, &stderr); code != exitOK {
		t.Fatalf("import exit = %d; stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "imported: test-talks (2 videos)") {
		t.Errorf("import stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runExport([]string{"test-talks"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("export exit = %d; stderr:\n%s", code, stderr.String())
	}

	var got collections.Collection
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("export output is not valid JSON: %v\n%s", err, stdout.String())
	}
	want, err := collections.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&got, want) {
		t.Errorf("round trip mismatch:\ngot  %+v\nwant %+v", &got, want)
	}
	if got.SchemaVersion != "1.0" {
		t.Errorf("schemaVersion = %q, want preserved %q", got.SchemaVersion, "1.0")
	}
}

func TestExportDatabaseModeUnknownSlug(t *testing.T) {
	t.Setenv("DISCOVERY_DATABASE_URL", "postgres://fake/db")
	setOpenDatabase(t, collections.NewMemStore(collections.MemStoreOptions{}), nil)

	var stdout, stderr bytes.Buffer
	if code := runExport([]string{"nope"}, &stdout, &stderr); code != exitErr {
		t.Fatalf("exit = %d, want %d", code, exitErr)
	}
	if !strings.Contains(stderr.String(), `collection "nope" not found`) {
		t.Errorf("stderr = %q, want not-found message", stderr.String())
	}
}

func TestExportFileMode(t *testing.T) {
	path := writeFile(t, "good.json", goodCollection)
	t.Setenv("DISCOVERY_DATABASE_URL", "")
	t.Setenv("DISCOVERY_COLLECTION_PATH", path)

	var stdout, stderr bytes.Buffer
	if code := runExport([]string{"test-talks"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitOK, stderr.String())
	}
	var got collections.Collection
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("export output is not valid JSON: %v", err)
	}
	if got.Slug != "test-talks" || len(got.Videos) != 2 || got.Videos[0].YouTubeID != "dQw4w9WgXcQ" {
		t.Errorf("exported collection wrong: %+v", got)
	}
}

func TestExportFileModeSlugMismatch(t *testing.T) {
	path := writeFile(t, "good.json", goodCollection)
	t.Setenv("DISCOVERY_DATABASE_URL", "")
	t.Setenv("DISCOVERY_COLLECTION_PATH", path)

	var stdout, stderr bytes.Buffer
	if code := runExport([]string{"other-talks"}, &stdout, &stderr); code != exitErr {
		t.Fatalf("exit = %d, want %d", code, exitErr)
	}
	if !strings.Contains(stderr.String(), `has slug "test-talks"`) {
		t.Errorf("stderr = %q, want slug mismatch hint", stderr.String())
	}
}

func TestExportFileModeUnconfigured(t *testing.T) {
	t.Setenv("DISCOVERY_DATABASE_URL", "")
	t.Setenv("DISCOVERY_COLLECTION_PATH", "")

	var stdout, stderr bytes.Buffer
	if code := runExport([]string{"any"}, &stdout, &stderr); code != exitErr {
		t.Fatalf("exit = %d, want %d", code, exitErr)
	}
	if !strings.Contains(stderr.String(), "DISCOVERY_COLLECTION_PATH") {
		t.Errorf("stderr = %q, want configuration hint", stderr.String())
	}
}

// --- serve ---

func TestServeFailsCleanlyWithoutConfig(t *testing.T) {
	t.Setenv("YOUTUBE_API_KEY", "")
	t.Setenv("DISCOVERY_COLLECTION_PATH", "")
	t.Setenv("DISCOVERY_DATABASE_URL", "")
	var stdout, stderr bytes.Buffer
	if code := runServe(nil, &stdout, &stderr); code != exitErr {
		t.Fatalf("exit = %d, want %d", code, exitErr)
	}
	if !strings.Contains(stderr.String(), "YOUTUBE_API_KEY") {
		t.Errorf("stderr = %q, want config error naming the missing variable", stderr.String())
	}
}
