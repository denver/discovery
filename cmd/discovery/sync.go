package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/denver/discovery/internal/collections"
	"github.com/denver/discovery/internal/config"
	"github.com/denver/discovery/internal/database"
	"github.com/denver/discovery/internal/rankings"
	syncengine "github.com/denver/discovery/internal/sync"
	"github.com/denver/discovery/internal/youtube"
)

// Test seams. Constructing the provider client and opening the database
// are the only pieces the CLI cannot exercise hermetically, so tests
// override these package variables with fakes.
var (
	// newFetcher builds the provider client for sync runs.
	newFetcher = func(apiKey string) syncengine.Fetcher { return youtube.NewClient(apiKey) }

	// openDatabase opens the database-mode store.
	openDatabase = database.Open
)

// runSync implements "discovery sync": one full sync run, designed for
// external cron. Exit codes: 0 full success, 1 failure, 3 the run
// completed but some videos could not be fetched (partial data persisted).
func runSync(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	timeout := fs.Duration("timeout", 5*time.Minute, "abort the sync run after this long")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: discovery sync [-timeout 5m]")
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitErr
	}
	if cfg.CollectionPath == "" {
		fmt.Fprintln(stderr, "sync: DISCOVERY_COLLECTION_PATH is required (the collection file to sync)")
		return exitErr
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	store, code := openStore(ctx, cfg, stderr)
	if code != exitOK {
		return code
	}
	defer closeStore(store, stderr)

	engine := syncengine.New(store, newFetcher(cfg.YouTubeAPIKey), rankings.DefaultRegistry(), syncengine.Options{
		CollectionPaths: []string{cfg.CollectionPath},
	})

	result, err := engine.Run(ctx)
	if result != nil {
		printSyncResult(stdout, result)
	}
	if err != nil {
		fmt.Fprintf(stderr, "sync: %v\n", err)
		return exitErr
	}
	if len(result.Failed) > 0 {
		return exitPartial
	}
	return exitOK
}

// openStore selects the Store by operating mode: DATABASE_URL present →
// PostgreSQL, otherwise the in-memory store with the configured cache file
// so provider data written by a CLI sync persists for the server process.
func openStore(ctx context.Context, cfg *config.Config, stderr io.Writer) (collections.Store, int) {
	if cfg.Mode() == config.DatabaseMode {
		store, err := openDatabase(ctx, cfg.DatabaseURL)
		if err != nil {
			fmt.Fprintf(stderr, "database: %v\n", err)
			return nil, exitErr
		}
		return store, exitOK
	}
	return collections.NewMemStore(collections.MemStoreOptions{CachePath: cfg.CachePath}), exitOK
}

// closeStore closes the store, downgrading a close failure to a warning:
// by this point the run's outcome is already decided.
func closeStore(store collections.Store, stderr io.Writer) {
	if err := store.Close(); err != nil {
		fmt.Fprintf(stderr, "warning: %v\n", err)
	}
}

// printSyncResult prints the one-line run summary, plus the failed IDs
// when any video could not be fetched.
func printSyncResult(w io.Writer, r *collections.SyncResult) {
	fmt.Fprintf(w, "sync: fetched=%d failed=%d duration=%s\n",
		r.Fetched, len(r.Failed), r.Duration.Round(time.Millisecond))
	if len(r.Failed) > 0 {
		fmt.Fprintf(w, "failed IDs: %s\n", strings.Join(r.Failed, ", "))
	}
}
