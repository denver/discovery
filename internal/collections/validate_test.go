package collections

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validJSON = `{
  "schemaVersion": "1.0",
  "slug": "ai-engineer-worlds-fair-2026",
  "title": "AI Engineer World's Fair 2026",
  "description": "Curated talks.",
  "author": {"name": "Ada Curator", "url": "https://example.com"},
  "source": {"type": "curated", "homepage": "https://www.ai.engineer/"},
  "refreshInterval": "6h",
  "defaultRanking": "views",
  "videos": [
    {
      "youtubeUrl": "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
      "speakers": [{"name": "Speaker One", "slug": "speaker-one"}],
      "event": {"name": "AI Engineer World's Fair", "year": 2026, "city": "San Francisco"},
      "track": "Agents",
      "topics": ["agents", "context-engineering"],
      "organizations": ["Example Company"],
      "featured": true,
      "addedAt": "2026-07-18T00:00:00Z"
    },
    {"youtubeId": "abcdefghijk"}
  ]
}`

func TestParseValid(t *testing.T) {
	c, err := Parse([]byte(validJSON))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Slug != "ai-engineer-worlds-fair-2026" {
		t.Errorf("slug = %q", c.Slug)
	}
	if len(c.Videos) != 2 {
		t.Fatalf("videos = %d, want 2", len(c.Videos))
	}
	if !c.Videos[0].IsPublished() {
		t.Error("published should default to true")
	}
	if _, ok := c.Videos[0].AddedAtTime(); !ok {
		t.Error("AddedAtTime should parse")
	}
	if d, ok := c.RefreshIntervalDuration(); !ok || d.Hours() != 6 {
		t.Errorf("RefreshIntervalDuration = %v, %v", d, ok)
	}
}

func TestValidateErrors(t *testing.T) {
	cases := []struct {
		name     string
		json     string
		wantPath string
		wantMsg  string
	}{
		{
			name:     "missing schemaVersion",
			json:     `{"slug":"a","title":"t","videos":[{"youtubeId":"abcdefghijk"}]}`,
			wantPath: "schemaVersion",
			wantMsg:  "required",
		},
		{
			name:     "unsupported schemaVersion",
			json:     `{"schemaVersion":"9.0","slug":"a","title":"t","videos":[{"youtubeId":"abcdefghijk"}]}`,
			wantPath: "schemaVersion",
			wantMsg:  "unsupported",
		},
		{
			name:     "missing slug",
			json:     `{"schemaVersion":"1.0","title":"t","videos":[{"youtubeId":"abcdefghijk"}]}`,
			wantPath: "slug",
			wantMsg:  "required",
		},
		{
			name:     "bad slug",
			json:     `{"schemaVersion":"1.0","slug":"Bad Slug!","title":"t","videos":[{"youtubeId":"abcdefghijk"}]}`,
			wantPath: "slug",
			wantMsg:  "lowercase",
		},
		{
			name:     "missing title",
			json:     `{"schemaVersion":"1.0","slug":"a","videos":[{"youtubeId":"abcdefghijk"}]}`,
			wantPath: "title",
			wantMsg:  "required",
		},
		{
			name:     "missing videos",
			json:     `{"schemaVersion":"1.0","slug":"a","title":"t"}`,
			wantPath: "videos",
			wantMsg:  "at least one",
		},
		{
			name:     "empty videos",
			json:     `{"schemaVersion":"1.0","slug":"a","title":"t","videos":[]}`,
			wantPath: "videos",
			wantMsg:  "at least one",
		},
		{
			name:     "video missing url and id",
			json:     `{"schemaVersion":"1.0","slug":"a","title":"t","videos":[{"track":"Agents"}]}`,
			wantPath: "videos[0]",
			wantMsg:  "youtubeUrl or youtubeId",
		},
		{
			name:     "bad youtube id",
			json:     `{"schemaVersion":"1.0","slug":"a","title":"t","videos":[{"youtubeId":"short"}]}`,
			wantPath: "videos[0].youtubeId",
			wantMsg:  "11-character",
		},
		{
			name:     "non-youtube host",
			json:     `{"schemaVersion":"1.0","slug":"a","title":"t","videos":[{"youtubeUrl":"https://vimeo.com/12345"}]}`,
			wantPath: "videos[0].youtubeUrl",
			wantMsg:  "not YouTube",
		},
		{
			name:     "unparseable url",
			json:     `{"schemaVersion":"1.0","slug":"a","title":"t","videos":[{"youtubeUrl":"not a url"}]}`,
			wantPath: "videos[0].youtubeUrl",
			wantMsg:  "valid http(s) URL",
		},
		{
			name:     "javascript scheme rejected",
			json:     `{"schemaVersion":"1.0","slug":"a","title":"t","videos":[{"youtubeUrl":"javascript:alert(1)"}]}`,
			wantPath: "videos[0].youtubeUrl",
			wantMsg:  "valid http(s) URL",
		},
		{
			name:     "duplicate videos by id",
			json:     `{"schemaVersion":"1.0","slug":"a","title":"t","videos":[{"youtubeId":"abcdefghijk"},{"youtubeId":"abcdefghijk"}]}`,
			wantPath: "videos[1]",
			wantMsg:  "duplicate of videos[0]",
		},
		{
			name:     "duplicate url and id",
			json:     `{"schemaVersion":"1.0","slug":"a","title":"t","videos":[{"youtubeUrl":"https://www.youtube.com/watch?v=abcdefghijk"},{"youtubeId":"abcdefghijk"}]}`,
			wantPath: "videos[1]",
			wantMsg:  "duplicate of videos[0]",
		},
		{
			name:     "speaker missing name",
			json:     `{"schemaVersion":"1.0","slug":"a","title":"t","videos":[{"youtubeId":"abcdefghijk","speakers":[{"slug":"x"}]}]}`,
			wantPath: "videos[0].speakers[0].name",
			wantMsg:  "required",
		},
		{
			name:     "bad speaker slug",
			json:     `{"schemaVersion":"1.0","slug":"a","title":"t","videos":[{"youtubeId":"abcdefghijk","speakers":[{"name":"X","slug":"Not Valid"}]}]}`,
			wantPath: "videos[0].speakers[0].slug",
			wantMsg:  "lowercase",
		},
		{
			name:     "implausible year",
			json:     `{"schemaVersion":"1.0","slug":"a","title":"t","videos":[{"youtubeId":"abcdefghijk","event":{"year":1200}}]}`,
			wantPath: "videos[0].event.year",
			wantMsg:  "implausible",
		},
		{
			name:     "bad addedAt",
			json:     `{"schemaVersion":"1.0","slug":"a","title":"t","videos":[{"youtubeId":"abcdefghijk","addedAt":"july 18"}]}`,
			wantPath: "videos[0].addedAt",
			wantMsg:  "RFC 3339",
		},
		{
			name:     "bad refreshInterval",
			json:     `{"schemaVersion":"1.0","slug":"a","title":"t","refreshInterval":"tomorrow","videos":[{"youtubeId":"abcdefghijk"}]}`,
			wantPath: "refreshInterval",
			wantMsg:  "invalid duration",
		},
		{
			name:     "bad author url",
			json:     `{"schemaVersion":"1.0","slug":"a","title":"t","author":{"name":"X","url":"ftp://x"},"videos":[{"youtubeId":"abcdefghijk"}]}`,
			wantPath: "author.url",
			wantMsg:  "http(s)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.json))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			verrs, ok := err.(ValidationErrors)
			if !ok {
				t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
			}
			for _, ve := range verrs {
				if ve.Path == tc.wantPath && strings.Contains(ve.Message, tc.wantMsg) {
					return
				}
			}
			t.Errorf("no error at path %q containing %q; got:\n%v", tc.wantPath, tc.wantMsg, verrs)
		})
	}
}

func TestParseTypeErrorHasFieldPath(t *testing.T) {
	_, err := Parse([]byte(`{"schemaVersion":"1.0","slug":"a","title":42,"videos":[{"youtubeId":"abcdefghijk"}]}`))
	if err == nil {
		t.Fatal("expected error")
	}
	verrs, ok := err.(ValidationErrors)
	if !ok || len(verrs) == 0 {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	if verrs[0].Path != "title" {
		t.Errorf("path = %q, want title", verrs[0].Path)
	}
}

func TestLoadFileYAML(t *testing.T) {
	yamlDoc := `
schemaVersion: "1.0"
slug: yaml-collection
title: YAML Collection
videos:
  - youtubeId: abcdefghijk
    topics: [agents]
`
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte(yamlDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if c.Slug != "yaml-collection" || c.Videos[0].YouTubeID != "abcdefghijk" {
		t.Errorf("unexpected parse: %+v", c)
	}
}

func TestLoadFileUnsupportedExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected unsupported extension error, got %v", err)
	}
}

func TestLoadFileExample(t *testing.T) {
	c, err := LoadFile("../../collections/example.json")
	if err != nil {
		t.Fatalf("example collection must validate: %v", err)
	}
	if c.Slug == "" {
		t.Error("example slug empty")
	}
}
