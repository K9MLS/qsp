# ADR-0039: The peer table is shared too, and the fix belonged one layer down

**Status:** Accepted
**Relates to:** [ADR-0038](ADR-0038-routing-core-is-shared.md),
[ADR-0002](ADR-0002-single-writer-routing-core.md)

## Context

[ADR-0038](ADR-0038-routing-core-is-shared.md) found that `routing.Core` was
reached from two goroutines and locked it. That was correct and it was not
enough.

`peers.Listener.deliver` resolves each destination through `Master.Lookup`, and
`peers.Master` had no synchronisation at all — `m.peers` was a plain map owned by
the goroutine reading the DMR socket. So every delivery from an upstream link
already raced with peer registration, on the same seam and for the same reason,
and locking the routing state above it did nothing about the table underneath.

The detector reported it the first time an IPSC transmission was routed while a
hotspot was registering.

**The lesson is about the shape of the fix, not the bug.** ADR-0038 identified
the right seam — one listener handing frames to another — and then guarded only
the first thing that seam touched. Everything else it reaches has the same
exposure, and asking "what else does this path touch?" would have found the peer
table in the same hour. A concurrency fix scoped to the symptom is a concurrency
fix that will be needed again.

## Decision

**`peers.Master` guards all of its mutable state with one `sync.RWMutex`:** the
peer table, the attachments, the subscriber locations and the login throttle.

One lock rather than four, because these are not independent — `Handle` mutates
peers, attachments, locations and the throttle in a single message, and separate
locks would mean an ordering rule that nothing enforces. That is the mistake
this pair of ADRs exists to stop making twice.

Readers take the read lock, and everything reached from `Handle` keeps the write
lock for the whole message. `LocateFor` called `Locate`, which would have
deadlocked on entry; the shared body is now an unexported `locate` and both
exported forms take the lock once. **No other exported method on `Master` calls
another**, which is what makes a single lock at the exported boundary safe, and
it is worth re-checking before adding one that does.

## Consequences

The upstream delivery race is fixed, which is the part that was already
reachable on a configured path rather than a new one.

`Handle` holds a write lock for the duration of one datagram: a parse, a map
write and some bookkeeping. Readers are console handlers and the routing
resolution, at voice-frame rates. Contention is not a concern at these
magnitudes, and if it ever becomes one the answer is a measurement rather than
a second lock.

ADR-0002's single-writer model is now confined to what it can actually hold: the
sockets, and the goroutines that read them. **Any state two listeners reach is
shared state, and QSP is a program whose purpose is to serve more than one
protocol at once.** P25 will be the third. Before adding a listener, the
question to ask is what it touches that another listener already touches, and
the answer is a lock rather than a comment.
