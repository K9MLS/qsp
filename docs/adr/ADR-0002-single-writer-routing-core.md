# ADR-0002: Single-writer routing core

**Status:** Accepted

## Context

The routing core owns the peer registry, active streams, the route table and
hang timers. Every one is touched concurrently by UDP readers, the scheduler,
configuration changes and the console.

Three shapes were considered:

1. **Mutex-guarded shared state.** Familiar, and every future contributor gets a
   fresh chance to forget a lock.
2. **Sharded by talkgroup.** Good throughput; cross-talkgroup operations such as
   bridging need locks across shards, which reintroduces the problem where it is
   hardest to reason about.
3. **Single-writer actor.** One goroutine owns all state and consumes one
   inbound channel.

## Decision

Single-writer actor.

I/O goroutines parse and validate, then hand structured events to the core. The
core is the only mutator.

## Consequences

- Data races on routing state become structurally impossible rather than merely
  tested against.
- **Nothing blocking may ever run inside the core.** No database call, no network
  call, no file I/O, no lock another goroutine might hold. This is the price and
  it is not negotiable — one slow operation stalls all routing.
- Testing gets easier: feed the core events, assert its outputs, no sockets.
- Reversing this later means rewriting the centre of the system, which is why it
  is recorded before any of it is written.

## Open

Measured throughput on a Raspberry Pi under realistic peer counts. If a single
writer proves insufficient, sharding is the fallback and this ADR is superseded.

## Amendment, 2026-09-02 — see [ADR-0038](ADR-0038-routing-core-is-shared.md)

**The consequence claimed above — that data races on routing state become
structurally impossible rather than merely tested against — was not true.** It
described an intention. Nothing enforced it, and the first component to reach
the core from a second goroutine did so without anything objecting: an upstream
link calls `DeliverFromUpstream` from its own read goroutine, and has since links
were built.

`routing.Core` now takes a mutex over its mutable state, so the claim above is
true by mechanism instead of by intent.

**On "no lock another goroutine might hold":** that rule stands and is not
violated. It forbids the core from blocking on somebody else's lock — a database,
a socket, a file. The core's own mutex is held for a map lookup and a short loop
over one bridge's endpoints, at a rate bounded by voice frames, and is released
before anything is written anywhere. The rule exists so that one slow operation
cannot stall all routing, and nothing here is slow.

The single-writer model remains correct for sockets, peer tables and everything
else owned by one listener. It stopped being achievable for the core itself the
moment QSP served two protocols on two sockets, which is what it was built to do.
