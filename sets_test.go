package crdt

import (
	"encoding/json/v2"
	"errors"
	"math"
	"reflect"
	"slices"
	"sync"
	"testing"
)

func TestGSet(t *testing.T) {
	var a, b GSet[string]
	for _, value := range []string{"apple", "pear", "apple"} {
		if err := a.Add(value); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Add("orange"); err != nil {
		t.Fatal(err)
	}
	if err := b.Merge(a.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if !b.Contains("apple") || b.Contains("banana") || b.Len() != 3 {
		t.Fatal("incorrect membership")
	}
	values := slices.Sorted(b.Values())
	if !slices.Equal(values, []string{"apple", "orange", "pear"}) {
		t.Fatal(values)
	}
	snapshot := a.Snapshot()
	snapshot[0] = "corrupted"
	if a.Contains("corrupted") {
		t.Fatal("snapshot aliases live state")
	}
	// The iterator holds no mutex across caller code.
	for value := range b.Values() {
		if err := b.Add(value + "!"); err != nil {
			t.Fatal(err)
		}
	}
	if b.Len() != 6 {
		t.Fatal(b.Len())
	}
}

func TestTwoPhaseSet(t *testing.T) {
	var a, b TwoPhaseSet[string]
	if a.Remove("absent") {
		t.Fatal("removed absent value")
	}
	if err := a.Add("x"); err != nil {
		t.Fatal(err)
	}
	stale := a.Snapshot()
	if err := b.Merge(stale); err != nil {
		t.Fatal(err)
	}
	if !a.Remove("x") || a.Remove("x") {
		t.Fatal("incorrect remove result")
	}
	if err := b.Add("x"); err != nil {
		t.Fatal(err)
	}
	if err := b.Merge(a.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err := b.Merge(stale); err != nil {
		t.Fatal(err)
	}
	if b.Contains("x") || b.Len() != 0 || len(slices.Collect(b.Values())) != 0 {
		t.Fatal("removed value resurrected")
	}
	if err := b.Add("x"); !errors.Is(err, ErrRemoved) {
		t.Fatal(err)
	}
	if err := b.Add("y"); err != nil {
		t.Fatal(err)
	}
	if b.Len() != 1 || !slices.Equal(slices.Collect(b.Values()), []string{"y"}) {
		t.Fatal("visible elements")
	}
	snapshot := b.Snapshot()
	snapshot.Adds[0] = "corrupt"
	if b.Contains("corrupt") {
		t.Fatal("snapshot aliases live state")
	}
	if err := b.Merge(TwoPhaseSetState[string]{Adds: []string{"z"}, Removes: []string{"unknown"}}); !errors.Is(err, ErrInvalidState) {
		t.Fatal(err)
	}
	if b.Contains("z") {
		t.Fatal("invalid merge partially applied")
	}
}

func TestORSet(t *testing.T) {
	var a, b ORSet[string]
	if a.Remove("absent") {
		t.Fatal("removed absent value")
	}
	if err := a.Add("x"); err != nil {
		t.Fatal(err)
	}
	stale := a.Snapshot()
	if err := b.Merge(stale); err != nil {
		t.Fatal(err)
	}
	// b's fresh add is concurrent with a's remove and must survive it.
	if err := b.Add("x"); err != nil {
		t.Fatal(err)
	}
	if !a.Remove("x") || a.Remove("x") {
		t.Fatal("incorrect remove result")
	}
	removed := a.Snapshot()
	if err := a.Merge(b.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err := b.Merge(removed); err != nil {
		t.Fatal(err)
	}
	if !a.Contains("x") || !b.Contains("x") {
		t.Fatal("concurrent add lost")
	}
	if !a.Remove("x") {
		t.Fatal("remove failed")
	}
	if err := b.Merge(a.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err := b.Merge(stale); err != nil {
		t.Fatal(err)
	}
	if b.Contains("x") || b.Len() != 0 {
		t.Fatal("stale snapshot resurrected value")
	}
	if err := b.Add("x"); err != nil {
		t.Fatal(err)
	}
	if !b.Contains("x") || b.Len() != 1 || !slices.Equal(slices.Collect(b.Values()), []string{"x"}) {
		t.Fatal("re-add failed")
	}
	snapshot := b.Snapshot()
	snapshot.Entries[0].Tags[0] = "corrupt"
	for _, tag := range b.Snapshot().Entries[0].Tags {
		if tag == "corrupt" {
			t.Fatal("tags alias live state")
		}
	}
	snapshot.Tombstones[0] = "corrupt"
	if slices.Contains(b.Snapshot().Tombstones, "corrupt") {
		t.Fatal("tombstones alias live state")
	}
	liveTag := b.Snapshot().Entries[0].Tags[0]
	for _, invalid := range []ORSetState[string]{
		{Entries: []ORSetEntry[string]{{Value: "bad", Tags: []string{""}}}},
		{Entries: []ORSetEntry[string]{{Value: "bad"}}},
		{Entries: []ORSetEntry[string]{{Value: "bad", Tags: []string{liveTag}}}},
		{Entries: []ORSetEntry[string]{{Value: "one", Tags: []string{"same"}}, {Value: "two", Tags: []string{"same"}}}},
		{Entries: []ORSetEntry[string]{{Value: "valid", Tags: []string{"unique"}}}, Tombstones: []string{""}},
	} {
		if err := b.Merge(invalid); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("invalid state accepted: %v", err)
		}
		if b.Len() != 1 {
			t.Fatal("invalid merge partially applied")
		}
	}
}

func TestInvalidSetElements(t *testing.T) {
	var g GSet[any]
	var p TwoPhaseSet[any]
	var o ORSet[any]
	for _, value := range []any{math.NaN(), []int{1}, struct{ N float64 }{math.NaN()}, struct{ Key any }{[]int{1}}} {
		for _, add := range []func(any) error{g.Add, p.Add, o.Add} {
			if err := add(value); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("accepted invalid element: %v", err)
			}
		}
		if g.Contains(value) || p.Contains(value) || o.Contains(value) || p.Remove(value) || o.Remove(value) {
			t.Fatal("invalid membership")
		}
		if err := g.Merge([]any{"valid", value}); !errors.Is(err, ErrInvalidState) {
			t.Fatal(err)
		}
		if err := p.Merge(TwoPhaseSetState[any]{Adds: []any{value}}); !errors.Is(err, ErrInvalidState) {
			t.Fatal(err)
		}
		if err := o.Merge(ORSetState[any]{Entries: []ORSetEntry[any]{{Value: value, Tags: []string{"tag"}}}}); !errors.Is(err, ErrInvalidState) {
			t.Fatal(err)
		}
	}
	if g.Len() != 0 || p.Len() != 0 || o.Len() != 0 {
		t.Fatal("invalid input changed set")
	}
	if err := g.Add(nil); err != nil {
		t.Fatal(err)
	}
	if !g.Contains(nil) {
		t.Fatal("nil interface is a valid element")
	}
}

func TestConcurrentSets(t *testing.T) {
	var g GSet[int]
	var p TwoPhaseSet[int]
	var o ORSet[int]
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Go(func() {
			if err := g.Add(i); err != nil {
				t.Error(err)
			}
			if err := p.Add(i); err != nil {
				t.Error(err)
			}
			if err := o.Add(i); err != nil {
				t.Error(err)
			}
			_ = g.Contains(i)
			_ = p.Contains(i)
			_ = o.Contains(i)
			if err := g.Merge(g.Snapshot()); err != nil {
				t.Error(err)
			}
			if err := p.Merge(p.Snapshot()); err != nil {
				t.Error(err)
			}
			if err := o.Merge(o.Snapshot()); err != nil {
				t.Error(err)
			}
			if i%2 == 0 {
				p.Remove(i)
				o.Remove(i)
			}
		})
	}
	wg.Wait()
	if g.Len() != 30 || p.Len() != 15 || o.Len() != 15 {
		t.Fatalf("set sizes = %d, %d, %d", g.Len(), p.Len(), o.Len())
	}
}

func canonicalOR(state ORSetState[int]) ORSetState[int] {
	slices.Sort(state.Tombstones)
	for i := range state.Entries {
		slices.Sort(state.Entries[i].Tags)
	}
	slices.SortFunc(state.Entries, func(a, b ORSetEntry[int]) int { return a.Value - b.Value })
	return state
}

func FuzzSetConvergence(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 256 {
			data = data[:256]
		}
		g := [3]*GSet[int]{{}, {}, {}}
		p := [3]*TwoPhaseSet[int]{{}, {}, {}}
		o := [3]*ORSet[int]{{}, {}, {}}
		for i, op := range data {
			n, value := i%3, int(op%8)
			switch op % 3 {
			case 0:
				if err := g[n].Add(value); err != nil {
					t.Fatal(err)
				}
				// A permanently removed value cannot be added to a two-phase set.
				if err := p[n].Add(value); err != nil && !errors.Is(err, ErrRemoved) {
					t.Fatal(err)
				}
				if err := o[n].Add(value); err != nil {
					t.Fatal(err)
				}
			case 1:
				p[n].Remove(value)
				o[n].Remove(value)
			case 2:
				if err := g[n].Merge(g[(n+1)%3].Snapshot()); err != nil {
					t.Fatal(err)
				}
				if err := p[n].Merge(p[(n+1)%3].Snapshot()); err != nil {
					t.Fatal(err)
				}
				if err := o[n].Merge(o[(n+1)%3].Snapshot()); err != nil {
					t.Fatal(err)
				}
			}
		}
		// Deliver the same snapshots in different orders and with duplicates.
		gs := [3][]int{g[0].Snapshot(), g[1].Snapshot(), g[2].Snapshot()}
		ps := [3]TwoPhaseSetState[int]{p[0].Snapshot(), p[1].Snapshot(), p[2].Snapshot()}
		os := [3]ORSetState[int]{o[0].Snapshot(), o[1].Snapshot(), o[2].Snapshot()}
		var expectedG []int
		var expectedP TwoPhaseSetState[int]
		var expectedO ORSetState[int]
		for i, order := range [][]int{{0, 1, 2, 0}, {2, 1, 0, 2}, {1, 0, 2, 1}} {
			var a GSet[int]
			var b TwoPhaseSet[int]
			var c ORSet[int]
			for _, n := range order {
				if err := a.Merge(gs[n]); err != nil {
					t.Fatal(err)
				}
				if err := b.Merge(ps[n]); err != nil {
					t.Fatal(err)
				}
				if err := c.Merge(os[n]); err != nil {
					t.Fatal(err)
				}
			}
			ag := slices.Sorted(a.Values())
			bp := b.Snapshot()
			slices.Sort(bp.Adds)
			slices.Sort(bp.Removes)
			co := canonicalOR(c.Snapshot())
			if i == 0 {
				expectedG, expectedP, expectedO = ag, bp, co
			} else if !slices.Equal(ag, expectedG) || !reflect.DeepEqual(bp, expectedP) || !reflect.DeepEqual(co, expectedO) {
				t.Fatal("sets did not converge")
			}
		}
	})
}

func TestSnapshotJSONRoundTrip(t *testing.T) {
	var a, b ORSet[string]
	if err := a.Add("x"); err != nil {
		t.Fatal(err)
	}
	if err := a.Add("removed"); err != nil {
		t.Fatal(err)
	}
	a.Remove("removed")
	encoded, err := json.Marshal(a.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var state ORSetState[string]
	if err := json.Unmarshal(encoded, &state); err != nil {
		t.Fatal(err)
	}
	if err := b.Merge(state); err != nil {
		t.Fatal(err)
	}
	if !b.Contains("x") || b.Contains("removed") {
		t.Fatal("JSON round trip lost state")
	}
}
