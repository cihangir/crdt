package crdt_test

import (
	"encoding/json/v2"
	"fmt"
	"slices"

	"github.com/cihangir/crdt"
)

func ExampleGSet() {
	var east, west crdt.GSet[string]
	if err := east.Add("apple"); err != nil {
		panic(err)
	}
	if err := west.Add("pear"); err != nil {
		panic(err)
	}
	if err := east.Merge(west.Snapshot()); err != nil {
		panic(err)
	}
	fmt.Println(slices.Sorted(east.Values()))
	// Output: [apple pear]
}

func ExampleTwoPhaseSet() {
	var set crdt.TwoPhaseSet[string]
	if err := set.Add("retired-id"); err != nil {
		panic(err)
	}
	set.Remove("retired-id")
	fmt.Println(set.Contains("retired-id"))
	fmt.Println(set.Add("retired-id"))
	// Output:
	// false
	// crdt: a two-phase set cannot re-add a removed element
}

func ExampleORSet() {
	var phone, laptop crdt.ORSet[string]
	if err := phone.Add("milk"); err != nil {
		panic(err)
	}
	if err := laptop.Merge(phone.Snapshot()); err != nil {
		panic(err)
	}
	phone.Remove("milk")
	// The laptop has not observed the removal. Its fresh add survives it.
	if err := laptop.Add("milk"); err != nil {
		panic(err)
	}
	if err := phone.Merge(laptop.Snapshot()); err != nil {
		panic(err)
	}
	fmt.Println(phone.Contains("milk"))
	// Output: true
}

func ExampleLWWRegister() {
	var a, b crdt.LWWRegister[string]
	if err := a.Assign("draft"); err != nil {
		panic(err)
	}
	if err := b.Merge(a.Snapshot()); err != nil {
		panic(err)
	}
	if err := b.Assign("published"); err != nil {
		panic(err)
	}
	if err := a.Merge(b.Snapshot()); err != nil {
		panic(err)
	}
	value, assigned := a.Value()
	fmt.Println(value, assigned)
	// Output: published true
}

func ExampleCounterState() {
	east := crdt.CounterState{"east": 3}
	west := crdt.CounterState{"west": 5}
	merged, err := east.Merge(west)
	if err != nil {
		panic(err)
	}
	value, err := merged.Value()
	if err != nil {
		panic(err)
	}
	fmt.Println(value)
	// Output: 8
}

func ExampleORSet_Snapshot() {
	var source, destination crdt.ORSet[string]
	if err := source.Add("shared"); err != nil {
		panic(err)
	}
	wire, err := json.Marshal(source.Snapshot())
	if err != nil {
		panic(err)
	}
	var snapshot crdt.ORSetState[string]
	if err := json.Unmarshal(wire, &snapshot); err != nil {
		panic(err)
	}
	if err := destination.Merge(snapshot); err != nil {
		panic(err)
	}
	fmt.Println(destination.Contains("shared"))
	// Output: true
}

func ExampleNewWithBackends() {
	east, err := crdt.NewMemoryBackend("east")
	if err != nil {
		panic(err)
	}
	west, err := crdt.NewMemoryBackend("west")
	if err != nil {
		panic(err)
	}
	client, err := crdt.NewWithBackends([]crdt.Backend{east, west}, crdt.WithPrefix("example"))
	if err != nil {
		east.Close()
		west.Close()
		panic(err)
	}
	defer client.Close()
	visits := client.NewGCounter("visits")
	if err := visits.Add(3); err != nil {
		panic(err)
	}
	value, err := visits.Merge()
	if err != nil {
		panic(err)
	}
	fmt.Println(value)
	balance := client.NewPNCounter("balance")
	if err := balance.Add(-2); err != nil {
		panic(err)
	}
	value, err = balance.Merge()
	if err != nil {
		panic(err)
	}
	fmt.Println(value)
	// Output:
	// 3
	// -2
}
