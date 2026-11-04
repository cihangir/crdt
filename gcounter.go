package crdt

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
)

// GCounter is a grow-only counter using configurable storage backends. It is
// safe for concurrent use. Each handle writes to one randomly selected actor;
// Merge synchronizes actors. An unsuccessful Add may have reached its backend,
// so do not blindly retry it. Use a new key rather than deleting live actor state.
type GCounter struct {
	crdt   *CRDT
	key    string
	writer int
}

// NewGCounter opens a named counter without network I/O.
func (c *CRDT) NewGCounter(key string) *GCounter {
	return &GCounter{crdt: c, key: c.counterKey("g", key), writer: rand.IntN(len(c.replicas))}
}

// Add increments the selected actor by delta. Negative deltas are rejected.
func (g *GCounter) Add(delta int64) error { return g.AddContext(context.Background(), delta) }

// AddContext atomically increments one actor without automatic retries/failover.
func (g *GCounter) AddContext(ctx context.Context, delta int64) error {
	if delta < 0 {
		return ErrNegativeDelta
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r := g.crdt.replicas[g.writer]
	return r.backend.Increment(ctx, g.key, delta)
}

// Value reads the merged value without repairing replicas. If a replica is
// unavailable, the returned value is a partial lower bound and err is non-nil.
func (g *GCounter) Value() (int64, error) { return g.ValueContext(context.Background()) }

// ValueContext is Value with cancellation and a deadline.
func (g *GCounter) ValueContext(ctx context.Context) (int64, error) {
	state, err := g.Snapshot(ctx)
	value, valueErr := state.Value()
	return value, errors.Join(err, valueErr)
}

// Query is an alias for Value.
func (g *GCounter) Query() (int64, error) { return g.Value() }

// Snapshot reads a detached state from all replicas. If some reads fail, state
// contains only components from successful reads and err describes the failures.
func (g *GCounter) Snapshot(ctx context.Context) (CounterState, error) {
	states := make([]CounterState, len(g.crdt.replicas))
	errs := make([]error, len(states))
	var wg sync.WaitGroup
	for i, r := range g.crdt.replicas {
		wg.Go(func() { states[i], errs[i] = r.backend.Snapshot(ctx, g.key) })
	}
	wg.Wait()
	merged := make(CounterState)
	for i, state := range states {
		if errs[i] == nil {
			errs[i] = state.validate()
			if errs[i] == nil {
				for actor, value := range state {
					merged[actor] = max(merged[actor], value)
				}
			}
		}
		if errs[i] != nil {
			errs[i] = fmt.Errorf("crdt: read replica %d: %w", i, errs[i])
		}
	}
	return merged, errors.Join(errs...)
}

// Merge reads and repairs all replicas by component-wise maximum, returning the
// merged value. Repairs cannot overwrite concurrent increments. A non-nil error
// indicates an incomplete read, repair, or an unrepresentable total.
func (g *GCounter) Merge() (int64, error) { return g.MergeContext(context.Background()) }

// MergeContext is Merge with cancellation and a deadline.
func (g *GCounter) MergeContext(ctx context.Context) (int64, error) {
	state, readErr := g.Snapshot(ctx)
	repairErr := g.MergeState(ctx, state)
	value, valueErr := state.Value()
	return value, errors.Join(readErr, repairErr, valueErr)
}

// MergeState joins a detached state into every backend without lowering any
// component. This is idempotent and may safely be retried after an error.
func (g *GCounter) MergeState(ctx context.Context, state CounterState) error {
	if err := state.validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	errs := make([]error, len(g.crdt.replicas))
	var wg sync.WaitGroup
	for i, r := range g.crdt.replicas {
		wg.Go(func() {
			if err := r.backend.Merge(ctx, g.key, state); err != nil {
				errs[i] = fmt.Errorf("crdt: repair replica %d: %w", i, err)
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}
