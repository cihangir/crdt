package crdt

import (
	"context"
	"fmt"
	"maps"
	"math"
	"sync"
)

// MemoryBackend is a process-local, volatile storage actor. All operations are
// safe for concurrent use. Construct it with NewMemoryBackend and do not copy it.
// Closing it discards its state. It provides no disk persistence or crash recovery.
type MemoryBackend struct {
	mu     sync.Mutex
	id     string
	states map[string]CounterState
	closed bool
}

var _ Backend = (*MemoryBackend)(nil)

// NewMemoryBackend constructs an empty actor with a nonempty, globally unique ID.
// Independent actors must have different IDs, even when using different storage
// implementations. Recover a live actor's latest state before reusing its ID.
func NewMemoryBackend(id string) (*MemoryBackend, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: empty memory actor ID", ErrInvalidBackend)
	}
	return &MemoryBackend{id: id, states: make(map[string]CounterState)}, nil
}

// ID returns the actor's stable identity, including after Close.
func (b *MemoryBackend) ID() string { return b.id }

// Increment atomically advances this actor's component for key.
func (b *MemoryBackend) Increment(ctx context.Context, key string, delta int64) error {
	if delta < 0 {
		return ErrNegativeDelta
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.closed {
		return ErrClosed
	}
	state := b.states[key]
	if state[b.id] > math.MaxInt64-delta {
		return ErrOverflow
	}
	if delta == 0 {
		return nil
	}
	if state == nil {
		state = make(CounterState)
		b.states[key] = state
	}
	state[b.id] += delta
	return nil
}

// Snapshot returns a detached copy of the component vector for key.
func (b *MemoryBackend) Snapshot(ctx context.Context, key string) (CounterState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b.closed {
		return nil, ErrClosed
	}
	return maps.Clone(b.states[key]), nil
}

// Merge atomically joins a snapshot without lowering any existing component.
func (b *MemoryBackend) Merge(ctx context.Context, key string, state CounterState) error {
	if err := state.validate(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.closed {
		return ErrClosed
	}
	if len(state) == 0 {
		return nil
	}
	current := b.states[key]
	if current == nil {
		current = make(CounterState)
		b.states[key] = current
	}
	for actor, value := range state {
		current[actor] = max(current[actor], value)
	}
	return nil
}

// Close discards state and prevents further operations. Repeated calls succeed.
func (b *MemoryBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.states = nil
	return nil
}
