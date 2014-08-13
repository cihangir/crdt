# CCRDT [![GoDoc](https://godoc.org/github.com/cihangir/ccrdt?status.svg)](https://godoc.org/github.com/cihangir/ccrdt) [![Build Status](https://travis-ci.org/cihangir/ccrdt.svg)](https://travis-ci.org/cihangir/ccrdt)

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
