# ADR-0013: The routing decision is a pure function

**Status:** Accepted

## Context

Routing is the centre of QSP and the part where a mistake is least visible and
most damaging. A wrong decision does not crash. It quietly sends a net onto the
wrong talkgroup, or loops audio back to the repeater that produced it, and the
first sign is somebody on the air asking why.

The obvious shape is a routing engine that reads a frame, decides, and writes it
onward. That shape makes the decision only testable through I/O.

## Decision

`routing.Table.Route` takes a call's arrival point and returns the endpoints it
should be delivered to. **It performs no I/O and forwards nothing.**

Three properties hold for every result, enforced by unit tests and by a fuzz
target that explores arbitrary configurations:

1. **The source is never a target.** On a repeater, delivering a call back where
   it came from is feedback. No configuration an operator can write should be
   able to produce it.
2. **Targets are unique.** Two bridges joining the same pair deliver one copy.
   A second copy is audible.
3. **The result is deterministic.** Same inputs, same output, same order — so a
   log line and a test can be compared.

A fourth property covers observability: **an empty result always carries a
reason.** "Why didn't this call route?" is the question this package exists to
answer, and an empty list with no explanation is the worst possible answer.

## Consequences

- The entire routing decision is a table of inputs and expected outputs. No
  sockets, no peers, no timing.
- **Fuzzing is possible**, and worthwhile: it explores configurations an
  operator might write that no test author would think of. 728,000 executions
  found no configuration producing an echo or a duplicate.
- `Table` is immutable after construction, including the slices inside it. A
  configuration change builds a new table which the core swaps in atomically, so
  a call is never routed against a half-applied configuration (invariant I3).
- **Deliberately not decided here:** contention. `Route` does not know whether a
  target is already carrying a call, because it does not know what is in flight.
  That belongs to the routing core, which does. Putting it here would require
  passing live state into a pure function and would make it untestable in the
  way that motivated this decision.

## A subtlety worth recording

`Decision.Bridges` reports **every** enabled bridge that would carry the call,
not only the one that first contributed a target.

The distinction matters operationally. With two overlapping bridges, naming only
the first would mislead an operator into disabling it and finding the call still
routes, because the second still carries it. This was caught by a test that
initially looked like it was asserting a cosmetic detail.
