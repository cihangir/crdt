package crdt

import (
	"context"
	"errors"
	"math"
	"math/big"
)

// PNCounterState contains independent grow-only positive and negative components.
type PNCounterState struct {
	Positive CounterState `json:"positive"`
	Negative CounterState `json:"negative"`
}

// Value returns positive minus negative. Intermediate totals may exceed int64;
// only the final result must fit.
func (s PNCounterState) Value() (int64, error) {
	if err := errors.Join(s.Positive.validate(), s.Negative.validate()); err != nil {
		return 0, err
	}
	total := new(big.Int)
	for _, value := range s.Positive {
		total.Add(total, big.NewInt(value))
	}
	for _, value := range s.Negative {
		total.Sub(total, big.NewInt(value))
	}
	if !total.IsInt64() {
		return 0, ErrOverflow
	}
	return total.Int64(), nil
}

// Merge joins both component vectors into a detached state.
func (s PNCounterState) Merge(other PNCounterState) (PNCounterState, error) {
	positive, pErr := s.Positive.Merge(other.Positive)
	negative, nErr := s.Negative.Merge(other.Negative)
	if err := errors.Join(pErr, nErr); err != nil {
		return PNCounterState{}, err
	}
	return PNCounterState{Positive: positive, Negative: negative}, nil
}

// Compare reports the partial order of both component vectors, not their values.
func (s PNCounterState) Compare(other PNCounterState) (Order, error) {
	p, pErr := s.Positive.Compare(other.Positive)
	n, nErr := s.Negative.Compare(other.Negative)
	if err := errors.Join(pErr, nErr); err != nil {
		return Equal, err
	}
	switch {
	case p == Concurrent || n == Concurrent || (p == Before && n == After) || (p == After && n == Before):
		return Concurrent, nil
	case p == Before || n == Before:
		return Before, nil
	case p == After || n == After:
		return After, nil
	default:
		return Equal, nil
	}
}

// PNCounter supports increments and decrements using configurable backends.
// It has the same concurrency, durability and retry requirements as GCounter.
type PNCounter struct {
	positive *GCounter
	negative *GCounter
}

// NewPNCounter opens a named PN-counter, in a namespace separate from GCounter.
func (c *CRDT) NewPNCounter(key string) *PNCounter {
	positive := c.NewGCounter(key)
	positive.key = c.counterKey("pn:p", key)
	negative := &GCounter{crdt: c, key: c.counterKey("pn:n", key), writer: positive.writer}
	return &PNCounter{positive: positive, negative: negative}
}

// Add adds a signed delta.
func (p *PNCounter) Add(delta int64) error { return p.AddContext(context.Background(), delta) }

// AddContext adds a signed delta. MinInt64 is rejected because its magnitude
// cannot fit in one nonnegative int64 actor component.
func (p *PNCounter) AddContext(ctx context.Context, delta int64) error {
	if delta == math.MinInt64 {
		return ErrOverflow
	}
	if delta < 0 {
		return p.negative.AddContext(ctx, -delta)
	}
	return p.positive.AddContext(ctx, delta)
}

// Increment adds a nonnegative delta.
func (p *PNCounter) Increment(delta int64) error { return p.positive.Add(delta) }

// IncrementContext adds a nonnegative delta with cancellation and a deadline.
func (p *PNCounter) IncrementContext(ctx context.Context, delta int64) error {
	return p.positive.AddContext(ctx, delta)
}

// Decrement subtracts a nonnegative delta.
func (p *PNCounter) Decrement(delta int64) error { return p.negative.Add(delta) }

// DecrementContext subtracts a nonnegative delta with cancellation and a deadline.
func (p *PNCounter) DecrementContext(ctx context.Context, delta int64) error {
	return p.negative.AddContext(ctx, delta)
}

// Snapshot reads both component vectors. Any error means the snapshot is partial;
// unlike a G-counter, a partial PN-counter value is not necessarily a lower bound.
func (p *PNCounter) Snapshot(ctx context.Context) (PNCounterState, error) {
	positive, pErr := p.positive.Snapshot(ctx)
	negative, nErr := p.negative.Snapshot(ctx)
	return PNCounterState{Positive: positive, Negative: negative}, errors.Join(pErr, nErr)
}

// Value reads the current value without repairing replicas.
func (p *PNCounter) Value() (int64, error) { return p.ValueContext(context.Background()) }

// Query is an alias for Value.
func (p *PNCounter) Query() (int64, error) { return p.Value() }

// ValueContext is Value with cancellation and a deadline.
func (p *PNCounter) ValueContext(ctx context.Context) (int64, error) {
	state, err := p.Snapshot(ctx)
	value, valueErr := state.Value()
	return value, errors.Join(err, valueErr)
}

// Merge synchronizes both component vectors and returns the merged value.
func (p *PNCounter) Merge() (int64, error) { return p.MergeContext(context.Background()) }

// MergeContext is Merge with cancellation and a deadline.
func (p *PNCounter) MergeContext(ctx context.Context) (int64, error) {
	state, readErr := p.Snapshot(ctx)
	repairErr := p.MergeState(ctx, state)
	value, valueErr := state.Value()
	return value, errors.Join(readErr, repairErr, valueErr)
}

// MergeState joins a snapshot into all replicas. It is safe to retry. The two
// vectors are independent joins, not a transaction across storage actors.
func (p *PNCounter) MergeState(ctx context.Context, state PNCounterState) error {
	if err := errors.Join(state.Positive.validate(), state.Negative.validate()); err != nil {
		return err
	}
	return errors.Join(p.positive.MergeState(ctx, state.Positive), p.negative.MergeState(ctx, state.Negative))
}
