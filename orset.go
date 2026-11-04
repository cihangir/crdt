package crdt

import (
	"iter"
	"maps"
	"slices"
	"sync"
	"uuid"
)

// ORSetEntry records the globally unique tags belonging to an added value.
type ORSetEntry[T comparable] struct {
	Value T        `json:"value"`
	Tags  []string `json:"tags"`
}

// ORSetState is a detached observed-remove set snapshot. Tombstones must be
// retained: dropping them can resurrect elements when an old snapshot arrives.
type ORSetState[T comparable] struct {
	Entries    []ORSetEntry[T] `json:"entries"`
	Tombstones []string        `json:"tombstones"`
}

// ORSet is an add-wins observed-remove set. A remove only affects add tags it has
// observed, so a concurrent add survives, and removed elements can be re-added.
// Its zero value is ready for concurrent use; do not copy after first use.
type ORSet[T comparable] struct {
	mu         sync.RWMutex
	entries    map[T]map[string]struct{}
	tombstones map[string]struct{}
}

// Add inserts a value with a fresh UUID, including when it is already present.
func (s *ORSet[T]) Add(value T) error {
	if !validElement(value) {
		return ErrInvalidState
	}
	tag := uuid.NewV4().String()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[T]map[string]struct{})
	}
	if s.entries[value] == nil {
		s.entries[value] = make(map[string]struct{})
	}
	s.entries[value][tag] = struct{}{}
	return nil
}

// Remove removes all currently observed adds of value. It returns whether the
// value was present. It does not remove concurrent adds that have not arrived.
func (s *ORSet[T]) Remove(value T) bool {
	if !validElement(value) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tags := s.entries[value]
	if len(tags) == 0 {
		return false
	}
	if s.tombstones == nil {
		s.tombstones = make(map[string]struct{})
	}
	maps.Copy(s.tombstones, tags)
	delete(s.entries, value)
	return true
}

// Contains reports membership.
func (s *ORSet[T]) Contains(value T) bool {
	if !validElement(value) {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries[value]) > 0
}

// Len returns the number of visible elements.
func (s *ORSet[T]) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// Values iterates over a detached snapshot of visible elements.
func (s *ORSet[T]) Values() iter.Seq[T] {
	s.mu.RLock()
	values := slices.Collect(maps.Keys(s.entries))
	s.mu.RUnlock()
	return slices.Values(values)
}

// Snapshot returns all live add tags and all tombstones.
func (s *ORSet[T]) Snapshot() ORSetState[T] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := ORSetState[T]{Tombstones: slices.Collect(maps.Keys(s.tombstones))}
	for value, tags := range s.entries {
		state.Entries = append(state.Entries, ORSetEntry[T]{Value: value, Tags: slices.Collect(maps.Keys(tags))})
	}
	return state
}

// Merge joins a snapshot, discarding adds whose tags have been removed. Invalid
// snapshots are rejected before changing the set. Tags must name exactly one value.
func (s *ORSet[T]) Merge(state ORSetState[T]) error {
	owners := make(map[string]T)
	for _, entry := range state.Entries {
		if !validElement(entry.Value) || len(entry.Tags) == 0 {
			return ErrInvalidState
		}
		for _, tag := range entry.Tags {
			if tag == "" {
				return ErrInvalidState
			}
			if owner, ok := owners[tag]; ok && owner != entry.Value {
				return ErrInvalidState
			}
			owners[tag] = entry.Value
		}
	}
	for _, tag := range state.Tombstones {
		if tag == "" {
			return ErrInvalidState
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Check conflicts against local live tags before applying any input.
	for value, tags := range s.entries {
		for tag := range tags {
			if owner, ok := owners[tag]; ok && owner != value {
				return ErrInvalidState
			}
		}
	}
	if s.entries == nil {
		s.entries = make(map[T]map[string]struct{})
	}
	if s.tombstones == nil {
		s.tombstones = make(map[string]struct{})
	}
	for _, tag := range state.Tombstones {
		s.tombstones[tag] = struct{}{}
	}
	for _, entry := range state.Entries {
		for _, tag := range entry.Tags {
			if _, removed := s.tombstones[tag]; removed {
				continue
			}
			if s.entries[entry.Value] == nil {
				s.entries[entry.Value] = make(map[string]struct{})
			}
			s.entries[entry.Value][tag] = struct{}{}
		}
	}
	for value, tags := range s.entries {
		for tag := range tags {
			if _, removed := s.tombstones[tag]; removed {
				delete(tags, tag)
			}
		}
		if len(tags) == 0 {
			delete(s.entries, value)
		}
	}
	return nil
}
