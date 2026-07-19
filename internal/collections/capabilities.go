package collections

import (
	"context"
	"time"
)

// Mover is a video with its rank change over a time window.
type Mover struct {
	Video            *Video `json:"video"`
	Position         int    `json:"position"`
	PreviousPosition int    `json:"previousPosition"`
	Change           int    `json:"change"` // positive = moved up
}

// MoverStore is an optional Store capability, implemented by database mode
// only. Callers type-assert; stores without it get a 501 at the API layer.
// Movers returns videos ordered by absolute rank change under the given
// strategy between now and the closest rank snapshot at or before
// now-window. Videos with no snapshot in range are omitted.
type MoverStore interface {
	Movers(ctx context.Context, slug, strategy string, window time.Duration, limit int) ([]Mover, error)
}
