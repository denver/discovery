// Package rankings defines the ranking strategy contract and the built-in
// strategies. Strategies are pure scoring functions: no I/O, no clocks.
//
// Adding a strategy: implement Ranker in a new file and register it in
// DefaultRegistry (see registry.go). Nothing else changes; the API and CLI
// resolve strategies by name.
package rankings

import (
	"errors"
	"time"

	"github.com/denver/discovery/internal/collections"
)

// ErrHistoryRequired is returned by windowed strategies (views_24h, ...)
// when the History source cannot provide snapshots (file mode).
var ErrHistoryRequired = errors.New("ranking strategy requires database mode (historical snapshots)")

// History gives strategies read access to metric snapshots. File mode
// implementations return collections.ErrHistoryUnavailable, which windowed
// strategies translate into ErrHistoryRequired.
type History interface {
	// Snapshots returns time-ordered snapshots for a video since the
	// given time.
	Snapshots(videoID string, since time.Time) ([]collections.Snapshot, error)
}

// NoHistory is a History that always reports unavailability. Used in file
// mode and by tests of non-windowed strategies.
type NoHistory struct{}

func (NoHistory) Snapshots(string, time.Time) ([]collections.Snapshot, error) {
	return nil, collections.ErrHistoryUnavailable
}

// Ranker scores videos under one strategy. Higher scores rank first.
// Implementations must be safe for concurrent use and must not panic on
// videos with nil Statistics (score such videos as 0; Rank places them
// after scored videos).
type Ranker interface {
	// Name is the strategy identifier used in ?sort= and defaultRanking.
	Name() string

	// Score computes the video's score. now is the reference time for
	// windowed strategies (injected for determinism, never time.Now()).
	// Returning ErrHistoryRequired aborts the ranking as a whole.
	Score(v *collections.Video, hist History, now time.Time) (float64, error)
}

// Ranked pairs a video with its computed ranking under one strategy.
type Ranked struct {
	Video *collections.Video
	// Position is 1-based. Score is the raw strategy score.
	Position int
	Score    float64
}
