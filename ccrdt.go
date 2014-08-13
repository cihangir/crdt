// Package ccrdt provides Convergent and Commutative Replicated Data Types
package ccrdt

import (
	"math/rand"
	"sync"
	"time"

	"github.com/koding/redis"
)

// CCRDTPrefix is the redis connection prefix for all connections
var CCRDTPrefix = "ccrdt"

type sessions struct {
	sessions []*redis.RedisSession

	randomSource *rand.Rand
	// lock for sessions
	mu sync.Mutex
}

func (s *sessions) Connect(server string) error {
	redis, err := redis.NewRedisSession(&redis.RedisConf{Server: server})
	if err != nil {
		return err
	}
	redis.SetPrefix(CCRDTPrefix)
	s.Add(redis)

	return nil
}

func (s *sessions) Add(session *redis.RedisSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = append(s.sessions, session)
}

// One returns a random connection
func (s *sessions) One() *redis.RedisSession {
	// s.mu.Lock()
	// defer s.mu.Unlock()
	return s.sessions[s.randomSource.Intn(len(s.sessions))]
}

// All returns all connections
func (s *sessions) All() []*redis.RedisSession {
	// s.mu.Lock()
	// defer s.mu.Unlock()
	return s.sessions
}

// Count returns backend service count
func (s *sessions) Count() int {
	// s.mu.Lock()
	// defer s.mu.Unlock()
	return len(s.sessions)
}

// New creates a new CCRDT system and its backend connections
func New(servers []string) (*CCRDT, error) {
	c := &CCRDT{
		sessions: &sessions{
			randomSource: rand.New(
				rand.NewSource(time.Now().UnixNano()),
			),
		},
	}

	// sync connections
	var wg sync.WaitGroup

	// TODO add lock for err
	var err error

	// try to connect to all servers
	for _, s := range servers {
		wg.Add(1)
		go func(server string) {
			defer wg.Done()
			// return early if any of the previous connections returned err
			if err != nil {
				return
			}

			if errc := c.sessions.Connect(server); errc != nil {
				err = errc
				return
			}
		}(s)
	}
	wg.Wait()

	if err != nil {
		return nil, err
	}

	return c, nil
}

// CCRDT holds the required data for CCRDT systems
type CCRDT struct {
	// main redis connections
	sessions *sessions

	randomSource *rand.Rand
}
