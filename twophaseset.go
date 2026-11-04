package crdt

import (
	"errors"
	"iter"
	"maps"
	"slices"
	"sync"
)

// ErrRemoved indicates that a two-phase set element was permanently removed.
var ErrRemoved = errors.New("crdt: a two-phase set cannot re-add a removed element")

// TwoPhaseSetState is the detached add-set and permanent remove-set.
type TwoPhaseSetState[T comparable] struct {
	Adds    []T `json:"adds"`
	Removes []T `json:"removes"`
}

// TwoPhaseSet supports permanent removal. A removed element can never be added
// again. Its zero value is ready for concurrent use; do not copy after first use.
type TwoPhaseSet[T comparable] struct {
	mu      sync.RWMutex
	adds    map[T]struct{}
	removes map[T]struct{}
}

// Add inserts an element, unless it has ever been removed.
func (s *TwoPhaseSet[T]) Add(value T) error {
	if !validElement(value) {
		return ErrInvalidState
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, removed := s.removes[value]; removed {
		return ErrRemoved
	}
	if s.adds == nil {
		s.adds = make(map[T]struct{})
	}
	s.adds[value] = struct{}{}
	return nil
}

// Remove permanently removes an observed element. Removing an absent element is
// a no-op. Once observed and removed, an element stays removed even if another
// replica concurrently adds it.
func (s *TwoPhaseSet[T]) Remove(value T) bool {
	if !validElement(value) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, added := s.adds[value]; !added {
		return false
	}
	if _, removed := s.removes[value]; removed {
		return false
	}
	if s.removes == nil {
		s.removes = make(map[T]struct{})
	}
	s.removes[value] = struct{}{}
	return true
}

// Contains reports whether the element was added and not removed.
func (s *TwoPhaseSet[T]) Contains(value T) bool {
	if !validElement(value) {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, added := s.adds[value]
	_, removed := s.removes[value]
	return added && !removed
}

// Snapshot returns the complete detached state, including removal history.
func (s *TwoPhaseSet[T]) Snapshot() TwoPhaseSetState[T] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return TwoPhaseSetState[T]{Adds: slices.Collect(maps.Keys(s.adds)), Removes: slices.Collect(maps.Keys(s.removes))}
}

// Values iterates over a detached snapshot of visible elements.
func (s *TwoPhaseSet[T]) Values() iter.Seq[T] {
	s.mu.RLock()
	values := make([]T, 0, len(s.adds))
	for value := range s.adds {
		if _, removed := s.removes[value]; !removed {
			values = append(values, value)
		}
	}
	s.mu.RUnlock()
	return slices.Values(values)
}

// Len returns the number of visible elements.
func (s *TwoPhaseSet[T]) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.adds) - len(s.removes)
}

// Merge joins both histories, validating them before changing the set.
func (s *TwoPhaseSet[T]) Merge(state TwoPhaseSetState[T]) error {
	adds := make(map[T]struct{}, len(state.Adds))
	for _, value := range state.Adds {
		if !validElement(value) {
			return ErrInvalidState
		}
		adds[value] = struct{}{}
	}
	for _, value := range state.Removes {
		if !validElement(value) {
			return ErrInvalidState
		}
		if _, added := adds[value]; !added {
			return ErrInvalidState
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.adds == nil {
		s.adds = make(map[T]struct{})
	}
	if s.removes == nil {
		s.removes = make(map[T]struct{})
	}
	maps.Copy(s.adds, adds)
	for _, value := range state.Removes {
		s.removes[value] = struct{}{}
	}
	return nil
}
