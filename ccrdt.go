package crdt

import (
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

// CRDTPrefix is the default counter namespace. Set it before constructing clients.
//
// Deprecated: use WithPrefix instead. Changing a global while other goroutines
// construct clients is not safe.
var CRDTPrefix = "crdt"

// Option configures a CRDT client independently of its storage backend.
type Option func(*CRDT)

// WithPrefix sets the counter namespace for a client.
func WithPrefix(prefix string) Option { return func(c *CRDT) { c.prefix = prefix } }

type replica struct {
	id      string
	backend Backend
}

// CRDT owns the storage backends for independent counter actors. It is safe for
// concurrent use and must not be copied. Call Close when finished.
type CRDT struct {
	replicas  []replica
	prefix    string
	closeOnce sync.Once
	closeErr  error
}

// NewWithBackends constructs a client from initialized storage backends without
// performing I/O. Backends may use different storage implementations. Their IDs
// must be distinct and stable. On success the client owns and closes them; on
// failure the caller retains ownership. The input slice is not retained.
func NewWithBackends(backends []Backend, options ...Option) (*CRDT, error) {
	if len(backends) == 0 {
		return nil, ErrNoBackends
	}
	c := &CRDT{prefix: CRDTPrefix}
	for _, option := range options {
		option(c)
	}
	seen := make(map[string]bool, len(backends))
	for i, backend := range backends {
		if nilBackend(backend) {
			return nil, fmt.Errorf("%w: backend %d is nil", ErrInvalidBackend, i)
		}
		id := backend.ID()
		if id == "" || seen[id] {
			return nil, fmt.Errorf("%w: backend %d has an empty or duplicate actor ID", ErrInvalidBackend, i)
		}
		seen[id] = true
		c.replicas = append(c.replicas, replica{id: id, backend: backend})
	}
	return c, nil
}

func nilBackend(backend Backend) bool {
	if backend == nil {
		return true
	}
	value := reflect.ValueOf(backend)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// Close closes each owned backend exactly once, returning any close errors.
// Subsequent calls return the same result. It is safe to call concurrently.
func (c *CRDT) Close() error {
	c.closeOnce.Do(func() {
		errs := make([]error, len(c.replicas))
		for i, replica := range c.replicas {
			if err := replica.backend.Close(); err != nil {
				errs[i] = fmt.Errorf("crdt: close backend %d: %w", i, err)
			}
		}
		c.closeErr = errors.Join(errs...)
	})
	return c.closeErr
}

func (c *CRDT) counterKey(kind, key string) string {
	return c.prefix + ":v2:" + kind + ":" + base64.RawURLEncoding.EncodeToString([]byte(key))
}
