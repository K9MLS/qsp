# QSP — Project Memory

**Single source of truth. Regenerated at major milestones.**
Last regenerated: 2026-08-25, at 0.1.3, after documentation accuracy became a
CI gate.

---

## 1. What QSP is

A free, self-hosted DMR linking server for amateur radio. GPL-3.0. Copyright
K9MLS; employer clearance granted 2026-08-23.

The gap it fills: the protocols in this space are solved, the operations are
not. a commercial DMR server is proprietary and dealer-quoted; the open alternatives require
hand-editing INI files whose field names disagree with each other.

**The feature that justifies its existence is scheduled and PTT-triggered
bridging.** Both are now implemented.

---

## 2. Current state

| | |
|---|---|
| Version | 0.1.3 |
| Tests | 294, all passing (291 `Test`, 3 `Fuzz`) |
| Race detector | clean |
| Dependencies | **zero** — standard library only |
| Cross-compile | linux/amd64, arm64, armv7 — all `CGO_ENABLED=0` |
| Health report | 11 subsystems |
| Hardware validated | partially — see §6 |
| CI | never run; the repository has never been pushed |

### Phase gates (BLUEPRINT §16)

No phase advances on a passing test suite alone. By that rule:

| Phase | Gate | Status |
|---|---|---|
| 1 — HBP master core | A hotspot keys up and its transmission decodes | **CLOSED 2026-08-25** — 5 streams, 556 frames, 0 dropped |
| 2 — Console | A newcomer is running in under 10 minutes, unassisted | open — shell is functional and plain; redesign not started |
| 3 — Scheduler + PTT | A scheduled net links and unlinks unattended for **two weeks** | open — code complete, soak not started |
| 4 — P25 | P25 and DMR live on one instance | blocked on ADR-0008 and on a capture containing P25 voice |

Phase 3's gate is two weeks of wall-clock time and cannot be compressed, so it
is the critical path. It cannot start until 1 and 2 close.

### Working

- **HBP codec** — every message in `testdata/hbp/` parses and round-trips
  byte-for-byte. Fuzzed past 1M executions.
- **Peer lifecycle** — login, authentication, registration, keepalives, clean
  disconnect, rejection, timeout, NAT-rebind policy.
- **UDP listener** — one goroutine owns the socket and all live state.
- **Call observation** — frames reassembled into transmissions; a stream that
  loses its terminator is closed and flagged.
- **Routing** — bridges with talkgroup and timeslot translation, contention
  handling, never echoes to source.
- **Scheduler** — recurring windows, DST-correct, level-triggered.
- **PTT triggers** — on-demand bridging with hang time.
- **Console** — peers, last heard, traffic counters, health; live over SSE.
- **Documentation accuracy gate** — `cmd/qsp/docaccuracy_test.go` checks
  documented endpoints against registered routes, paths named in prose against
  the filesystem, emptiness claims against directory contents, and absence
  claims against the health registry. Runs as its own CI job.

### Not built

P25, vocoder pool, AllStar, Zello, EchoLink. Each registers a health check
reporting `unavailable` with the phase that brings it; the authoritative list is
`unbuiltSubsystems` in `cmd/qsp/app.go`, and a subsystem leaves it on the commit
that implements it.

Runtime status is *not* the source of truth for this. `StatusUnavailable` is
also what a built-but-disabled listener reports, so the two must be
distinguished explicitly or documentation checks acquire false exemptions.

### Known gaps

| Gap | What closes it |
|---|---|
| No live voice frame decoded | A hotspot transmission reaching QSP |
| `RPTCL`/`MSTNAK` never seen on a wire | Capture a disconnect and a bad login |
| Repeater-ID rewrite on relay unverified | A TG 9990 parrot capture |
| `description`/`slots` field split unverified | A single-timeslot hotspot |
| No manual override for Net Control | Deliberately deferred |
| No SQL driver registered | `go get modernc.org/sqlite` + blank import |
| CI never run | Push to GitHub; `staticcheck` has never analysed a line |
| No authentication on any endpoint | Designed, unbuilt. `/api/peers` discloses callsigns, radio IDs and source addresses. Bind to `127.0.0.1`; reach the console over a tunnel |
| Docs can still over-claim | The accuracy gate catches absence claims, not promises of things that do not exist. That stays a review problem |
| `overall: healthy` with 10 of 11 unavailable | Correct by the current rule, but reads oddly. Revisit before wiring alerting |

---

## 3. Architecture in one page

One statically-linked pure-Go binary.

```
UDP ──► reader goroutine ──► hbp.Parse ──► peers.Master (auth, registration)
                                                │
                                    ┌───────────┼───────────┐
                                    ▼           ▼           ▼
                              calls.Tracker  routing.Core  events.Bus
                                (observe)     (relay)         │
                                                              ▼
                                                      SSE ──► console
```

**One goroutine owns the socket, the peer registry, the call tracker, the
routing core and the triggers.** None of them carry locks. Observers on other
goroutines read immutable snapshots the loop publishes.

The UDP read deadline doubles as the sweep timer — no second goroutine, no
channels, and peer timeouts, lost calls, expired triggers and schedule changes
all evaluate on the same tick.

### Invariants (ARCHITECTURE.md §2)

| | |
|---|---|
| I1 | Routing core is a single-writer actor |
| I2 | The hot path never touches the database |
| I3 | Configuration changes are transactional |
| I3a | Protocol code derives only from captures or the published spec |
| I3b | A peer's address is part of its identity |
| I3c | Observation is not routing |
| I3d | The routing decision is pure and cannot echo |
| I3e | The scheduler is level-triggered and stores wall time |
| I3f | Two mechanisms open a bridge; they merge by OR |
| I4 | cgo lives only at the edges |

---

## 4. Decision records

16 ADRs in `docs/adr/`. The ones that shape everything else:

- **0002** Single-writer routing core
- **0004** No external dependencies
- **0008** Protocol licensing — **still OPEN**
- **0010** Codecs parse but do not interpret
- **0012** The peer password is a file path, never a value
- **0013** The routing decision is a pure function
- **0014** A transmission occupies its origin as well as its destinations
- **0015** The scheduler is level-triggered and stores wall time
- **0016** PTT triggers, and how they merge with the schedule

---

## 5. Protocol knowledge

`docs/architecture/hbp-protocol.md` is the reference. Four places where the
published specification and real traffic **disagree**, with QSP following what
was observed:

1. Spec says `MSTACK`; real masters send `RPTACK`.
2. Spec has the master pinging; real peers send `RPTPING`.
3. Spec says every minute; observed interval is 10 seconds.
4. **The DMRD flags byte.** The spec's bit table is the inverse of reality.
   Read literally, the capture yields transmissions that change timeslot
   mid-stream and carry voice-sequence numbers that do not exist in DMR. QSP's
   layout yields the A–F superframe on 14 of 14 captured streams; the table's
   reading matches 0 of 14.

**Authentication:** `SHA-256(salt ‖ password)`, verified empirically and
confirmed by a live MMDVMHost.

---

## 6. Hardware validation, 2026-08-23

A WPSD hotspot (MMDVMHost + DMRGateway, ID 3132910) registered with QSP and held
its session for minutes with keepalives cycling.

**Confirmed:** the six-step handshake, the auth digest, `RPTC` field offsets,
and the keepalive direction — which was chosen *against* the specification.

**Found three defects no test here could have:**

- Shutdown hung 15s with a console tab open. `http.Server.Shutdown` waits for
  active connections but does not cancel their request contexts, and every
  shutdown test closed a server with no client attached.
- The console asserted "No protocol, routing, scheduling or vocoder code is
  present" long after all three were built.
- Peer addresses displayed as `[::ffff:192.168.1.155]`.

**Did not confirm:** any live voice frame. DMRGateway had no rule routing a
talkgroup to the custom network, so frames never left the Pi. Diagnosed from the
traffic counters: 28 datagrams in six minutes with nothing dropped is the
keepalive rate exactly.

### 2026-08-25 — Phase 1 gate closed

A live transmission reached QSP and decoded. Five voice streams, 556 frames,
zero dropped, zero collisions; every one of the 576 LAN payloads round-trips
byte-for-byte through the codec. Frame rates land within 1.5 % of DMR's
16.67/s across durations from 3.8 s to 14.6 s.

What blocked the earlier attempt was `[DMR Network Custom] Enabled=0` in
`/etc/dmrgateway` — the WPSD dashboard reported the network as on while the
file said off, so DMRGateway never loaded it and routed everything to
BrandMeister. The talkgroup mapping is `TGRewrite0=2,11,2,9,1`: dial TG 11 on
TS2, arrive as TG 9.

Parrot was dropped from the gate's wording. It is a BrandMeister service and
QSP does not implement it, so "hears itself through parrot" was never
achievable on a QSP-only network. The substance — a live transmission
decoding — is what was verified.

Fixture: `testdata/hbp/hbp-voice-live.pcap`.

---

## 7. Working conventions

- **Approval Gate** — propose and self-review before writing substantial code;
  stop and await "Proceed".
- **No fake anything** — no stub that claims success, no seeded demo data, no
  invented protocol behaviour. If it is not implemented, the application says so.
  **This extends to static prose**: the console once described a build that no
  longer existed, which is the same failure in slower motion.
- **Capture before implementing** — protocol claims are backed by a fixture or
  marked unverified.
- **Verification scripts are less trustworthy than the code they check.** Six
  throwaway-script errors this project against zero shipped defects. Checks
  belong in Go, under CI.
- **Race detector is a blocking gate** — it has caught three real defects.
- **Documentation accuracy is a CI gate, not a habit.** Stale prose is a bug and
  is treated as one. Where a claim can be derived from code instead of asserted
  in prose, derive it — `handleNoConsole` builds its endpoint list from
  `server.apiRoutes` rather than repeating it, which removes the drift rather
  than detecting it.
- **A version bump is how a changed tree stays honestly labelled.** Two
  different trees must never carry the same version, even for a
  documentation-only change.
- Delivery: single versioned zip, `VERSION` and `CHANGELOG.md` inside.

---

## 8. Immediate next steps

1. **Push to a private GitHub repo.** CI has never run; `staticcheck` has never
   analysed a line. A versioned zip is currently the only copy, and it lives on
   one machine. This blocks nothing else technically and everything else
   practically.
2. **Close the Phase 1 gate.** A DMRGateway rule routing a talkgroup to the QSP
   network, then key up on the TG 9990 parrot. Roughly twenty minutes at the
   bench, and one session closes three documented gaps at once: the live voice
   frame, the repeater-ID rewrite on relay, and `RPTCL` on a clean disconnect.
   The codec has never decoded a voice frame from a radio; everything downstream
   relays frames the parser has not seen in anger.
3. **Stand up a server for the Phase 3 soak.** Two weeks of wall-clock time that
   nothing can compress, so it should start the day Phases 1 and 2 close. Note
   that a server validates *operation*, not the protocol — only a radio closes
   Phase 1. Deploy with the console bound to `127.0.0.1`; only UDP 62031 needs
   to face the hotspot, and there is no authentication in this build.
4. **Console visual design** (Phase 2) — the current shell is honest but plain.
   Can proceed in parallel with the soak.
5. **ADR-0008** — the licensing question remains open and blocks Phase 4.
6. **P25** (Phase 4) — also needs a capture containing an actual P25
   transmission; `testdata/p25/` holds polling traffic only.
