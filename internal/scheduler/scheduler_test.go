package scheduler_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/denver/discovery/internal/collections"
	"github.com/denver/discovery/internal/rankings"
	"github.com/denver/discovery/internal/scheduler"
	syncpkg "github.com/denver/discovery/internal/sync"
)

const collectionJSON = `{
  "schemaVersion": "1.0",
  "slug": "sched-test",
  "title": "Scheduler Test",
  "videos": [{"youtubeId": "vid00000001"}]
}`

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// countingFetcher counts Fetch calls and returns one video.
type countingFetcher struct {
	calls atomic.Int32
}

func (f *countingFetcher) Fetch(_ context.Context, ids []string) ([]collections.ProviderVideo, []string, error) {
	f.calls.Add(1)
	out := make([]collections.ProviderVideo, 0, len(ids))
	for _, id := range ids {
		out = append(out, collections.ProviderVideo{
			ID:    id,
			Title: "T",
			Stats: collections.Statistics{ViewCount: 1, CapturedAt: time.Now()},
		})
	}
	return out, nil, nil
}

// newEngine builds a real sync engine over a MemStore and the fetcher.
func newEngine(t *testing.T, fetcher syncpkg.Fetcher) *syncpkg.Engine {
	t.Helper()
	path := filepath.Join(t.TempDir(), "collection.json")
	if err := os.WriteFile(path, []byte(collectionJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	store := collections.NewMemStore(collections.MemStoreOptions{Logger: discardLogger()})
	return syncpkg.New(store, fetcher, rankings.DefaultRegistry(), syncpkg.Options{
		CollectionPaths: []string{path},
		Logger:          discardLogger(),
	})
}

func TestSchedulerFiresAndStops(t *testing.T) {
	fetcher := &countingFetcher{}
	engine := newEngine(t, fetcher)
	sched := scheduler.New(engine, 5*time.Millisecond, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sched.Start(ctx)
		close(done)
	}()

	// Wait until the engine has run at least 3 times.
	deadline := time.After(5 * time.Second)
	for fetcher.calls.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("scheduler fired %d times, want >= 3", fetcher.calls.Load())
		case <-time.After(time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after context cancellation")
	}
}

// fakeRunner scripts Run results without a real engine.
type fakeRunner struct {
	calls atomic.Int32
	err   error
}

func (r *fakeRunner) Run(context.Context) (*collections.SyncResult, error) {
	r.calls.Add(1)
	if r.err != nil {
		return nil, r.err
	}
	return &collections.SyncResult{Failed: []string{}}, nil
}

func TestSchedulerContinuesAfterFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"run failure", errors.New("boom")},
		{"overlap skipped", syncpkg.ErrSyncInProgress},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{err: tc.err}
			sched := scheduler.New(runner, time.Millisecond, discardLogger())

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				sched.Start(ctx)
				close(done)
			}()

			deadline := time.After(5 * time.Second)
			for runner.calls.Load() < 3 {
				select {
				case <-deadline:
					t.Fatalf("scheduler stopped after failures: %d runs", runner.calls.Load())
				case <-time.After(time.Millisecond):
				}
			}
			cancel()
			<-done
		})
	}
}

func TestSchedulerRejectsNonPositiveInterval(t *testing.T) {
	runner := &fakeRunner{}
	sched := scheduler.New(runner, 0, discardLogger())

	done := make(chan struct{})
	go func() {
		sched.Start(context.Background())
		close(done)
	}()
	select {
	case <-done: // returned immediately instead of ticking or panicking
	case <-time.After(time.Second):
		t.Fatal("Start with zero interval must return immediately")
	}
	if runner.calls.Load() != 0 {
		t.Fatalf("engine ran %d times with zero interval", runner.calls.Load())
	}
}
