# QSP Architecture

This document describes how QSP is put together and, more importantly, which
properties must not be broken. It is the reference for reviewing changes.

For *what* QSP does, see [`BLUEPRINT.md`](BLUEPRINT.md). Individual decisions and
their reasoning live in [`docs/adr/`](docs/adr/).

---

## 1. Shape

One statically-linked pure-Go binary. A serialised routing core, protocol
adapters feeding it, and a web console observing it.

```
UDP sockets ──► per-peer readers ──► parse ──► validate
                                                  │
                                                  ▼
                                    ┌─────────────────────────┐
                                    │   ROUTING CORE          │
                                    │   single-writer actor   │
                                    │   owns all live state   │
                                    └──┬──────────┬───────────┘
                                       │          │
                    ┌──────────────────┘          └────────────┐
                    ▼                                          ▼
         per-peer writers                                 event bus
                    │                                          │
                    ▼                                    ┌─────┴─────┐
           UDP sockets out                            SSE        SQLite
                                                   (console)  (history/audit)
```

Implemented today: config, storage, events, health, logging, HTTP, console, the
HBP codec (`parse` above), the peer lifecycle (`validate` above), the routing
core, call observation, the scheduler and PTT triggers.

P25, the vocoder pool and the analog connectors (AllStar, Zello, EchoLink) are
later phases and report `unavailable` in the health endpoint.

---

## 2. Invariants

These four claims are what the design rests on. A change that violates one is
not a refactor; it is a redesign and needs an ADR.

### I1 — The routing core is a single-writer actor

All live routing state — peer registry, active streams, route table, hang timers
— is owned by one goroutine consuming one inbound channel. I/O goroutines parse
and validate, then hand structured events in. Nothing else mutates it.

This makes data races structurally impossible rather than merely tested against.
The cost: **nothing blocking may ever run inside the core.** No database call, no
network call, no file I/O, no lock that another goroutine might hold.

### I2 — The hot path never touches the database

Routing decisions are made against in-memory state. SQLite serves configuration
history, audit and reporting, all off the critical path.

This invariant is what makes a pure-Go SQLite driver an acceptable trade
(ADR-0005). Wanting a database read inside a routing decision is the signal to
stop and revisit, not to work around it.

### I3 — Configuration changes are transactional

A change is validated, persisted, then handed to the routing core as a single
atomic swap. A partially-applied configuration never exists, and an invalid one
never becomes active.

### I3a — Protocol code is derived from captured traffic only

No HBP or P25 implementation source has been read, ported or transliterated.
Every claim `internal/protocol/hbp` makes about the wire format is demonstrated
by a fixture in `testdata/hbp/`. Message kinds known to exist but never captured
are rejected with `ErrNotCaptured` rather than guessed at.

This is a licensing constraint (ADR-0008) that turned out to be an engineering
benefit: it forced three behaviours into the open that no specification would
have revealed — the `SHA-256(salt ‖ password)` construction, DMRGateway's
abbreviated `DMRC`/`DMRP` dialect, and the fact that `RPTACK` is ambiguous
without connection state.

### I3b — A peer's address is part of its identity

After authentication, every datagram is checked against the address the peer
registered from. Repeater IDs are public, so the address is the only thing
separating the station that authenticated from anyone who knows its number. A
rebound peer must re-run the handshake; see
[ADR-0011](docs/adr/ADR-0011-nat-rebind.md).

### I3c — Observation is not routing

`internal/calls` reassembles frames into transmissions so an operator can see
who is talking. It makes no routing decision and forwards nothing. An accepted
frame is observed and discarded until the routing engine exists.

A stream that stops without a terminator is closed after a timeout and recorded
as such rather than cleaned up silently: in a bridging context an unclosed
stream is how a talkgroup gets welded open, and an operator seeing many of them
has a real problem worth surfacing.

**The listener's sweep interval must stay no coarser than the call timeout.**
Otherwise a lost transmission displays as live until the next sweep, which an
operator cannot distinguish from somebody actually keyed up.
`TestSweepIntervalIsFineEnoughForCallTimeout` pins the relationship.

### I3d — The routing decision is pure and cannot echo

`routing.Table.Route` maps a call's arrival point to its destinations. It does
no I/O and forwards nothing, which is what makes the whole decision testable as
a table of inputs and outputs — and fuzzable.

Three invariants hold for any configuration: **the source is never a target**
(on a repeater that is feedback), targets are unique, and the order is
deterministic. A fourth covers the operator: an empty result always says why.

See [ADR-0013](docs/adr/ADR-0013-routing-decision-is-pure.md).

`routing.Core` adds the one thing a pure decision cannot have: knowledge of what
is in flight. A transmission reserves its destinations **and its origin**, so a
collision is refused whole rather than fragmented across the network
([ADR-0014](docs/adr/ADR-0014-contention.md)).

**Forwarding is off by default.** `dmr.forwarding` is the switch, separate from
`dmr.enabled`, so an operator can run QSP as a master and watch peers connect
before it puts audio on anybody's repeater.

### I3e — The scheduler is level-triggered and stores wall time

`scheduler.Schedule.ActiveAt` answers "which bridges should be enabled at this
instant" and keeps no memory of past evaluations. Restart recovery, missed
ticks and clock steps therefore need no code: a net in progress when QSP
restarts is picked up on the first sweep.

Windows store a weekday, a local time and an IANA zone — never an instant. A net
at 20:00 is 02:00 UTC in winter and 01:00 UTC in summer, so a stored instant
would drag it an hour off twice a year.

**The listener's sweep interval bounds how quickly a window takes effect**, and
the sweep must stay no coarser than the shortest thing it governs.

See [ADR-0015](docs/adr/ADR-0015-level-triggered-scheduler.md).

### I3f — Two mechanisms open a bridge, and they merge by OR

A bridge may be opened by the schedule, by a PTT trigger, both, or neither.
Either opening it is enough; neither can close what the other opened. A bridge
controlled by either ignores its own `enabled` field, so exactly one thing
decides each bridge.

**The frame that opens a triggered bridge must itself be relayed.** Applying the
change on the next sweep would clip the first syllable of every on-demand
transmission. See [ADR-0016](docs/adr/ADR-0016-ptt-triggered-bridging.md).

### I4 — cgo lives only at the edges

The core binary is pure Go and statically linked. The only planned cgo is the
Opus codec in the Zello connector, which ships as a separate binary
(ADR-0009). The core must remain cross-compilable to amd64, arm64 and armv7 from
one machine.

---

## 3. Packages and dependency rules

```
cmd/qsp                     wiring; the only place the graph is assembled
console                     embedded console assets

internal/domain             (phase 1) entities; no I/O, no dependencies
internal/protocol/hbp       DMR Homebrew Protocol codec (M8 complete)
internal/protocol/p25       (phase 4) P25 reflector networking
internal/routing            bridge model, routing decision, contention core, PTT triggers
internal/peers              peer identity, lifecycle, and the UDP listener (M9 complete)
internal/scheduler          scheduled bridging: level-triggered, DST-correct
internal/vocoder            (phase 5) finite resource pool
internal/audio              (phase 5) USRP transport
internal/calls              call observation: frames reassembled into transmissions

internal/config             configuration model, validation, versioning
internal/database           connection lifecycle and migrations
internal/events             publish/subscribe bus
internal/health             health-check framework
internal/logging            structured logging and canonical attribute keys
internal/audit              administrative audit trail
internal/auth               credential primitives
internal/bindcheck          can this host bind this address? (-check, and peering)
internal/server             HTTP transport, SSE
migrations                  embedded SQL
```

**Rules:**

1. `internal/logging` depends on nothing. Everything may depend on it.
2. `internal/config` depends on nothing but the standard library. It must not
   import `logging`, which is why it duplicates two small parsers — the
   duplication is deliberate and keeps the graph acyclic.
3. `internal/health` depends on nothing. Subsystems provide checkers to it;
   it never reaches into them.
4. `internal/events` is not permitted to import any subsystem. Subsystems
   publish to it.
5. `cmd/qsp` is the only package that assembles the graph. No package
   constructs its own dependencies from global state.
6. No package-level mutable state anywhere. No `init()` with side effects.

---

## 4. Concurrency model

| Component | Strategy |
|---|---|
| Event bus | `sync.RWMutex` for the subscriber set and history; a **per-subscription mutex** serialising delivery against closure |
| Health registry | `RWMutex` for registration; checks run concurrently, each in its own goroutine, each bounded by a timeout |
| HTTP server | Go's standard per-connection goroutines |
| SSE handler | One goroutine per client; bounded queue; drops are counted and surfaced |
| HBP codec | Stateless and allocation-owning: parsed messages copy variable data rather than aliasing the caller's read buffer, so a UDP server may reuse one buffer per socket |
| Peer master | Single-goroutine ownership, no internal locking. `Handle` is a pure function of (datagram, address, time); every accessor returns copies so observers never alias registry state |
| Call tracker | Owned by the listener goroutine alongside the Master; no locks. Readers use the published snapshot |
| Scheduler | Pure function of a schedule and an instant; no state at all |
| Peer listener | One goroutine owns the socket and the Master. The UDP read deadline doubles as the expiry timer, so no second goroutine or channel is needed. Shared with other goroutines: atomic counters, and an immutable peer snapshot republished after every change |
| Routing core | Single-writer actor — see I1 |

Two lessons already learned here, both worth keeping:

- **Check-then-act across goroutines is a race.** The event bus originally
  checked a `closed` flag before sending on a channel. The race detector caught
  a send-on-closed-channel window between the check and the send. The fix was a
  per-subscription mutex making check and send atomic with respect to closure.
- **An injected clock is called concurrently.** `health.Registry.Run` calls the
  injected clock from every check goroutine. Injected clocks must be
  concurrency-safe; the contract is documented on both `Options.Clock` fields.

`go test -race ./...` is a blocking gate in CI.

---

## 5. Event model

Events carry a monotonically increasing sequence number. Consumers use it to
detect gaps.

Declared types: `peer.connected`, `peer.disconnected`, `call.started`,
`call.ended`, `route.changed`, `health.changed`, `config.changed`. Publishing an
undeclared type is refused — it would appear in the console as an unexplained
event.

**Three properties:**

1. **Publish never blocks.** A subsystem handling network traffic must not stall
   because a browser tab is slow. Delivery to a slow subscriber is dropped and
   counted.
2. **Loss is never silent.** A lagging subscriber is marked, and the SSE handler
   disconnects it with a `resync` instruction rather than letting it continue
   with an incomplete view.
3. **Replay is bounded and honest.** `Replay` returns whether the retained
   history could cover the requested gap. When it could not, the client is told
   to discard local state and re-snapshot.

### The SSE synchronisation contract

```
client                                    server
  │  GET /api/events                        │
  │  Last-Event-ID: 41                      │
  ├────────────────────────────────────────►│
  │                                         │  Subscribe() → seq 57
  │                                         │  Replay(41) → events, complete?
  │  ◄── event: resync  (only if a gap) ────┤
  │  ◄── id: 42 … id: 57  (replayed) ───────┤
  │  ◄── id: 58 …         (live) ───────────┤
```

A client with no `Last-Event-ID` is told to snapshot first. It is never left to
assume the stream is the whole story.

---

## 6. Configuration lifecycle

```
web form ──► validate (every problem, not the first)
                │
                ├── invalid ──► render each field's problem and its fix
                │
                └── valid ──► checksum ──► persist as a new version
                                              │
                                              └──► atomic swap into the core
```

- **The operator never edits a configuration file.** The JSON form is a
  persistence format, not a user interface.
- **Unknown fields are rejected on load.** A typo must not silently leave the
  default in place, or the operator believes a setting was applied when it was
  not.
- **Every field error carries a fix**, not just a complaint. Enforced by test.
- **Every version is checksummed**, so two versions can be compared without
  decoding and a rollback can be verified.

---

## 7. Storage

SQLite. Migrations are embedded, ordered `NNNN_description.sql` files, applied
one per transaction together with the row recording them — a failure leaves the
database at a known version rather than half-migrated.

Three refusals, all tested:

- A migration edited after being applied. Databases would silently diverge.
- A gap or duplicate in the version sequence. A packaging error.
- A database migrated by a newer binary. Rather than corrupt it, QSP refuses to
  start and tells the operator to upgrade or restore a backup (ADR-0007).

---

## 8. Security posture

Assumptions and their consequences:

| Assumption | Consequence |
|---|---|
| Credentials must not reach versioned state | The peer password is a file path in configuration, never a value (ADR-0012); it cannot enter the version history, an export, or a diff |
| Peer-supplied text is attacker-controlled | Callsigns and descriptions arrive over UDP from anyone who authenticates. The console escapes them before rendering, and the CSP forbids inline script regardless |
| The console must not reach into live state | `/api/peers` reads a snapshot published by the listener goroutine, and renders a hand-written projection. The domain type holds each challenged peer's salt; the projection has no field for it |
|---|---|
| Every network packet is hostile | Parsers are fuzzed; malformed input must never panic |
| The console may be exposed | Strict CSP with no `unsafe-inline`; the console is built to satisfy it |
| A reverse proxy terminates TLS | Forwarding headers are honoured **only** when the operator declares `behind_proxy`; otherwise any client could forge its address in the audit trail |
| Long-lived handlers must be cancellable | `Shutdown` cancels the server's base context before waiting, or a streaming handler blocked on its request context holds shutdown until the timeout |
| Handlers can have defects | Panics are recovered into a 500 so one broken endpoint cannot drop a bridge carrying live traffic |
| Secrets reach logs by accident | `audit.Redact` matches key names over-eagerly; a needless redaction costs nothing, a leak is unrecoverable |
| Passwords will be attacked offline | PBKDF2-HMAC-SHA256, 600,000 iterations, self-describing hashes so the algorithm can be upgraded without locking anyone out (ADR-0006) |

**Not yet designed:** sessions, roles, authorisation. Per Constitution §8 these
land before any state-changing endpoint exists. There are none today.

---

## 9. Testing strategy

| Layer | Gate |
|---|---|
| Unit — domain logic, parsers, validation | every PR |
| Golden-frame protocol fixtures | every PR (once fixtures exist) |
| Fuzz — every packet parser | CI plus extended nightly |
| Integration — subsystems over loopback | every PR |
| Failure injection — the Constitution §9 list | every PR |
| Race — `go test -race ./...` | every PR, **blocking** |
| Static — `go vet`, `staticcheck` | every PR, **blocking** |
| Soak — 72 h with induced failures | pre-release |
| Hardware — real radios | **phase gates only** |

Principles that have already earned their place:

- **Named failure modes, not coverage percentages.**
  `TestReplayReportsIncompleteWhenHistoryEvicted` is findable and provable.
- **Verify against an external reference where one exists.** The PBKDF2
  implementation is tested against vectors generated by OpenSSL rather than
  against itself.
- **Time is injected.** DST and clock-jump tests are impossible otherwise.
- **No parser is written before its fixtures exist.** Constitution §3.
