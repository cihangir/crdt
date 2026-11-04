package crdt

import (
	"context"
	"errors"
)

var (
	// ErrNoBackends indicates that a client was constructed without storage.
	ErrNoBackends = errors.New("crdt: at least one backend is required")
	// ErrInvalidBackend indicates a nil backend, empty actor ID or duplicate actor.
	ErrInvalidBackend = errors.New("crdt: invalid backend")
	// ErrClosed indicates that a built-in backend has been closed.
	ErrClosed = errors.New("crdt: backend is closed")
)

// Backend stores counter component vectors for one actor. Implementations must
// be safe for concurrent use. Keys are fully namespaced by CRDT; an absent key
// represents an empty state. Returned snapshots must not alias backend storage.
//
// ID must be nonempty, stable, and unique among independent actors. Increment
// must atomically add a nonnegative delta to this actor's component, rejecting
// int64 overflow. Merge must atomically join component-wise maxima with existing
// state, preserving concurrent increments. A read followed by an ordinary write
// does not satisfy this contract. Invalid states must be rejected before mutation.
// Merge must neither mutate nor retain the input map; callers may reuse it after
// Merge returns, and several backends may read it concurrently.
//
// Honor context cancellation/deadlines. Increment errors may be ambiguous after
// an I/O failure, so callers must not automatically retry. Merge is idempotent
// and safe to retry. Storage durability and recovery are backend responsibilities.
// Implementations must never silently reset or lower a live actor's components.
//
// NewWithBackends transfers lifecycle ownership to CRDT on success: Close closes
// every supplied backend. It leaves them caller-owned if construction fails.
// A backend must not be shared with another client that can close it prematurely.
type Backend interface {
	ID() string
	Increment(ctx context.Context, key string, delta int64) error
	Snapshot(ctx context.Context, key string) (CounterState, error)
	Merge(ctx context.Context, key string, state CounterState) error
	Close() error
}
