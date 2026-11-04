package crdt

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/redis/go-redis/v9"
)

// RedisBackend stores one actor's counter vectors as Redis hashes. Its methods
// are safe for concurrent use. Do not copy it; use NewRedisBackend to construct it.
// Use a stable address/database identity and configure Redis persistence without
// key eviction or expiration. Increments disable automatic retries and failover.
type RedisBackend struct {
	id        string
	client    *redis.Client
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

var _ Backend = (*RedisBackend)(nil)

// NewRedisBackend connects to and pings a Redis actor. Address may be host:port
// or a redis:// / rediss:// URL with credentials and a database. Failed
// construction releases its connections. Pass it to NewWithBackends on success.
func NewRedisBackend(ctx context.Context, address string) (*RedisBackend, error) {
	backend, err := newRedisBackend(address)
	if err != nil {
		return nil, err
	}
	if err := backend.client.Ping(ctx).Err(); err != nil {
		_ = backend.Close()
		return nil, fmt.Errorf("crdt: connect Redis backend: %w", err)
	}
	return backend, nil
}

func newRedisBackend(address string) (*RedisBackend, error) {
	var opt *redis.Options
	var err error
	if strings.Contains(address, "://") {
		opt, err = redis.ParseURL(address)
	} else if strings.TrimSpace(address) == "" {
		err = errors.New("empty server address")
	} else {
		opt = &redis.Options{Addr: address}
	}
	if err != nil {
		return nil, errors.New("crdt: invalid Redis address")
	}
	// Retrying an increment after losing its reply could count it twice.
	opt.MaxRetries = -1
	opt.ContextTimeoutEnabled = true
	if opt.Network == "" {
		opt.Network = "tcp"
	}
	identity := opt.Network + "\x00" + opt.Addr + "\x00" + strconv.Itoa(opt.DB)
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))
	return &RedisBackend{id: id, client: redis.NewClient(opt)}, nil
}

// New is the Redis convenience constructor. Use NewWithBackends for configurable
// storage. Servers may be host:port addresses or redis:// / rediss:// URLs.
func New(servers []string) (*CRDT, error) { return NewContext(context.Background(), servers) }

// NewContext constructs a client using Redis backends, pinging every server and
// closing all connections on failure. Each address/database pair identifies one
// persistent actor. Use the same address spelling on all clients, without aliases.
func NewContext(ctx context.Context, servers []string, options ...Option) (*CRDT, error) {
	if len(servers) == 0 {
		return nil, ErrNoServers
	}
	backends := make([]Backend, 0, len(servers))
	closeBackends := func() {
		for _, backend := range backends {
			_ = backend.Close()
		}
	}
	for i, server := range servers {
		backend, err := newRedisBackend(server)
		if err != nil {
			closeBackends()
			return nil, fmt.Errorf("crdt: server %d: %w", i, err)
		}
		backends = append(backends, backend)
	}
	c, err := NewWithBackends(backends, options...)
	if err != nil {
		closeBackends()
		return nil, err
	}
	errs := make([]error, len(backends))
	var wg sync.WaitGroup
	for i, backend := range backends {
		wg.Go(func() {
			if err := backend.(*RedisBackend).client.Ping(ctx).Err(); err != nil {
				errs[i] = fmt.Errorf("crdt: connect replica %d: %w", i, err)
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// ID returns the address/database-derived stable actor ID, including after Close.
func (b *RedisBackend) ID() string { return b.id }

// Increment atomically adds delta to this actor's component, with overflow checks.
func (b *RedisBackend) Increment(ctx context.Context, key string, delta int64) error {
	if delta < 0 {
		return ErrNegativeDelta
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.closed.Load() {
		return ErrClosed
	}
	if delta == 0 {
		return nil
	}
	limit := strconv.FormatInt(math.MaxInt64-delta, 10)
	return counterError(incrementScript.Run(ctx, b.client, []string{key}, b.id, delta, limit).Err())
}

// Snapshot reads and validates a detached counter vector. Missing keys are empty.
func (b *RedisBackend) Snapshot(ctx context.Context, key string) (CounterState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b.closed.Load() {
		return nil, ErrClosed
	}
	fields, err := b.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, counterError(err)
	}
	state := make(CounterState, len(fields))
	for actor, raw := range fields {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || actor == "" || value < 0 || strconv.FormatInt(value, 10) != raw {
			return nil, ErrInvalidState
		}
		state[actor] = value
	}
	return state, nil
}

// Merge atomically joins component maxima, preserving concurrent increments.
func (b *RedisBackend) Merge(ctx context.Context, key string, state CounterState) error {
	if err := state.validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.closed.Load() {
		return ErrClosed
	}
	if len(state) == 0 {
		return nil
	}
	args := make([]any, 0, len(state)*2)
	for actor, value := range state {
		args = append(args, actor, strconv.FormatInt(value, 10))
	}
	return counterError(mergeScript.Run(ctx, b.client, []string{key}, args...).Err())
}

// Close releases the connections exactly once; repeated calls return the same error.
func (b *RedisBackend) Close() error {
	b.closeOnce.Do(func() {
		b.closed.Store(true)
		b.closeErr = b.client.Close()
	})
	return b.closeErr
}

// incrementScript atomically validates an actor's stored component and increments
// it. HINCRBY already provides an atomic increment and int64 overflow checking;
// Lua bundles the additional nonnegative, canonical-integer validation and the
// overflow precheck with that update. A plain HINCRBY could replace this script
// if stored components were assumed to be valid.
//
// Stored counts and the overflow threshold stay as decimal strings. Converting
// them to Lua numbers would lose integer precision above 2^53; HINCRBY performs
// the actual arithmetic using Redis's signed 64-bit integer implementation.
//
// See https://redis.io/docs/latest/commands/hincrby/.
var incrementScript = redis.NewScript(`
local current = redis.call('HGET', KEYS[1], ARGV[1])
if current and not ((current == '0' or string.match(current, '^[1-9][0-9]*$')) and
 (#current < 19 or (#current == 19 and current <= '9223372036854775807'))) then
 return redis.error_reply('CRDT_INVALID_STATE')
end
if current and (#current > #ARGV[3] or (#current == #ARGV[3] and current > ARGV[3])) then
 return redis.error_reply('CRDT_OVERFLOW')
end
redis.call('HINCRBY', KEYS[1], ARGV[1], ARGV[2])
return 1
`)

// mergeScript atomically joins incoming and stored components by taking their
// maximum on one Redis server. This prevents a stale repair from overwriting a
// concurrent increment: a merge can read 5, another client increment it to 6,
// and an ordinary HSET of the merge's stale 5 would lose that increment. The
// script reads the current value and conditionally updates it without another
// client's commands interleaving, preserving 6 in that example.
//
// A Go mutex cannot coordinate writers in different client processes. Redis
// WATCH/MULTI/EXEC could instead protect a comparison performed in Go, at the
// cost of additional network exchanges and retries when concurrent writes cause
// transaction conflicts. Lua keeps this conditional update on the server.
// Other backends provide the same atomic merge contract with their own storage
// primitives; MemoryBackend uses a Go mutex. Synchronizing separate Redis servers
// is still handled by Go and is not a single cross-server transaction.
//
// Validate the entire destination before writing because Lua errors do not roll
// back earlier writes. Compare canonical decimal strings rather than Lua numbers
// to preserve exact int64 values, including integers above 2^53.
//
// See https://redis.io/docs/latest/develop/programmability/eval-intro/ and
// https://redis.io/docs/latest/develop/using-commands/transactions/.
var mergeScript = redis.NewScript(`
local function valid(s)
 return (s == '0' or string.match(s, '^[1-9][0-9]*$')) and
  (#s < 19 or (#s == 19 and s <= '9223372036854775807'))
end
local existing = redis.call('HGETALL', KEYS[1])
for i = 1, #existing, 2 do
 if existing[i] == '' or not valid(existing[i + 1]) then
  return redis.error_reply('CRDT_INVALID_STATE')
 end
end
for i = 1, #ARGV, 2 do
 local current = redis.call('HGET', KEYS[1], ARGV[i])
 local incoming = ARGV[i + 1]
 if not current or #current < #incoming or (#current == #incoming and current < incoming) then
  redis.call('HSET', KEYS[1], ARGV[i], incoming)
 end
end
return 1
`)

func counterError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, redis.ErrClosed) {
		return fmt.Errorf("%w: %w", ErrClosed, err)
	}
	var reply redis.Error
	if !errors.As(err, &reply) {
		return err
	}
	switch strings.TrimPrefix(reply.Error(), "ERR ") {
	case "CRDT_INVALID_STATE":
		return fmt.Errorf("%w: %w", ErrInvalidState, err)
	case "CRDT_OVERFLOW":
		return fmt.Errorf("%w: %w", ErrOverflow, err)
	default:
		return err
	}
}
