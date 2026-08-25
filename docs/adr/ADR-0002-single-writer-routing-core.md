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
