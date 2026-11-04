package crdt

import (
	"math"
	"sync"
	"uuid"
)

// LWWRegisterState is a shallow copy of register state. Clock is a Lamport logical
// clock, not a wall-clock timestamp; Actor breaks concurrent timestamp ties.
// Values must be treated as immutable, including any referenced maps or slices.
type LWWRegisterState[T any] struct {
	Value T      `json:"value"`
	Clock uint64 `json:"clock"`
	Actor string `json:"actor"`
}

// LWWRegister is a last-writer-wins register ordered by (logical clock, actor).
// Its zero value is an empty register, ready for concurrent use. Do not copy it
// after first use. Values may be any type, but must be treated as immutable.
// Snapshot and Merge copy T by assignment, not by recursively cloning its data.
type LWWRegister[T any] struct {
	mu    sync.RWMutex
	actor string
	state LWWRegisterState[T]
}

// Assign writes a value later than every state this register has observed.
func (r *LWWRegister[T]) Assign(value T) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.Clock == math.MaxUint64 {
		return ErrOverflow
	}
	if r.actor == "" {
		r.actor = uuid.NewV4().String()
	}
	r.state = LWWRegisterState[T]{Value: value, Clock: r.state.Clock + 1, Actor: r.actor}
	return nil
}

// Value returns the current value and whether the register has been assigned.
func (r *LWWRegister[T]) Value() (T, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state.Value, r.state.Clock != 0
}

// Snapshot returns the current state. The zero state is an empty register.
func (r *LWWRegister[T]) Snapshot() LWWRegisterState[T] {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

// Merge adopts a later state. Repeated delivery has no effect. Snapshots must
// originate from registers: a timestamp identifies one immutable write. Equal
// timestamps refer to the same write and are ignored without comparing T.
func (r *LWWRegister[T]) Merge(state LWWRegisterState[T]) error {
	if (state.Clock == 0) != (state.Actor == "") {
		return ErrInvalidState
	}
	if state.Clock == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if state.Clock > r.state.Clock || (state.Clock == r.state.Clock && state.Actor > r.state.Actor) {
		r.state = state
	}
	return nil
}
