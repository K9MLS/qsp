# ADR-0038: The routing core is reached by more than one listener, so it locks

**Status:** Accepted
**Relates to:** [ADR-0002](ADR-0002-single-writer-routing-core.md),
[ADR-0018](ADR-0018-openbridge.md),
[ADR-0036](ADR-0036-ipsc-voice-is-not-a-dmr-burst.md)

## Context

`routing.Core` documented itself as owned by the goroutine that reads the DMR
socket, consistent with ADR-0002, and the codebase took that seriously. Applying
a configuration change from an HTTP handler was deliberately not done directly;
`internal/peers/reload.go` exists solely to carry a new table onto the serve
goroutine, and says in its own comment that a handler calling `SetTable` would be
a data race.

**That invariant was already false, and had been since upstream links were
built.** `internal/upstream/peer.go` calls its `Receive` callback from the link's
own read goroutine. In `cmd/qsp/app.go` that callback is
`peers.Listener.DeliverFromUpstream`, which calls `Core.Route`, which writes the
reservation map. A frame arriving from another network and a frame arriving from
a hotspot could therefore enter `Route` at the same moment on two goroutines.

Nothing caught it. The routing tests are single-goroutine. No test ran a link and
a peer together under the race detector. And no upstream link has ever met a real
far end, so the code that would have demonstrated it has never run in anger.

Wiring the IPSC listener to routing made it reproducible immediately, because an
IPSC repeater arrives on a second socket with a serve goroutine of its own. The
detector reported it on the first run of the first test.

This is the pattern §8a of `PROJECT_MEMORY.md` names: two things each correct on
their own. `Core` was right to keep its state unlocked given a single owner. The
listener was right to route a frame the moment it arrived. The pair was wrong,
and the prose asserting the invariant is what made it invisible — a comment
saying "owned by one goroutine" reads like a fact and is in truth a request.

## Decision

**`routing.Core` guards its mutable state with a mutex, and the ownership rule is
enforced by the type rather than asserted about it.**

The lock covers what changes under traffic: the reservation map, the routing
table and the access lists. The collaborators fixed at construction — the peer
lookup, the subscriber lookup, the attachment source — are set once in `NewCore`
and only read, so they are not covered.

`Table()` becomes safe to call from any goroutine. It remains the wrong thing for
a console to call, because the value it returns may be replaced a moment later;
that is a staleness argument rather than a safety one, and the comment now says
so.

ADR-0002 is not withdrawn. Goroutine ownership remains the model for sockets,
peer tables and everything else that belongs to one listener. `Core` is the
exception because it is the one piece of state that more than one listener must
reach: it is where the network's shape lives, and a second protocol on a second
socket is precisely the thing QSP exists to do.

## Consequences

The upstream race is fixed as a side effect, which matters more than the IPSC
path that exposed it — it was a defect on a path an operator could already
configure.

`internal/peers/reload.go` is now belt and braces. It is kept: applying a table
at a transmission boundary rather than mid-frame is a behavioural choice about
not cutting somebody off mid-sentence, and that reason survives the lock.

A lock in the routing path is a contention risk in principle. In practice a
routing decision is a map lookup and a short loop over a bridge's endpoints, at
a rate bounded by voice frames — one per peer per sixty milliseconds — so the
critical section is orders of magnitude shorter than the interval between
entries.

**The general lesson is recorded because it will recur:** a comment claiming a
concurrency invariant is not a mechanism, and this project now has two listeners
and will later have a third for P25. Where an invariant matters, enforce it in
the type. Where it cannot be enforced, a test that runs both producers under the
detector is the next best thing, and its absence is why this survived.
