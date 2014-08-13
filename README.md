# CRDT [![GoDoc](https://godoc.org/github.com/cihangir/crdt?status.svg)](https://godoc.org/github.com/cihangir/crdt) [![Build Status](https://travis-ci.org/cihangir/crdt.svg)](https://travis-ci.org/cihangir/crdt)

WIP!

Convergent and Commutative Replicated Data Types


Counters
--------

### G-Counter

A G-Counter is a grow-only counter (inspired by vector clocks) in which only
increment and merge are possible. Divergent histories are resolved by taking the
maximum count for the counter.  The value of the counter is the sum of all
counts.


Implementation Differences form the original paper:

* Increment() Instead of incrementing one-by-one, in this package you can give increment count
* Query() <- this wonnt be implemented(at least for now), merge is doing the same thing
* Compare(a,b) <- this wonnt be implemented(at least for now), merge is doing the same thing
* Merge, merge is not merging two actors, it is fetching all the values from all the actors and compares them, if any of them is different fixes while reading

Things that can be done

* Instead of giving multiple backedn services into CCRDT New function, we can pass only one
act according to the original paper


