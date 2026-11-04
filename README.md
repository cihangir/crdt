# CRDT

[![CI](https://github.com/cihangir/crdt/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/cihangir/crdt/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/cihangir/crdt.svg)](https://pkg.go.dev/github.com/cihangir/crdt)

Conflict-free replicated data types for Go 1.27+. Replicas merge independent
updates and converge when they have received the same state, regardless of the
order or number of deliveries.

| Type | Storage | Conflict rule |
| --- | --- | --- |
| `GCounter` | Configurable backend | Sum the maximum count for each actor; increments only |
| `PNCounter` | Configurable backend | Two grow-only vectors: positive minus negative |
| `GSet[T]` | In memory | Union of all added elements |
| `TwoPhaseSet[T]` | In memory | Union of adds and permanent removals; removed elements cannot return |
| `ORSet[T]` | In memory | Remove observed adds; a concurrent add survives and re-adds are allowed |
| `LWWRegister[T]` | In memory | Largest logical clock wins; actor ID breaks ties |

All handles are safe for concurrent use. Do not copy a handle after first use.
The sets and register have usable zero values. Transport, periodic synchronization,
and persistence of their snapshots are owned by the application.

```sh
go get github.com/cihangir/crdt@main
```

## Counter backends

G/PN counters use the `Backend` interface. Redis is one storage option; the
included `MemoryBackend` runs entirely in process. Custom implementations and
mixed backend types use the same counter APIs.

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/cihangir/crdt"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	east, err := crdt.NewMemoryBackend("east")
	if err != nil {
		return err
	}
	west, err := crdt.NewMemoryBackend("west")
	if err != nil {
		east.Close()
		return err
	}

	client, err := crdt.NewWithBackends(
		[]crdt.Backend{east, west},
		crdt.WithPrefix("example"),
	)
	if err != nil {
		east.Close()
		west.Close()
		return err
	}
	defer client.Close()

	ctx := context.Background()
	visits := client.NewGCounter("visits")
	if err := visits.AddContext(ctx, 3); err != nil {
		return err
	}
	value, err := visits.MergeContext(ctx)
	if err != nil {
		return err
	}
	fmt.Println(value) // 3

	balance := client.NewPNCounter("balance")
	if err := balance.AddContext(ctx, -2); err != nil {
		return err
	}
	value, err = balance.MergeContext(ctx)
	if err != nil {
		return err
	}
	fmt.Println(value) // -2
	return nil
}
```

`NewWithBackends` accepts initialized backends and performs no I/O. It validates
nonempty, distinct actor IDs and copies the input slice. On success the client
owns the supplied backends and closes each exactly once; on failure they remain
caller-owned. Do not share a backend with another client that may close it early.
`Close` is safe to repeat and to call concurrently.

Memory storage is volatile: closing a backend or exiting the process loses its
state. Choose a unique ID for every independent actor and keep that ID stable
while its state is live. Recover the latest state before reusing an actor ID;
starting an empty actor under a previously used ID can lose updates.

Every counter handle selects one actor for writes. `Add` atomically increments
only that actor's component. `Merge` reads all actors, takes the maximum **per
component**, sums the components, and joins the result into each backend.
Independent increments are preserved, and atomic merge cannot lower a concurrent
increment. Counter keys are namespaced consistently across backend types.

- `Value` / `ValueContext` / `Query` read without repair.
- `Merge` / `MergeContext` read and repair, returning the value.
- `Snapshot(ctx)` exports the component vectors; `MergeState(ctx, snapshot)`
  joins them into every configured backend.
- `CounterState` and `PNCounterState` support pure `Merge`, `Compare`, and `Value`.
  Comparison reports `Equal`, `Before`, `After`, or `Concurrent` based on the
  vectors, not just their totals.
- G-counter increments must be nonnegative. PN-counter `Add` accepts signed
  increments; `Increment` and `Decrement` accept nonnegative magnitudes, with
  context variants available. Every component is limited to `math.MaxInt64`;
  `Add(math.MinInt64)` is rejected because its magnitude cannot fit one component.
  Arithmetic overflow is reported with `ErrOverflow`.

Check every returned error. An unavailable backend produces an error with the
partial result; a G-counter's partial result is a lower bound, while a PN-counter's
partial result has no such bound. Neither query nor repair is a global transaction:
concurrent writes can require another merge to propagate everywhere. State merges
are idempotent and safe to retry; increments are not generally safe to retry after
an I/O failure because the backend may have applied the write before losing its
reply.

### Redis backend

```go
store, err := crdt.NewRedisBackend(ctx, "redis://localhost:6379/0")
if err != nil {
	return err
}
client, err := crdt.NewWithBackends([]crdt.Backend{store}, crdt.WithPrefix("example"))
if err != nil {
	store.Close()
	return err
}
defer client.Close()
```

Addresses may be `host:port`, `redis://user:password@host:6379/0`, or `rediss://`
URLs. `NewRedisBackend` connects and pings the server, cleaning up on failure.
This snippet assumes a caller-provided `ctx` and a function that returns an error.

The existing `New(servers)` and `NewContext(ctx, servers, options...)` functions
remain convenience constructors for a client made entirely of Redis backends.
They ping every configured server and clean up if any connection fails.
The following snippets are function-body fragments whose enclosing function returns
an error; `ctx` is a caller-provided `context.Context`.

```go
client, err := crdt.NewContext(ctx, []string{
	"127.0.0.1:6379",
	"127.0.0.1:6380",
}, crdt.WithPrefix("example"))
if err != nil {
	return err
}
defer client.Close()
```

These must be independent Redis actors, rather than a primary and its read-only
replica. Each address/database pair owns a component in a Redis hash. Use the
same address spelling and DB on all clients, without aliases for one server.
Decimal string comparisons preserve exact integers beyond Lua's 2^53 precision
limit. Increments disable automatic retries and failover to avoid double-counting
writes after an ambiguous network error.

Configure Redis persistence and disable eviction/expiration for counter keys.
Never reset, delete, restore an older version of, or reuse a live actor's counter
hash. Recovery must restore the latest state before writes resume. Use new names
for new counters.

### Custom backends

Implement the exported interface and pass instances to `NewWithBackends`:

```go
type Backend interface {
	ID() string
	Increment(ctx context.Context, key string, delta int64) error
	Snapshot(ctx context.Context, key string) (crdt.CounterState, error)
	Merge(ctx context.Context, key string, state crdt.CounterState) error
	Close() error
}
```

`Increment` must atomically increase the backend's own actor component, checking
for overflow. `Merge` must atomically take the component-wise maximum against
current storage; reading and then overwriting without concurrency control loses
updates. An absent key is an empty vector. `Snapshot` returns detached state, and
`Merge` must neither retain nor mutate its input map. Honor cancellation and
report errors rather than silently resetting state. IDs must be unique across
all actors, even when they use different storage implementations. The built-in
backends share a contract test suite in `backend_test.go`.

## Generic sets

A grow-only set exchanges a snapshot of its elements:

```go
var east, west crdt.GSet[string]
if err := east.Add("apple"); err != nil {
	return err
}
if err := west.Add("pear"); err != nil {
	return err
}
if err := east.Merge(west.Snapshot()); err != nil {
	return err
}
for value := range east.Values() {
	fmt.Println(value) // apple and pear, in unspecified order
}
```

`TwoPhaseSet[T]` adds permanent removal. `Remove(value)` affects an observed
element; a later `Add(value)` returns `ErrRemoved`. A concurrent add cannot undo
a removal. Use this for identifiers that must never be reused.

`ORSet[T]` tags each add with a unique UUID. A removal only deletes the tags it has
seen, so a concurrent add survives, and an element can be added again later:

```go
var phone, laptop crdt.ORSet[string]
if err := phone.Add("milk"); err != nil {
	return err
}
if err := laptop.Merge(phone.Snapshot()); err != nil {
	return err
}
phone.Remove("milk")
if err := laptop.Add("milk"); err != nil {
	return err
} // concurrent fresh add
if err := phone.Merge(laptop.Snapshot()); err != nil {
	return err
}
fmt.Println(phone.Contains("milk")) // true
```

All sets expose `Add`, `Contains`, `Len`, `Values`, `Snapshot`, and `Merge`.
The removable sets also expose `Remove`, which returns whether it removed a
visible value. `Values` iterates over a detached snapshot; caller code runs
without holding a set lock. Iteration order is unspecified.

Elements must have stable, reflexive equality. NaN values and non-comparable
values hidden in interfaces are rejected with `ErrInvalidState`. Use value-based
identifiers that preserve their equality across your chosen transport. Snapshots
copy their containers; referenced data inside elements is not recursively copied.

Removal history is part of the state. **Keep two-phase removals and OR-set
tombstones** when persisting or transmitting snapshots; dropping them can resurrect
removed elements. No automatic history compaction is performed, so removal history
grows over the lifetime of these sets.

## Last-writer-wins register

```go
var a, b crdt.LWWRegister[string]
if err := a.Assign("draft"); err != nil {
	return err
}
if err := b.Merge(a.Snapshot()); err != nil {
	return err
}
if err := b.Assign("published"); err != nil {
	return err
}
if err := a.Merge(b.Snapshot()); err != nil {
	return err
}
value, assigned := a.Value() // "published", true
fmt.Println(value, assigned)
```

Each assignment advances a Lamport logical clock beyond the observed state.
Concurrent assignments with equal clocks resolve by actor ID, giving every replica
the same winner. This orders logical writes, not physical wall-clock time, and
requires no synchronized clocks. Each writing handle generates a fresh actor UUID.
An unassigned register returns `false` from `Value`.

`T` may be any type, but values must be treated as immutable. Snapshots copy `T`
by assignment; maps, slices, and pointers are not deeply cloned. Restore state into
a fresh register with `Merge`. Repeated delivery of the same timestamp is ignored;
a timestamp must always refer to the same immutable write.

## Snapshots and transport

Send snapshots, not structs containing mutexes. Snapshot types have exported
fields and JSON tags. For example, using Go 1.27's `encoding/json/v2`:

```go
wire, err := json.Marshal(phone.Snapshot())
if err != nil {
	return err
}
var state crdt.ORSetState[string]
if err := json.Unmarshal(wire, &state); err != nil {
	return err
}
if err := laptop.Merge(state); err != nil {
	return err
}
```

Pick element/value types your transport can encode faithfully. Do not mutate a
snapshot while another goroutine is reading or merging it. `Merge` validates the
snapshot's structure before applying it; snapshots are expected to originate from
trusted replicas of the same logical object. Object identity and authentication
belong to the application's synchronization protocol.

## Upgrading the original prototype

The existing `New([]string)`, `NewGCounter(key)`, `Add(delta)`, and `Merge()` entry
points remain. Use `NewWithBackends` to choose storage, or `NewContext` for Redis.
Prefer `WithPrefix` over the deprecated global `CRDTPrefix`. The abandoned `github.com/koding/redis` dependency has been replaced
with `github.com/redis/go-redis/v9`.

The Redis wire format changes deliberately. New counters live under
`<prefix>:v2:<kind>:<base64-key>` and leave legacy scalar keys untouched. Old
mirrored scalar counters cannot be converted into independent actor histories
automatically. To preserve their known baseline, stop the old writers, determine
the maximum legacy scalar, and import it once using one stable synthetic actor:

```go
if err := visits.MergeState(ctx, crdt.CounterState{"legacy-import": legacyMaximum}); err != nil {
	return err
}
```

Use that same synthetic actor if repeating the import. This preserves the known
baseline, but cannot recover increments already lost by the old max-of-scalars
implementation. Do not run old and new writers against the same logical counter
during migration. Negative G-counter deltas now return an error, and read/repair
failures are reported instead of silently ignored.

## Development

```sh
make check
```

That command verifies Go source and README snippet formatting, module checksums,
`go vet`, race detection, and
tests. The normal suite checks memory and Redis backends, including mixed
replicas, using isolated in-process Redis servers; no installed Redis or Docker
is needed. Examples run as tests. Additional commands:

```sh
make fmt
make fuzz
make bench
```

To test the Lua scripts against independent real Redis servers, set their addresses:

```sh
CRDT_REDIS_SERVERS=127.0.0.1:6379,127.0.0.1:6380 go test -race -run TestRedisIntegration -v
```

The integration test uses a unique namespace and deletes only its own keys.
GitHub Actions runs the race suite on Linux, macOS, and Windows, plus real Redis,
fuzzing, and `govulncheck`. This module uses generics, range-over-function iterators,
`maps` / `slices`, `math/rand/v2`, `sync.WaitGroup.Go`, and the standard-library
`uuid` package. The preferred project toolchain is Go 1.27.3; see the
[Go 1.27 release notes](https://go.dev/doc/go1.27).

The CRDT definitions follow the state-based models from Shapiro et al.'s
[A comprehensive study of Convergent and Commutative Replicated Data Types](https://inria.hal.science/inria-00555588).
