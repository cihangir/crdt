package ccrdt

import (
	"strconv"

	"github.com/koding/redis"
)

type GCounter struct {
	ccrdt *CCRDT
	key   string
}

func (c *CCRDT) NewGCounter(key string) *GCounter {
	return &GCounter{
		ccrdt: c,
		key:   key,
	}
}

func (g *GCounter) Add(delta int64) error {
	_, err := g.ccrdt.sessions.One().Incrby(g.key, delta)
	return err
}

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
