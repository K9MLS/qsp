# ADR-0003: Sequenced event bus with bounded replay

**Status:** Accepted

## Context

The console needs live updates. The obvious approach — publish to subscriber
channels — has two failure modes that matter operationally.

A slow browser tab can block a publisher. If that publisher is handling network
traffic, a background tab degrades radio service.

A reconnecting client cannot tell whether it missed anything, so it renders a
confidently wrong view. That is worse than showing nothing.

## Decision

- Every event carries a monotonically increasing sequence number.
- Publish is non-blocking. A full subscriber queue drops the event and
  increments a counter.
- A bounded ring buffer retains recent events. `Replay(since)` returns the
  events **and a boolean stating whether the history covered the gap**.
- `Subscribe` returns the sequence current at subscription, giving a race-free
  snapshot-then-stream ordering.

## Consequences

- Traffic handling is never affected by console clients.
- A client always knows whether its view is complete. When it is not, it is told
  to discard local state and re-snapshot.
- Retained history is finite, so a long disconnection means a resync rather than
  a replay. Acceptable: a snapshot is cheap.
- **Implementation note.** Delivery and closure must be atomic with respect to
  each other. Checking a `closed` flag before sending is a check-then-act race;
  the race detector caught a send-on-closed-channel window during development.
  A per-subscription mutex is the fix.
