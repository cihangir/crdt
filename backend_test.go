package crdt

import (
	"context"
	"errors"
	"maps"
	"math"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func memoryBackend(t *testing.T, id string) *MemoryBackend {
	t.Helper()
	backend, err := NewMemoryBackend(id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	return backend
}

// The same contract suite is run against both real implementations. A storage
// adapter must preserve the CRDT's atomicity and snapshot semantics, not just
// implement the interface's method signatures.
func TestBackendContract(t *testing.T) {
	factories := map[string]func(*testing.T) Backend{
		"memory": func(t *testing.T) Backend { return memoryBackend(t, "memory-actor") },
		"redis": func(t *testing.T) Backend {
			server := miniredis.RunT(t)
			backend, err := NewRedisBackend(t.Context(), server.Addr())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = backend.Close() })
			return backend
		},
	}
	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			b := factory(t)
			if b.ID() == "" {
				t.Fatal("empty actor ID")
			}
			state, err := b.Snapshot(t.Context(), "missing")
			if err != nil || len(state) != 0 {
				t.Fatal(state, err)
			}
			if err := b.Increment(t.Context(), "counter", 0); err != nil {
				t.Fatal(err)
			}
			const large = int64(1<<53 + 1)
			if err := b.Increment(t.Context(), "counter", large); err != nil {
				t.Fatal(err)
			}
			state, err = b.Snapshot(t.Context(), "counter")
			if err != nil || !maps.Equal(state, CounterState{b.ID(): large}) {
				t.Fatal(state, err)
			}
			state[b.ID()] = 0
			input := CounterState{b.ID(): 1, "other": 5}
			if err := b.Merge(t.Context(), "counter", input); err != nil {
				t.Fatal(err)
			}
			input["other"] = 100
			state, err = b.Snapshot(t.Context(), "counter")
			if err != nil || !maps.Equal(state, CounterState{b.ID(): large, "other": 5}) {
				t.Fatalf("snapshot or merge aliases state: %v %v", state, err)
			}
			for range 2 {
				if err := b.Merge(t.Context(), "counter", CounterState{"other": 3}); err != nil {
					t.Fatal(err)
				}
			}
			if err := b.Increment(t.Context(), "counter", 2); err != nil {
				t.Fatal(err)
			}
			state, err = b.Snapshot(t.Context(), "counter")
			if err != nil || !maps.Equal(state, CounterState{b.ID(): large + 2, "other": 5}) {
				t.Fatal(state, err)
			}
			if err := b.Increment(t.Context(), "counter", -1); !errors.Is(err, ErrNegativeDelta) {
				t.Fatal(err)
			}
			if err := b.Merge(t.Context(), "counter", CounterState{"unapplied": 9, "bad": -1}); !errors.Is(err, ErrInvalidState) {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := b.Increment(ctx, "counter", 1); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := b.Merge(ctx, "counter", CounterState{"unapplied": 9}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := b.Snapshot(ctx, "counter"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			after, err := b.Snapshot(t.Context(), "counter")
			if err != nil || !maps.Equal(after, state) {
				t.Fatalf("rejected operation changed state: %v %v", after, err)
			}
			if err := b.Merge(t.Context(), "empty", nil); err != nil {
				t.Fatal(err)
			}
			if err := b.Increment(t.Context(), "limit", math.MaxInt64); err != nil {
				t.Fatal(err)
			}
			if err := b.Increment(t.Context(), "limit", 1); !errors.Is(err, ErrOverflow) {
				t.Fatal(err)
			}
			limit, err := b.Snapshot(t.Context(), "limit")
			if err != nil || limit[b.ID()] != math.MaxInt64 {
				t.Fatal(limit, err)
			}
			// Repairs racing with increments cannot clobber them.
			var wg sync.WaitGroup
			for range 3 {
				wg.Go(func() {
					for range 20 {
						if err := b.Increment(t.Context(), "race", 1); err != nil {
							t.Error(err)
							return
						}
						if err := b.Merge(t.Context(), "race", CounterState{b.ID(): 1, "other": 7}); err != nil {
							t.Error(err)
							return
						}
					}
				})
			}
			wg.Wait()
			state, err = b.Snapshot(t.Context(), "race")
			if err != nil || !maps.Equal(state, CounterState{b.ID(): 60, "other": 7}) {
				t.Fatal(state, err)
			}
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
			if err := b.Close(); err != nil {
				t.Fatal("Close is not idempotent", err)
			}
			if err := b.Increment(t.Context(), "counter", 1); !errors.Is(err, ErrClosed) {
				t.Fatal(err)
			}
			if err := b.Increment(t.Context(), "counter", 0); !errors.Is(err, ErrClosed) {
				t.Fatal("zero increment hid a closed backend", err)
			}
			if err := b.Merge(t.Context(), "counter", CounterState{"other": 8}); !errors.Is(err, ErrClosed) {
				t.Fatal(err)
			}
			if err := b.Merge(t.Context(), "counter", nil); !errors.Is(err, ErrClosed) {
				t.Fatal("empty merge hid a closed backend", err)
			}
			if _, err := b.Snapshot(t.Context(), "counter"); !errors.Is(err, ErrClosed) {
				t.Fatal(err)
			}
		})
	}
}

func TestConfigurableCounterBackends(t *testing.T) {
	factories := map[string]func(*testing.T) []Backend{
		"memory": func(t *testing.T) []Backend { return []Backend{memoryBackend(t, "east"), memoryBackend(t, "west")} },
		"mixed": func(t *testing.T) []Backend {
			redis, err := NewRedisBackend(t.Context(), miniredis.RunT(t).Addr())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = redis.Close() })
			return []Backend{memoryBackend(t, "east"), redis}
		},
	}
	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			backends := factory(t)
			c, err := NewWithBackends(backends, WithPrefix("configurable"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = c.Close() })
			// Caller modification of the constructor's input slice cannot change c.
			saved := append([]Backend(nil), backends...)
			backends[0] = nil
			g := c.NewGCounter("same")
			g.writer = 0
			if err := g.Add(2); err != nil {
				t.Fatal(err)
			}
			g.writer = 1
			if err := g.Add(5); err != nil {
				t.Fatal(err)
			}
			value, err := g.Merge()
			requireValue(t, value, 7, err)
			for _, b := range saved {
				state, err := b.Snapshot(t.Context(), g.key)
				if err != nil || !maps.Equal(state, CounterState{saved[0].ID(): 2, saved[1].ID(): 5}) {
					t.Fatal(state, err)
				}
			}
			pn := c.NewPNCounter("same")
			pn.positive.writer = 0
			pn.negative.writer = 1
			if err := pn.Add(10); err != nil {
				t.Fatal(err)
			}
			if err := pn.Add(-3); err != nil {
				t.Fatal(err)
			}
			value, err = pn.Merge()
			requireValue(t, value, 7, err)
			// Counter families and names remain separate with every backend type.
			value, err = c.NewGCounter("other").Value()
			requireValue(t, value, 0, err)
			state, err := pn.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := pn.MergeState(t.Context(), state); err != nil {
				t.Fatal(err)
			}
			if err := saved[1].Close(); err != nil {
				t.Fatal(err)
			}
			value, err = g.Merge()
			if value != 7 || !errors.Is(err, ErrClosed) {
				t.Fatalf("partial result = %v, %v", value, err)
			}
			g.writer = 1
			if err := g.Add(0); !errors.Is(err, ErrClosed) {
				t.Fatal("zero counter increment hid a closed backend", err)
			}
			if err := g.MergeState(t.Context(), nil); !errors.Is(err, ErrClosed) {
				t.Fatal("empty counter merge hid a closed backend", err)
			}
		})
	}
}

type observedBackend struct {
	Backend
	id       string
	closes   int
	closeErr error
	snapshot CounterState
}

func (b *observedBackend) ID() string   { return b.id }
func (b *observedBackend) Close() error { b.closes++; return b.closeErr }
func (b *observedBackend) Snapshot(ctx context.Context, key string) (CounterState, error) {
	if b.snapshot != nil {
		return b.snapshot, nil
	}
	return b.Backend.Snapshot(ctx, key)
}

func TestBackendConstructionAndOwnership(t *testing.T) {
	if _, err := NewMemoryBackend(""); !errors.Is(err, ErrInvalidBackend) {
		t.Fatal(err)
	}
	if _, err := NewWithBackends(nil); !errors.Is(err, ErrNoBackends) {
		t.Fatal(err)
	}
	var typedNil *MemoryBackend
	first := &observedBackend{Backend: memoryBackend(t, "first"), id: "first"}
	duplicate := &observedBackend{Backend: memoryBackend(t, "duplicate"), id: "first"}
	empty := &observedBackend{Backend: memoryBackend(t, "empty"), id: ""}
	for _, backends := range [][]Backend{{nil}, {typedNil}, {first, nil}, {first, duplicate}, {empty}} {
		if _, err := NewWithBackends(backends); !errors.Is(err, ErrInvalidBackend) {
			t.Fatal(err)
		}
	}
	if first.closes != 0 || duplicate.closes != 0 || empty.closes != 0 {
		t.Fatal("failed construction took ownership")
	}
	closeFailure := errors.New("close failed")
	first.closeErr = closeFailure
	c, err := NewWithBackends([]Backend{first})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if err := c.Close(); !errors.Is(err, closeFailure) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if first.closes != 1 {
		t.Fatal("backend closed more than once")
	}
}

func TestCustomBackendInvalidSnapshot(t *testing.T) {
	b := &observedBackend{Backend: memoryBackend(t, "custom"), id: "custom", snapshot: CounterState{"bad": -1}}
	c, err := NewWithBackends([]Backend{b})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.NewGCounter("counter").Snapshot(t.Context()); !errors.Is(err, ErrInvalidState) {
		t.Fatal(err)
	}
}

func TestRedisBackendConstruction(t *testing.T) {
	for _, address := range []string{"", "https://example.com"} {
		if _, err := NewRedisBackend(t.Context(), address); err == nil {
			t.Fatal("accepted invalid address")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewRedisBackend(ctx, "127.0.0.1:1"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRedisErrorClassification(t *testing.T) {
	for _, message := range []string{"transport buffer overflow", "connection lost: CRDT_OVERFLOW", "read error: CRDT_INVALID_STATE"} {
		original := errors.New(message)
		if err := counterError(original); err != original {
			t.Fatalf("classified a non-Redis error as a CRDT error: %v", err)
		}
	}
}
