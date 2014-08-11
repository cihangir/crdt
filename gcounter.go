package ccrdt

import (
	"strconv"

	"github.com/koding/redis"
)

// GCounter is a grow-only counter (inspired by vector clocks) in which only
// increment and merge are possible. Divergent histories are resolved by taking
// the maximum count for the counter.  The value of the counter is the sum of
// all counts.
//
// TODO implement merge!!
type GCounter struct {
	ccrdt *CCRDT
	key   string
}

// NewGCounter creates a new GCounter
func (c *CCRDT) NewGCounter(key string) *GCounter {
	return &GCounter{
		ccrdt: c,
		key:   key,
	}
}

// Add adds item to the GCounter with a given delta
func (g *GCounter) Add(delta int64) error {
	_, err := g.ccrdt.sessions.One().IncrBy(g.key, delta)
	return err
}

// Sum returns the sum of all the actors
func (g *GCounter) Sum() (int64, error) {
	var res int64
	for _, c := range g.ccrdt.sessions.All() {
		val, err := c.Get(g.key)
		if err != nil && err != redis.ErrNil {
			return 0, err
		}

		if val == "" {
			val = "0"
		}
		i, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return 0, err
		}

		res += i
	}

	return res, nil
}
