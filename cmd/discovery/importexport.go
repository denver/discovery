package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/denver/discovery/internal/collections"
	"github.com/denver/discovery/internal/config"
)

// Import/export read DISCOVERY_DATABASE_URL (and DISCOVERY_COLLECTION_PATH in file
// mode) directly rather than via config.Load: they are editorial-content
// commands and must not demand a YouTube API key.

// runImport implements "discovery import <file>". Database mode upserts
// the validated collection into PostgreSQL. In file mode there is nothing
// to import — the server reads the collection file directly — so the file
// is validated and a note printed.
func runImport(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: discovery import <file>")
		return exitUsage
	}
	c, err := collections.LoadFile(args[0])
	if err != nil {
		printLoadError(stderr, err)
		return exitErr
	}

	dbURL := config.EnvLookup("DISCOVERY_DATABASE_URL")
	if dbURL == "" {
		fmt.Fprintf(stdout, "valid: %s (%d videos)\n", c.Slug, len(c.Videos))
		fmt.Fprintln(stdout, "file mode reads the collection file directly; nothing to import. Set DISCOVERY_DATABASE_URL to import into PostgreSQL.")
		return exitOK
	}

	ctx := context.Background()
	store, err := openDatabase(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(stderr, "database: %v\n", err)
		return exitErr
	}
	defer closeStore(store, stderr)
	if err := store.UpsertCollection(ctx, c); err != nil {
		fmt.Fprintf(stderr, "import: %v\n", err)
		return exitErr
	}
	fmt.Fprintf(stdout, "imported: %s (%d videos)\n", c.Slug, len(c.Videos))
	return exitOK
}

// runExport implements "discovery export <slug>": write the collection's
// editorial content as canonical, indented collection JSON (the file
// shape, schemaVersion preserved) to stdout. Database mode reads from the
// store; file mode re-emits the configured collection file in normalized
// form (resolved youtubeIds kept when present). Import then export
// round-trips editorial content.
func runExport(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: discovery export <slug>")
		return exitUsage
	}
	slug := args[0]

	var c *collections.Collection
	if dbURL := config.EnvLookup("DISCOVERY_DATABASE_URL"); dbURL != "" {
		ctx := context.Background()
		store, err := openDatabase(ctx, dbURL)
		if err != nil {
			fmt.Fprintf(stderr, "database: %v\n", err)
			return exitErr
		}
		defer closeStore(store, stderr)
		info, err := store.GetCollection(ctx, slug)
		if err != nil {
			if errors.Is(err, collections.ErrNotFound) {
				fmt.Fprintf(stderr, "export: collection %q not found\n", slug)
			} else {
				fmt.Fprintf(stderr, "export: %v\n", err)
			}
			return exitErr
		}
		c = &info.Collection
	} else {
		path := config.EnvLookup("DISCOVERY_COLLECTION_PATH")
		if path == "" {
			fmt.Fprintln(stderr, "export: set DISCOVERY_COLLECTION_PATH (file mode) or DISCOVERY_DATABASE_URL (database mode)")
			return exitErr
		}
		loaded, err := collections.LoadFile(path)
		if err != nil {
			printLoadError(stderr, err)
			return exitErr
		}
		if loaded.Slug != slug {
			fmt.Fprintf(stderr, "export: collection %q not found (the configured file %s has slug %q)\n", slug, path, loaded.Slug)
			return exitErr
		}
		c = loaded
	}

	out, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "export: %v\n", err)
		return exitErr
	}
	fmt.Fprintln(stdout, string(out))
	return exitOK
}
