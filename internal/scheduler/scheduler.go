// Package scheduler runs the sync engine on a fixed interval, honoring
// the collection/config refreshInterval. It is one of the three callers
// of the single sync path (ADR-001, decision 4); overlapping runs are
// prevented by the engine itself.
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/denver/discovery/internal/collections"
	syncpkg "github.com/denver/discovery/internal/sync"
)

// Runner is the slice of *sync.Engine the scheduler needs.
type Runner interface {
	Run(ctx context.Context) (*collections.SyncResult, error)
}

// The real engine must satisfy Runner.
var _ Runner = (*syncpkg.Engine)(nil)

// Scheduler triggers a sync run every interval until its context is
// canceled. Failed runs are logged and the schedule continues; a tick
// that finds a sync already running (manual or CLI) is skipped.
type Scheduler struct {
	engine   Runner
	interval time.Duration
	logger   *slog.Logger
}

// New returns a Scheduler. interval must be positive by the time Start is
// called; logger defaults to slog.Default() when nil.
func New(engine Runner, interval time.Duration, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{engine: engine, interval: interval, logger: logger}
}

// Start blocks, running the engine every interval, until ctx is done.
// Callers wanting a background scheduler run it in a goroutine and cancel
// the context to stop.
func (s *Scheduler) Start(ctx context.Context) {
	if s.interval <= 0 {
		s.logger.Error("scheduler: not starting, interval must be positive", "interval", s.interval)
		return
	}
	s.logger.Info("scheduler: started", "interval", s.interval)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("scheduler: stopped")
			return
		case <-ticker.C:
			s.runOnce(ctx)
		}
	}
}

// runOnce performs one scheduled sync and logs its outcome.
func (s *Scheduler) runOnce(ctx context.Context) {
	result, err := s.engine.Run(ctx)
	switch {
	case errors.Is(err, syncpkg.ErrSyncInProgress):
		s.logger.Debug("scheduler: sync already running, tick skipped")
	case err != nil:
		s.logger.Error("scheduler: sync failed", "error", err)
	default:
		s.logger.Info("scheduler: sync complete",
			"fetched", result.Fetched,
			"failed", len(result.Failed),
			"duration", result.Duration)
	}
}
