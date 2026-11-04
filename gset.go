package crdt

import (
	"iter"
	"maps"
	"reflect"
	"slices"
	"sync"
)

// Valid map keys must be comparable and reflexive. This also rejects NaN (even
// nested in structs) and non-comparable values hidden behind interface types.
func validElement[T comparable](value T) bool {
	// Only interfaces and composite keys can hide non-comparable dynamic values.
	// Concrete scalar keys need no reflection on the value itself.
	switch reflect.TypeFor[T]().Kind() {
	case reflect.Interface, reflect.Struct, reflect.Array:
		rv := reflect.ValueOf(value)
		if rv.IsValid() && !rv.Comparable() {
			return false
		}
	}
	return value == value
}

// GSet is a generic grow-only set. Its zero value is ready for use. All methods
// are safe for concurrent use; do not copy the set after first use. Elements must
// have stable, reflexive equality, as with ordinary Go map keys.
type GSet[T comparable] struct {
	mu       sync.RWMutex
	elements map[T]struct{}
}

// Add inserts an element. Duplicate adds have no effect.
func (s *GSet[T]) Add(value T) error {
	if !validElement(value) {
		return ErrInvalidState
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.elements == nil {
		s.elements = make(map[T]struct{})
	}
	s.elements[value] = struct{}{}
	return nil
}

// Contains reports membership.
func (s *GSet[T]) Contains(value T) bool {
	if !validElement(value) {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.elements[value]
	return ok
}

// Len returns the number of elements.
func (s *GSet[T]) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.elements)
}

// Snapshot returns all elements in unspecified order, detached from the set.
func (s *GSet[T]) Snapshot() []T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Collect(maps.Keys(s.elements))
}

// Values iterates over a detached snapshot without holding a lock in caller code.
func (s *GSet[T]) Values() iter.Seq[T] { return slices.Values(s.Snapshot()) }

// Merge joins a snapshot. Invalid input is rejected before changing the set.
func (s *GSet[T]) Merge(state []T) error {
	for _, value := range state {
		if !validElement(value) {
			return ErrInvalidState
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.elements == nil {
		s.elements = make(map[T]struct{}, len(state))
	}
	for _, value := range state {
		s.elements[value] = struct{}{}
	}
	return nil
}
