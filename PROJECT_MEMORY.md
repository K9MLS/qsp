# QSP — Project Memory

**Single source of truth. Regenerated at major milestones.**
Last regenerated: 2026-08-25, at 0.1.4, after the Phase 1 gate closed and the
repository went to GitHub.

---

## 0. Read this before proposing work

**QSP is built for the amateur radio community, not for one club.** Talkgroup
numbers, upstream masters, repeater IDs, passwords and timezones are all
administrator configuration. When a question sounds like "which talkgroup does
the club want?", the answer is "that is a field, not a decision". K9MLS's club
(BCARA) is the test bed, not the specification.

**The layers, in order. Build downward before upward.**

| Layer | What it is | State |
|---|---|---|
| **1. Repeat** | a group call reaches every other peer on that talkgroup | **built 2026-08-27** |
| **2. Access control** | which talkgroups, repeaters, subscribers are permitted | **missing — next** |
| **3. Subscription** | which peers receive which talkgroups | partial: schedule and triggers; no per-peer attachment |
| **4. Bridging** | connect this master to other systems | built, including OpenBridge |
| **5. Outbound peer** | connect *out* to XLX, DMR+, IPSC2 | missing |

QSP spent two days building layer 4 before layer 1 existed, because bridging was
mistaken for the routing model. See
[ADR-0019](docs/adr/ADR-0019-master-repeats.md). That is the single most
important thing to understand about this codebase's history: **the work is
sound, the order was wrong**, and the way it was caught was a user asking why
four hotspots on one talkgroup could not hear each other.

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
| Version | 0.1.9 |
| Tests | 381, all passing |
| Race detector | clean |
| Dependencies | **one direct** — `modernc.org/sqlite`, pure Go, no cgo (ADR-0017). QSP's own code is standard library only |
| Cross-compile | linux/amd64, arm64, armv7 — all `CGO_ENABLED=0` |
| Health report | 11 subsystems |
| Hardware validated | **yes** — live voice decoded 2026-08-25, see §6 |
| CI | green, 8 jobs, `github.com/K9MLS/qsp` (private) |
| Static analysis | `staticcheck` clean, pinned at 2024.1.1 |

### Phase gates (BLUEPRINT §16)

No phase advances on a passing test suite alone. By that rule:

| Phase | Gate | Status |
|---|---|---|
| 1 — HBP master core | A hotspot keys up and its transmission decodes | **CLOSED 2026-08-25** — 5 streams, 556 frames, 0 dropped |
| 2 — Console | A newcomer is running in under 10 minutes, unassisted | open — shell is functional and plain; redesign not started |
| 3 — Scheduler + PTT | A scheduled net links and unlinks unattended for **two weeks** | open — code complete, soak not started |
| 4 — P25 | P25 and DMR live on one instance | blocked on ADR-0008 and on a capture containing P25 voice |

Phase 3's gate is two weeks of wall-clock time and cannot be compressed, so it
is now the critical path. Phase 1 closing is what unblocked it.

Phase 2 does not gate the soak. The console works; it is only unpolished. The
two can run concurrently, and should, because the fortnight is the constraint.

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
- **OpenBridge upstreams** — `internal/protocol/openbridge` for the wire format,
  `internal/upstream` for the links, `dmr.upstreams` for configuration. Signed
  DMRD frames, no handshake, no keepalive. Routes through the existing core, so
  contention and translation apply unchanged. Never tested against a real far
  end: everything is verified over loopback, and the failures that matter — a
  passphrase mismatch, an address change — only appear against a granted bridge.
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
| `RPTCL`/`MSTNAK` never seen on a wire | Capture a disconnect and a bad login. `RPTCL` needs 30 s of tcpdump while the custom network is disabled |
| Repeater-ID rewrite on relay unverified | Two peers with forwarding on. **Not** closed by the 2026-08-25 capture — one peer, forwarding off, nothing relayed |
| `description`/`slots` field split unverified | A single-timeslot hotspot |
| No manual override for Net Control | Deliberately deferred |

| No ACL check for `password_file` on Windows | POSIX hosts refuse a file readable beyond its owner; Windows cannot — `os.Stat` reports no ACL. Secure it with an ACL there |
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

## 7a. Current scope

`BLUEPRINT-v1.md` is the current plan. Read it before proposing work; the frozen
`BLUEPRINT.md` v0.4 predates any code.

**The rule that governs design decisions:** QSP is built for the amateur radio
community, not for one club. Talkgroup numbers, upstream masters, repeater IDs
and passwords are all administrator configuration. When a question sounds like
"which talkgroup does the club want?", the answer is "that is a field, not a
decision". K9MLS's club is the test bed, not the specification.

**QSP's routing model is a commercial DMR server's**, not BrandMeister's: always-on, scheduled
and on-demand talkgroup management, which is `enabled`, `schedule` and
`triggers`. The administrator sets static talkgroups; users choose among them by
programming their radios.

**Linking outward is OpenBridge, not a homebrew peer.** BrandMeister forbids
peer bridging and asks that nobody build software impersonating Homebrew or
MMDVM without an onboard radio. OpenBridge is DMRD-only with no handshake and no
keepalive — small to build, and gated on the master admin's approval.

**IPSC is required, not optional.** Motorola XPR8300, XPR8400, SLR7500 and
MTR3000 are what club sites run. Both master and peer modes are needed.

**Unblocked 2026-08-27.** There is no published IPSC specification, but the
interim rules already permit implementing from captured traffic, and the club
runs the repeaters in question — so that route needed no amendment and is
preferred. DMRlink is GPL-3.0 rather than CC BY-SA 3.0 as ADR-0008 had recorded,
so reading it where captures fall short is permitted, at the cost of attribution
and a derivative-work notice.

**Deployment targets a server or VM.** The Pi remains the proven minimum and
stays in CI.

## 8. Where the next session starts

### What actually works today

A master that accepts hotspots, repeats a talkgroup between them, bridges
between talkgroups on a schedule or on PTT, links outward over OpenBridge, and
serves a read-only console plus a member onboarding page. Protocol validated
against real hardware; 381 tests; CI green.

### What is honestly missing

This is a thin product and it is worth saying so plainly.

| Missing | Consequence |
|---|---|
| **Admin interface** | every change is SSH and a text editor. No talkgroup can be added without an operator on the command line |
| **Access control (layer 2)** | every peer receives every talkgroup any peer transmits on. Fine for a club; unsafe facing the internet |
| **Per-peer talkgroup subscription (layer 3)** | a member cannot choose what they hear |
| **Authentication** | no login anywhere; `/api/peers` discloses callsigns, radio IDs and source addresses |
| **Persistence in use** | the schema exists and migrates; nothing writes to it |
| **Live map** | closer still: the coordinates were never discarded, and are now parsed, exposed on `/api/peers` and shown in the console. What remains is drawing them, which needs the map-library decision |
| **IPSC** | unblocked by ADR-0008; needs a capture |
| **P25, vocoder, AllStar, Zello, EchoLink** | later phases, each reporting `unavailable` |

### Order I would take it

1. **Prove audio between two real hotspots.** Never done. Twenty minutes with a
   second radio, and it either confirms layer 1 or finds what no test can.
2. **Layer 2, access control.** `TGID_ACL`, `REG_ACL`, `SUB_ACL` in HBlink's
   terms. Needed before any instance faces the internet, and needed before a
   club with strangers on it.
3. **Admin interface (Phase 2b).** Needs authentication and a config write path.
   `configuration_versions` already has `author`, `summary` and `document`
   columns waiting, so this is also what finally gives the database a writer.
4. **The live map.** Store the coordinates already arriving, expose them, draw
   them. The obstacle is that a map library means a build step or a CDN, which
   cuts against the console's no-dependency rule — worth an ADR.
5. **IPSC**, once a capture exists.

### Immediate, small

- Real BCARA talkgroups in `/var/lib/qsp/qsp.json`; `11 → 9` is a placeholder.
- The join page needs one path prefix so a reverse proxy needs one rule, not
  seven.
- A BrandMeister bridge request, since approval takes as long as it takes.

## 9. The soak, in progress

Started 2026-08-27 12:44 UTC on the Ubuntu VM at `192.168.1.247`, as service
`qsp`, bridge named `bcara`.

- **Baseline:** `NRestarts=0`, `MemoryCurrent` 2.66 MB.
- **Memory after 12 samples over 5.5 h:** flat, oscillating 3.91–4.21 MB. The
  early rise was allocation settling, not a leak. Sampled every 30 minutes by
  `/etc/cron.d/qsp-memory`, readable with `journalctl -t qsp-memory`.
- **Scheduler:** windows opened and closed on time at 17:00 and 17:30 UTC.
- **Restarts:** six on day one, all explained — the systemd unit fix, config
  edits, the club rename, and three binary deploys. The pass criterion is no
  *unexplained* restarts.

`docs/SOAK.md` has the procedure, the weekly check, and what `start-limit-hit`
means.

## 10. Deployment as it stands

| | |
|---|---|
| Host | Ubuntu 24.04 VM, `192.168.1.247`, 4 vCPU / 16 GB |
| Service | `/etc/systemd/system/qsp.service`, user `qsp`, state in `/var/lib/qsp` |
| Binary | `/usr/local/bin/qsp`, built on Fedora and copied over — the VM has no Go |
| DMR | UDP 62031, all interfaces |
| Console | `192.168.1.247:8080` |
| Public | `qsp.hopto.me` via Nginx Proxy Manager to :8080. **Needs UDP 62031 forwarded on the router for remote hotspots** |
| Join page | `https://qsp.hopto.me/join` — needs seven Custom Locations until the path prefix is fixed |
