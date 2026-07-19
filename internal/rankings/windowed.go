package rankings

import (
	"errors"
	"fmt"
	"time"

	"github.com/denver/discovery/internal/collections"
)

const day = 24 * time.Hour

// windowKind selects what a windowed strategy computes from a video's
// snapshots inside its time window.
type windowKind int

const (
	// viewDelta scores the change in view count across the window.
	viewDelta windowKind = iota

	// growthPercent scores the view-count delta as a percent of the
	// window's baseline view count.
	growthPercent

	// rankChange scores position improvement across the window. Rank
	// history is not exposed through History (metric snapshots only),
	// so the computation lands in T16 with rank snapshot queries.
	rankChange
)

// windowed is a snapshot-backed strategy. It reads a video's snapshots
// since now-window and scores per its windowKind. In file mode the
// History source reports collections.ErrHistoryUnavailable, which is
// translated into ErrHistoryRequired and aborts the ranking.
//
// A video needs at least two snapshots inside the window (a baseline and
// a latest observation) to score; otherwise it scores 0.
type windowed struct {
	name   string
	window time.Duration
	kind   windowKind
}

// Name implements Ranker.
func (w windowed) Name() string { return w.name }

// Score implements Ranker.
func (w windowed) Score(v *collections.Video, hist History, now time.Time) (float64, error) {
	if v == nil {
		return 0, nil
	}
	if hist == nil {
		return 0, ErrHistoryRequired
	}
	snaps, err := hist.Snapshots(v.ID, now.Add(-w.window))
	if err != nil {
		if errors.Is(err, collections.ErrHistoryUnavailable) {
			return 0, ErrHistoryRequired
		}
		return 0, fmt.Errorf("%s: reading snapshots for %s: %w", w.name, v.ID, err)
	}

	switch w.kind {
	case viewDelta:
		first, last, ok := endpoints(snaps)
		if !ok {
			return 0, nil
		}
		return float64(last.ViewCount - first.ViewCount), nil

	case growthPercent:
		first, last, ok := endpoints(snaps)
		if !ok || first.ViewCount == 0 {
			// A zero or missing baseline scores 0: growth from
			// nothing is undefined, and we never divide by zero.
			return 0, nil
		}
		return float64(last.ViewCount-first.ViewCount) / float64(first.ViewCount) * 100, nil

	case rankChange:
		return 0, fmt.Errorf("%s: rank-change scoring is implemented in T16", w.name)

	default:
		return 0, fmt.Errorf("%s: unknown window kind %d", w.name, w.kind)
	}
}

// endpoints returns the oldest and newest snapshots in the window.
// Snapshots arrive time-ordered per the History contract. ok is false
// with fewer than two snapshots: no baseline exists inside the window.
func endpoints(snaps []collections.Snapshot) (first, last collections.Snapshot, ok bool) {
	if len(snaps) < 2 {
		return collections.Snapshot{}, collections.Snapshot{}, false
	}
	return snaps[0], snaps[len(snaps)-1], true
}
