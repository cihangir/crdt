package crdt

import (
	"errors"
	"math"
	"sync"
	"testing"
)

func TestLWWRegister(t *testing.T) {
	var a, b LWWRegister[string]
	if value, ok := a.Value(); ok || value != "" {
		t.Fatal("nonempty zero register")
	}
	if err := a.Merge(LWWRegisterState[string]{}); err != nil {
		t.Fatal(err)
	}
	if err := a.Assign("first"); err != nil {
		t.Fatal(err)
	}
	first := a.Snapshot()
	if err := b.Merge(first); err != nil {
		t.Fatal(err)
	}
	if err := b.Assign("later"); err != nil {
		t.Fatal(err)
	}
	later := b.Snapshot()
	if err := a.Merge(later); err != nil {
		t.Fatal(err)
	}
	if err := a.Merge(first); err != nil {
		t.Fatal(err)
	}
	if value, ok := a.Value(); !ok || value != "later" {
		t.Fatal(value, ok)
	}
	// Equal logical times resolve by actor ID, regardless of delivery order.
	left := LWWRegisterState[string]{Value: "left", Clock: 10, Actor: "a"}
	right := LWWRegisterState[string]{Value: "right", Clock: 10, Actor: "b"}
	for _, state := range []LWWRegisterState[string]{left, right, left, right} {
		if err := a.Merge(state); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []LWWRegisterState[string]{right, left, right, left} {
		if err := b.Merge(state); err != nil {
			t.Fatal(err)
		}
	}
	if a.Snapshot() != b.Snapshot() {
		t.Fatal("concurrent assignments did not converge")
	}
	if err := a.Assign("causally later"); err != nil {
		t.Fatal(err)
	}
	if a.Snapshot().Clock != 11 {
		t.Fatal("did not advance beyond observed clock")
	}
	for _, invalid := range []LWWRegisterState[string]{{Clock: 0, Actor: "x"}, {Clock: 1}} {
		if err := a.Merge(invalid); !errors.Is(err, ErrInvalidState) {
			t.Fatal(err)
		}
	}
	if err := a.Merge(LWWRegisterState[string]{Clock: math.MaxUint64, Actor: "limit"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Assign("overflow"); !errors.Is(err, ErrOverflow) {
		t.Fatal(err)
	}
	// T need not support equality. Duplicate NaN deliveries must be harmless.
	var special LWWRegister[float64]
	if err := special.Assign(math.NaN()); err != nil {
		t.Fatal(err)
	}
	if err := special.Merge(special.Snapshot()); err != nil {
		t.Fatal(err)
	}
}

func TestLWWRegisterConcurrent(t *testing.T) {
	var register LWWRegister[int]
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			if err := register.Assign(i); err != nil {
				t.Error(err)
			}
			if err := register.Merge(register.Snapshot()); err != nil {
				t.Error(err)
			}
			register.Value()
		})
	}
	wg.Wait()
	if register.Snapshot().Clock != 100 {
		t.Fatal("lost assignment")
	}
}

func FuzzRegisterConvergence(f *testing.F) {
	f.Add(uint64(1), uint64(2), uint64(2))
	f.Fuzz(func(t *testing.T, a, b, c uint64) {
		states := []LWWRegisterState[int]{{Value: 1, Clock: a | 1, Actor: "a"}, {Value: 2, Clock: b | 1, Actor: "b"}, {Value: 3, Clock: c | 1, Actor: "c"}}
		var expected LWWRegisterState[int]
		for i, order := range [][]int{{0, 1, 2, 0}, {2, 1, 0, 2}, {1, 2, 0, 1}} {
			var r LWWRegister[int]
			for _, n := range order {
				if err := r.Merge(states[n]); err != nil {
					t.Fatal(err)
				}
			}
			if i == 0 {
				expected = r.Snapshot()
			} else if r.Snapshot() != expected {
				t.Fatal("register did not converge")
			}
		}
	})
}
