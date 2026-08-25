# Testing strategy

## Layers

| Layer | What it covers | When it runs |
|---|---|---|
| Unit | Domain logic, parsers, validation, pure functions | Every PR |
| Golden frame | Real captured packets, byte for byte | Every PR (once fixtures exist) |
| Fuzz | Every parser that touches network input | Every PR, plus extended nightly |
| Integration | Subsystems together over loopback | Every PR |
| Failure injection | The Constitution §9 list | Every PR |
| Race | `go test -race ./...` | Every PR, **blocking** |
| Static | `go vet`, `staticcheck` | Every PR, **blocking** |
| Soak | 72 hours with induced failures | Pre-release |
| Hardware | Real radios and repeaters | Phase gates only |

## Principles

### Name the failure mode, don't count the lines

`TestReplayReportsIncompleteWhenHistoryEvicted` states a property and proves it.
"84% coverage" states nothing. Coverage is a diagnostic for finding untested
paths, never a target.

### Verify against an external reference where one exists

The PBKDF2 implementation is tested against six known-answer vectors generated
by CPython's OpenSSL-backed `hashlib.pbkdf2_hmac` — including multi-block output
and embedded NUL bytes. Testing a cryptographic primitive against itself proves
only that it is self-consistent.

The same reasoning is why protocol parsers are blocked on real captures.

### Inject time

Nothing calls `time.Now()` where behaviour depends on it. Clocks are injected
via `Options.Clock`, without which DST transitions, clock jumps and timeout
boundaries cannot be tested at all.

**An injected clock is called concurrently** — `health.Registry.Run` calls it
from every check goroutine. Test clocks must be concurrency-safe. This was
caught by the race detector during development, in a test helper.

### Test the failure paths

The Constitution §9 list, each with its own test:

- Database unavailable at startup, and mid-operation
- Disk full during a write
- A peer that connects and never speaks
- A peer that floods
- Malformed packets of every shape
- Clock jumps, forward and backward
- Configuration corrupted on disk
- Network partition and recovery
- Resource exhaustion

### The race detector is a gate, not a suggestion

It has already caught a real defect here: the event bus checked a `closed` flag
before sending on a subscriber channel, leaving a window in which the channel
could be closed between the check and the send. Check-then-act across goroutines
is a race; the fix was a per-subscription mutex making the two atomic.

## Documentation is checked against the code

Prose describing a build that no longer exists has been this project's most
persistent defect: the console claimed no protocol, routing or scheduling code
was present long after all three were written, and fixing the console left the
same claim in two package comments, `ARCHITECTURE.md` and this file.

`cmd/qsp/docaccuracy_test.go` is a gate for the tractable part of that problem.
A test cannot know whether a sentence is true, but it can know whether a
sentence contradicts something the program enumerates:

| Check | Source of truth |
|---|---|
| Documented endpoints match registered routes | `server.APIPaths()` |
| Paths named in documentation exist | the filesystem |
| Directories called empty are empty | the filesystem |
| Nothing calls a built subsystem absent | the health registry, minus `unbuiltSubsystems` |
| Unbuilt subsystems name the phase that brings them | `unbuiltSubsystems` |

Two escape hatches exist, both requiring a written reason so that silencing a
check costs a sentence a reviewer can argue with. `<!-- doc-accuracy: frozen —
… -->` in a document's first twenty lines exempts it entirely, which is how
`BLUEPRINT.md` stays a dated artefact rather than a lie. `<!-- doc-accuracy:
allow-path … — … -->` exempts one path a decision record plans but has not yet
built.

What this does **not** catch is the opposite error — documentation promising
something that does not exist. That remains a matter of review.

## What the protocol fixtures cover, and what they do not

`testdata/hbp/` and `testdata/p25/` hold captured traffic only. Nothing in them
was fabricated.

Fabricating a packet to test a parser proves only that the parser agrees with
the author's idea of the protocol. It cannot reveal that the idea is wrong,
which is precisely the failure that matters — and the one that takes a club's
repeater off the air.

**Covered.** The complete HBP login handshake, steady-state keepalives, and a
voice session. Every message parses and round-trips byte-for-byte.

**Not covered, and therefore unverified in code:**

| Gap | What closes it |
|---|---|
| No live voice frame decoded end to end | A hotspot transmission reaching QSP |
| `RPTCL` and `MSTNAK` never seen on a wire | A capture of a disconnect and a bad login |
| Repeater-ID rewrite on relay | A TG 9990 parrot capture |
| The `description`/`slots` field split | A capture from a single-timeslot hotspot |
| Any P25 transmission | `testdata/p25/` holds polling traffic only |

P25 implementation remains blocked on that last row **and** on the licensing
question in [ADR-0008](../adr/ADR-0008-protocol-licensing.md).

See [`testdata/README.md`](../../testdata/README.md) for what a contributed
capture must include.
