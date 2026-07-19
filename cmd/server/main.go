// Command server runs the Discovery Engine HTTP server: the REST API
// under /api/v1, GET /health, an initial sync at startup, and the
// background refresh scheduler when a refresh interval is configured.
// The web UI mounts on the root mux at "/" (Lane E, merged separately).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/denver/discovery/internal/api"
	"github.com/denver/discovery/internal/collections"
	"github.com/denver/discovery/internal/config"
	"github.com/denver/discovery/internal/database"
	"github.com/denver/discovery/internal/rankings"
	"github.com/denver/discovery/internal/scheduler"
	"github.com/denver/discovery/internal/service"
	syncpkg "github.com/denver/discovery/internal/sync"
	"github.com/denver/discovery/internal/youtube"
)

const shutdownTimeout = 10 * time.Second

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("server exiting", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger.Info("configuration loaded", "config", cfg.Redacted())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Store: mode is selected solely by DATABASE_URL presence (ADR-001).
	var store collections.Store
	if cfg.Mode() == config.DatabaseMode {
		store, err = database.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
	} else {
		store = collections.NewMemStore(collections.MemStoreOptions{
			CachePath: cfg.CachePath,
			Logger:    logger,
		})
	}
	defer func() {
		if err := store.Close(); err != nil {
			logger.Error("close store", "error", err)
		}
	}()

	registry := rankings.DefaultRegistry()
	var paths []string
	if cfg.CollectionPath != "" {
		paths = append(paths, cfg.CollectionPath)
	}
	engine := syncpkg.New(store, youtube.NewClient(cfg.YouTubeAPIKey), registry, syncpkg.Options{
		CollectionPaths: paths,
		Logger:          logger,
	})
	svc := &service.Service{Store: store, Registry: registry}

	// Root mux: the API owns /health and /api/v1/*; "/" stays unclaimed
	// for the web UI handler, mounted at merge.
	apiHandler := api.New(svc, engine, api.WithLogger(logger))
	root := http.NewServeMux()
	root.Handle("/health", apiHandler)
	root.Handle("/api/v1/", apiHandler)

	// Initial sync in the background: a failure is logged, never fatal —
	// the server serves cached (or empty) data until the next run.
	go func() {
		if _, err := engine.Run(ctx); err != nil {
			logger.Error("initial sync failed; serving cached or empty data", "error", err)
		}
	}()

	// Scheduler: config override wins, else the collection file's
	// refreshInterval. No interval means manual/CLI syncs only.
	if interval := refreshInterval(cfg, logger); interval > 0 {
		go scheduler.New(engine, interval, logger).Start(ctx)
	}

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           root,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()
	logger.Info("discovery-engine listening", "addr", srv.Addr, "mode", cfg.Mode())

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// refreshInterval resolves the scheduler interval: the
// DISCOVERY_REFRESH_INTERVAL override when set, otherwise the collection
// file's refreshInterval. Zero disables the scheduler.
func refreshInterval(cfg *config.Config, logger *slog.Logger) time.Duration {
	if cfg.RefreshInterval > 0 {
		return cfg.RefreshInterval
	}
	if cfg.CollectionPath == "" {
		return 0
	}
	c, err := collections.LoadFile(cfg.CollectionPath)
	if err != nil {
		logger.Warn("could not read refreshInterval from collection file", "path", cfg.CollectionPath, "error", err)
		return 0
	}
	if d, ok := c.RefreshIntervalDuration(); ok {
		return d
	}
	return 0
}
