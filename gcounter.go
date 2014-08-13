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

	errCount := 0
	var lastErr error

	// add goroutine support
	for _, c := range g.ccrdt.sessions.All() {

		// TODO we can do read-repair here
		// redis returns lastest value
		_, err := c.IncrBy(g.key, delta)
		if err != nil {
			lastErr = err
			errCount++
		}
	}

	// at least we have one success
	if errCount != g.ccrdt.sessions.Count() {
		return nil
	}

	return lastErr
}

// Merge returns the sum of all the actors
func (g *GCounter) Merge() (int64, error) {
	var res int64
	var repairNeeded bool
	values := make(map[*redis.RedisSession]int64)

	for i, c := range g.ccrdt.sessions.All() {
		val, err := c.Get(g.key)
		if err != nil && err != redis.ErrNil {
			// ignore the errors
			// return 0, err
		}

		if val == "" {
			val = "0"
		}

		d, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			// ignore the errors
			// return 0, err
		}

		// add data to a temp cache
		values[c] = d

		// if the `res`is smaller than the current value, previous ones should
		// be repaired
		if res < d {
			// if this is the first operation, ignore the case
			if i != 0 {
				repairNeeded = true
			}

			res = d
		}
	}

	if repairNeeded {
		for ses, per := range values {
			if res == per {
				continue
			}
			ses.IncrBy(g.key, res-per)
		}
	}

	return res, nil
}
