// Package crdt implements state-based conflict-free replicated data types.
// Counters store monotonic components in configurable backends and merge by
// component-wise maximum. Built-in backends support Redis and volatile memory;
// applications can implement Backend for other storage. Generic sets and
// registers exchange detached snapshots. Replicas converge after receiving the
// same updates, in any order, including duplicate deliveries. Transport and
// synchronization are caller-owned.
package crdt
