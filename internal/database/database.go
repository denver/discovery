// Package database implements collections.Store backed by PostgreSQL
// (database mode). The real implementation lands in T14; this stub pins
// the constructor signature so wiring code compiles against it now.
package database

import (
	"context"
	"errors"

	"github.com/denver/discovery/internal/collections"
)

// ErrNotImplemented is returned until T14 lands.
var ErrNotImplemented = errors.New("database mode not implemented yet (T14)")

// Open connects to PostgreSQL, applies pending migrations, and returns a
// Store. The URL is a standard postgres:// connection string.
func Open(ctx context.Context, databaseURL string) (collections.Store, error) {
	return nil, ErrNotImplemented
}
