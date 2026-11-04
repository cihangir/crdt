package crdt

import "testing"

func BenchmarkCounterStateMerge(b *testing.B) {
	left := CounterState{"a": 10, "b": 20, "c": 30}
	right := CounterState{"a": 15, "b": 10, "d": 40}
	for b.Loop() {
		if _, err := left.Merge(right); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGSetMerge(b *testing.B) {
	var source GSet[int]
	for i := range 1000 {
		if err := source.Add(i); err != nil {
			b.Fatal(err)
		}
	}
	snapshot := source.Snapshot()
	var target GSet[int]
	for b.Loop() {
		if err := target.Merge(snapshot); err != nil {
			b.Fatal(err)
		}
	}
}
