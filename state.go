package crdt

import (
	"errors"
	"fmt"
	"maps"
	"math"
)

var (
	ErrNoServers     = errors.New("crdt: at least one Redis server is required")
	ErrNegativeDelta = errors.New("crdt: grow-only increments must be nonnegative")
	ErrOverflow      = errors.New("crdt: numeric overflow")
	ErrInvalidState  = errors.New("crdt: invalid replica state")
)

// Order describes the partial ordering of two CRDT states.
type Order uint8

const (
	Equal Order = iota
	Before
	After
	Concurrent
)

// CounterState is a detached snapshot of nonnegative counts keyed by actor ID.
// A nil map is the empty state. Do not modify a snapshot during a merge.
type CounterState map[string]int64

func (s CounterState) validate() error {
	for actor, value := range s {
		if actor == "" || value < 0 {
			return fmt.Errorf("%w: counter component", ErrInvalidState)
		}
	}
	return nil
}

// Value returns the sum of all actor components, checking for overflow.
func (s CounterState) Value() (int64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	var total int64
	for _, value := range s {
		if total > math.MaxInt64-value {
			return 0, ErrOverflow
		}
		total += value
	}
	return total, nil
}

// Merge returns a new state containing the maximum for each actor.
func (s CounterState) Merge(other CounterState) (CounterState, error) {
	if err := errors.Join(s.validate(), other.validate()); err != nil {
		return nil, err
	}
	merged := make(CounterState, len(s)+len(other))
	maps.Copy(merged, s)
	for actor, value := range other {
		merged[actor] = max(merged[actor], value)
	}
	return merged, nil
}

// Compare reports whether s equals, precedes, follows, or is concurrent with other.
func (s CounterState) Compare(other CounterState) (Order, error) {
	if err := errors.Join(s.validate(), other.validate()); err != nil {
		return Equal, err
	}
	var less, greater bool
	for actor, value := range s {
		less = less || value < other[actor]
		greater = greater || value > other[actor]
	}
	for actor, value := range other {
		less = less || s[actor] < value
		greater = greater || s[actor] > value
	}
	switch {
	case less && greater:
		return Concurrent, nil
	case less:
		return Before, nil
	case greater:
		return After, nil
	default:
		return Equal, nil
	}
}
