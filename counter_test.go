package crdt

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func testClient(t *testing.T, count int) (*CRDT, []*miniredis.Miniredis) {
	t.Helper()
	servers := make([]string, count)
	replicas := make([]*miniredis.Miniredis, count)
	for i := range count {
		replicas[i] = miniredis.RunT(t)
		servers[i] = replicas[i].Addr()
	}
	c, err := NewContext(t.Context(), servers, WithPrefix(t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, replicas
}

func requireValue(t *testing.T, value, want int64, err error) {
	t.Helper()
	if err != nil || value != want {
		t.Fatalf("value = %d, %v; want %d, nil", value, err, want)
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, ErrNoServers) {
		t.Fatalf("empty servers: %v", err)
	}
	for _, servers := range [][]string{{""}, {"https://example.com"}, {"localhost:6379", "redis://localhost:6379"}} {
		if _, err := New(servers); err == nil {
			t.Fatalf("accepted invalid servers: %v", servers)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewContext(ctx, []string{"127.0.0.1:1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestGCounterDivergenceAndRepair(t *testing.T) {
	c, _ := testClient(t, 3)
	g := c.NewGCounter("visits")
	const large = int64(1<<53 + 1)
	for i, delta := range []int64{large, 5, 3} {
		g.writer = i
		if err := g.Add(delta); err != nil {
			t.Fatal(err)
		}
	}
	value, err := g.Query()
	requireValue(t, value, large+8, err)
	state, err := g.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		value, err = g.Merge()
		requireValue(t, value, large+8, err)
	}
	for _, replica := range c.replicas {
		fields, err := replica.backend.(*RedisBackend).client.HGetAll(t.Context(), g.key).Result()
		if err != nil || len(fields) != 3 {
			t.Fatalf("repair: %v, %v", fields, err)
		}
	}
	// Replaying an old snapshot must not lower the actor's current value.
	g.writer = 0
	if err := g.Add(2); err != nil {
		t.Fatal(err)
	}
	if err := g.MergeState(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	value, err = g.Merge()
	requireValue(t, value, large+10, err)
}

func TestGCounterConcurrentIncrementAndRepair(t *testing.T) {
	c, _ := testClient(t, 3)
	handles := make([]*GCounter, 3)
	for i := range handles {
		handles[i] = c.NewGCounter("parallel")
		handles[i].writer = i
	}
	var wg sync.WaitGroup
	for _, g := range handles {
		wg.Go(func() {
			for range 20 {
				if err := g.Add(1); err != nil {
					t.Error(err)
					return
				}
				if _, err := g.Merge(); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	value, err := handles[0].Merge()
	requireValue(t, value, 60, err)
}

func TestCounterErrors(t *testing.T) {
	t.Run("validation and overflow", func(t *testing.T) {
		c, _ := testClient(t, 1)
		g := c.NewGCounter("limits")
		if err := g.Add(-1); !errors.Is(err, ErrNegativeDelta) {
			t.Fatal(err)
		}
		if err := g.Add(0); err != nil {
			t.Fatal(err)
		}
		if err := g.Add(math.MaxInt64); err != nil {
			t.Fatal(err)
		}
		if err := g.Add(1); !errors.Is(err, ErrOverflow) {
			t.Fatalf("overflow: %v", err)
		}
		value, err := g.Merge()
		requireValue(t, value, math.MaxInt64, err)
		if err := g.MergeState(t.Context(), CounterState{"bad": -1}); !errors.Is(err, ErrInvalidState) {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := g.AddContext(ctx, 1); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := g.ValueContext(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := g.MergeContext(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err := g.MergeState(ctx, CounterState{"x": 1}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
	t.Run("failed replica", func(t *testing.T) {
		c, _ := testClient(t, 2)
		g := c.NewGCounter("partial")
		g.writer = 0
		if err := g.Add(4); err != nil {
			t.Fatal(err)
		}
		_ = c.replicas[1].backend.Close()
		value, err := g.Merge()
		if value != 4 || err == nil {
			t.Fatalf("partial result = %d, %v", value, err)
		}
		g.writer = 1
		if err := g.Add(1); err == nil {
			t.Fatal("closed writer accepted increment")
		}
		_ = c.replicas[0].backend.Close()
		if _, err := g.Value(); err == nil {
			t.Fatal("all failed reads hidden")
		}
	})
	t.Run("corruption", func(t *testing.T) {
		for _, raw := range []string{"-1", "abc", "01", "9223372036854775808"} {
			t.Run(raw, func(t *testing.T) {
				c, _ := testClient(t, 1)
				g := c.NewGCounter("corrupt")
				r := c.replicas[0]
				if err := r.backend.(*RedisBackend).client.HSet(t.Context(), g.key, r.id, raw).Err(); err != nil {
					t.Fatal(err)
				}
				if _, err := g.Snapshot(t.Context()); !errors.Is(err, ErrInvalidState) {
					t.Fatal(err)
				}
				if err := g.Add(1); !errors.Is(err, ErrInvalidState) {
					t.Fatalf("increment: %v", err)
				}
				if err := g.MergeState(t.Context(), CounterState{"new": 1}); !errors.Is(err, ErrInvalidState) {
					t.Fatalf("merge: %v", err)
				}
				if exists := r.backend.(*RedisBackend).client.HExists(t.Context(), g.key, "new").Val(); exists {
					t.Fatal("invalid merge partially applied")
				}
			})
		}
	})
}

func TestCounterNamespacesAndReconnect(t *testing.T) {
	c, servers := testClient(t, 1)
	g := c.NewGCounter("same")
	if err := g.Add(2); err != nil {
		t.Fatal(err)
	}
	// URL and plain address forms identify the same actor, across client lifetimes.
	other, err := NewContext(t.Context(), []string{"redis://" + servers[0].Addr()}, WithPrefix(c.prefix))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	if other.replicas[0].id != c.replicas[0].id {
		t.Fatal("unstable actor identity")
	}
	if err := other.NewGCounter("same").Add(3); err != nil {
		t.Fatal(err)
	}
	value, err := g.Value()
	requireValue(t, value, 5, err)
	isolated, err := NewContext(t.Context(), []string{servers[0].Addr()}, WithPrefix("other"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = isolated.Close() })
	value, err = isolated.NewGCounter("same").Value()
	requireValue(t, value, 0, err)
	value, err = c.NewPNCounter("same").Value()
	requireValue(t, value, 0, err)
}

func TestPNCounter(t *testing.T) {
	c, _ := testClient(t, 2)
	p := c.NewPNCounter("balance")
	for _, delta := range []int64{10, -3, 2, -20} {
		if err := p.Add(delta); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Increment(3); err != nil {
		t.Fatal(err)
	}
	if err := p.Decrement(2); err != nil {
		t.Fatal(err)
	}
	if err := p.IncrementContext(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if err := p.DecrementContext(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	value, err := p.Merge()
	requireValue(t, value, -10, err)
	if err := p.Decrement(-1); !errors.Is(err, ErrNegativeDelta) {
		t.Fatal(err)
	}
	if err := p.Add(math.MinInt64); !errors.Is(err, ErrOverflow) {
		t.Fatal(err)
	}
	state, err := p.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.MergeState(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	value, err = p.Query()
	requireValue(t, value, -10, err)
	// Large opposing totals must cancel before the range check.
	state = PNCounterState{Positive: CounterState{"a": math.MaxInt64, "b": math.MaxInt64}, Negative: CounterState{"a": math.MaxInt64}}
	value, err = state.Value()
	requireValue(t, value, math.MaxInt64, err)
	state.Negative = nil
	if _, err := state.Value(); !errors.Is(err, ErrOverflow) {
		t.Fatal(err)
	}
	// A malformed negative vector must not cause the positive vector to be applied.
	invalid := PNCounterState{Positive: CounterState{"unapplied": 99}, Negative: CounterState{"bad": -1}}
	if err := p.MergeState(t.Context(), invalid); !errors.Is(err, ErrInvalidState) {
		t.Fatal(err)
	}
	if _, err := invalid.Value(); !errors.Is(err, ErrInvalidState) {
		t.Fatal(err)
	}
	if _, err := invalid.Compare(state); !errors.Is(err, ErrInvalidState) {
		t.Fatal(err)
	}
	if merged, err := invalid.Merge(state); !errors.Is(err, ErrInvalidState) || merged.Positive != nil || merged.Negative != nil {
		t.Fatalf("invalid PN merge returned a usable partial state: %v, %v", merged, err)
	}
	a := PNCounterState{Positive: CounterState{"a": 3}, Negative: CounterState{"b": 2}}
	b, err := a.Merge(PNCounterState{Positive: CounterState{"b": 4}, Negative: CounterState{"b": 1}})
	if err != nil {
		t.Fatal(err)
	}
	value, err = b.Value()
	requireValue(t, value, 5, err)
	b.Positive["a"] = 100
	if a.Positive["a"] != 3 {
		t.Fatal("PN merge aliases input")
	}
}

func TestCounterState(t *testing.T) {
	for _, tc := range []struct {
		a, b  CounterState
		order Order
	}{
		{nil, CounterState{"a": 0}, Equal},
		{nil, CounterState{"a": 1}, Before},
		{CounterState{"a": 2}, CounterState{"a": 1}, After},
		{CounterState{"a": 1}, CounterState{"b": 1}, Concurrent},
	} {
		order, err := tc.a.Compare(tc.b)
		if err != nil || order != tc.order {
			t.Fatalf("Compare = %v, %v; want %v", order, err, tc.order)
		}
	}
	for _, invalid := range []CounterState{{"": 1}, {"a": -1}} {
		if _, err := invalid.Value(); !errors.Is(err, ErrInvalidState) {
			t.Fatal(err)
		}
		if _, err := invalid.Merge(nil); !errors.Is(err, ErrInvalidState) {
			t.Fatal(err)
		}
		if _, err := invalid.Compare(nil); !errors.Is(err, ErrInvalidState) {
			t.Fatal(err)
		}
	}
	if _, err := (CounterState{"a": math.MaxInt64, "b": 1}).Value(); !errors.Is(err, ErrOverflow) {
		t.Fatal(err)
	}
	a := CounterState{"a": 3}
	b, err := a.Merge(CounterState{"b": 2})
	if err != nil {
		t.Fatal(err)
	}
	b["a"] = 7
	if a["a"] != 3 {
		t.Fatal("merged state aliases input")
	}
	for _, tc := range []struct {
		a, b PNCounterState
		want Order
	}{
		{PNCounterState{}, PNCounterState{}, Equal},
		{PNCounterState{}, PNCounterState{Positive: a}, Before},
		{PNCounterState{Negative: a}, PNCounterState{}, After},
		{PNCounterState{Positive: a}, PNCounterState{Negative: a}, Concurrent},
	} {
		order, err := tc.a.Compare(tc.b)
		if err != nil || order != tc.want {
			t.Fatalf("PN Compare = %v, %v; want %v", order, err, tc.want)
		}
	}
}

func FuzzCounterMerge(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9})
	f.Fuzz(func(t *testing.T, data []byte) {
		states := []CounterState{{}, {}, {}}
		for i, value := range data {
			actor := fmt.Sprint(i % 5)
			states[i%3][actor] = int64(value)
		}
		join := func(a, b CounterState) CounterState {
			t.Helper()
			s, err := a.Merge(b)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}
		a, b, c := states[0], states[1], states[2]
		if !maps.Equal(join(a, b), join(b, a)) {
			t.Fatal("not commutative")
		}
		if !maps.Equal(join(join(a, b), c), join(a, join(b, c))) {
			t.Fatal("not associative")
		}
		if !maps.Equal(join(a, a), a) {
			t.Fatal("not idempotent")
		}
	})
}

// Run against independent real Redis instances via CRDT_REDIS_SERVERS=a:6379,b:6379.
// Each invocation has its own prefix and deletes only the keys it creates.
func TestRedisIntegration(t *testing.T) {
	addresses := os.Getenv("CRDT_REDIS_SERVERS")
	if addresses == "" {
		t.Skip("set CRDT_REDIS_SERVERS for real Redis integration")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	var backends []Backend
	for _, address := range strings.Split(addresses, ",") {
		backend, err := NewRedisBackend(ctx, address)
		if err != nil {
			for _, opened := range backends {
				_ = opened.Close()
			}
			t.Fatal(err)
		}
		backends = append(backends, backend)
	}
	c, err := NewWithBackends(backends, WithPrefix(fmt.Sprintf("crdt-test:%d", time.Now().UnixNano())))
	if err != nil {
		for _, opened := range backends {
			_ = opened.Close()
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	g := c.NewGCounter("precision")
	limit := c.NewGCounter("limit")
	pn := c.NewPNCounter("balance")
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		for _, r := range c.replicas {
			if err := r.backend.(*RedisBackend).client.Del(cleanupCtx, g.key, limit.key, pn.positive.key, pn.negative.key).Err(); err != nil {
				t.Error(err)
			}
		}
	})
	for i := range c.replicas {
		g.writer = i
		if err := g.AddContext(ctx, 1<<53+1); err != nil {
			t.Fatal(err)
		}
	}
	value, err := g.MergeContext(ctx)
	requireValue(t, value, int64(len(c.replicas))*(1<<53+1), err)
	for range 3 {
		value, err = g.MergeContext(ctx)
		requireValue(t, value, int64(len(c.replicas))*(1<<53+1), err)
	}
	if err := limit.AddContext(ctx, math.MaxInt64); err != nil {
		t.Fatal(err)
	}
	if err := limit.AddContext(ctx, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Redis overflow not detected: %v", err)
	}
	value, err = limit.MergeContext(ctx)
	requireValue(t, value, math.MaxInt64, err)
	for i := range c.replicas {
		pn.positive.writer, pn.negative.writer = i, i
		if err := pn.AddContext(ctx, 25); err != nil {
			t.Fatal(err)
		}
		if err := pn.AddContext(ctx, -8); err != nil {
			t.Fatal(err)
		}
	}
	value, err = pn.MergeContext(ctx)
	requireValue(t, value, int64(len(c.replicas))*17, err)
}
