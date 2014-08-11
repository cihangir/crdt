# CCRDT [![GoDoc](https://godoc.org/github.com/cihangir/ccrdt?status.png)](https://godoc.org/github.com/cihangir/ccrdt) [![Build Status](https://travis-ci.org/cihangir/ccrdt.png)](https://travis-ci.org/cihangir/ccrdt)
=====

Convergent and Commutative Replicated Data Types


Counters
--------

### G-Counter

A G-Counter is a grow-only counter (inspired by vector clocks) in which only
increment and merge are possible. Divergent histories are resolved by taking the
maximum count for the counter.  The value of the counter is the sum of all
counts.
