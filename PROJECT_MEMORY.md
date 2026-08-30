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
| **5. Outbound peer** | connect *out* to XLX, DMR+, IPSC2 | built, never met a real far end |

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
| Tests | 743, all passing |
| Race detector | clean |
| Dependencies | **one direct** — `modernc.org/sqlite`, pure Go, no cgo (ADR-0017). QSP's own code is standard library only |
| Cross-compile | linux/amd64, arm64, armv7 — all `CGO_ENABLED=0` |
| Health report | 11 subsystems |
| Hardware validated | **yes** — live voice 2026-08-25, and a two-station QSO across a real network 2026-08-28, see §6 |
| Members | two: K9MLS (Denton, TX) and KB9TYC (Wisconsin, WI) |
| CI | green, 8 jobs, `github.com/K9MLS/qsp` (private) |
| Static analysis | `staticcheck` clean, pinned at 2024.1.1. **It runs in the development container**: the release binary comes from GitHub, which the network policy allows, unlike the module proxy |
| Migrations | 4 — configuration versions, audit events, users and sessions, callsign cache |

### Phase gates (BLUEPRINT §16)

No phase advances on a passing test suite alone. By that rule:

| Phase | Gate | Status |
|---|---|---|
| 1 — HBP master core | A hotspot keys up and its transmission decodes | **CLOSED 2026-08-25** — 5 streams, 556 frames, 0 dropped |
| 2 — Console | A newcomer is running in under 10 minutes, unassisted | open — the console is built out, with five administration pages, but **no newcomer has tried it**. The one thing that would close this gate is somebody who is not the author following the join page |
| 3 — Scheduler + PTT | A scheduled net links and unlinks unattended for **two weeks** | open — code complete, soak running since 2026-08-27 but interrupted by daily deploys. See §9 |
| 4 — P25 | P25 and DMR live on one instance | blocked on ADR-0008 and on a capture containing P25 voice |

Phase 3's gate is two weeks of wall-clock time and cannot be compressed, so it
is the critical path. Phase 1 closing is what unblocked it.

**The soak has not had two weeks of anything.** It has been deployed to almost
daily since it began, and every restart is explained — but a fortnight of
explained restarts is not the unattended fortnight the gate describes. The clock
should be treated as running from the last deploy.

Phase 2 does not gate the soak, and the two can run concurrently.

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

Parrot was dropped from the gate's wording because QSP did not implement it.
**It does now**, per [ADR-0028](docs/adr/ADR-0028-parrot.md), as a group
call. See the caveat in §6a. It is a buffer rather than an audio feature,
which is why it arrived long before the vocoder.

Fixture: `testdata/hbp/hbp-voice-live.pcap`.

---

## 6a. What two members on a real network taught us, 2026-08-28 and 08-29

A second station joined — KB9TYC in Wisconsin, Wisconsin, about a thousand miles
from K9MLS in Denton, Texas — and **everything below was found by using QSP
rather than by testing it.** That is the pattern worth carrying forward: the
defects that mattered were all invisible to a passing suite.

### Confirmed working on air

Voice both ways on a shared talkgroup. Private calls radio to radio. Text
messages. Parrot, as a group call. Radio ID lookups against the registry.
Authentication, configuration saves from a browser, and the access control page.

### Defects found by looking at a running system

- **QSP's own security headers broke the same feature three times.** `img-src`
  blocked map tiles; `Referrer-Policy: no-referrer` made OpenStreetMap refuse
  them; `style-src 'self'` silently discarded the inline style attribute
  positioning every tile, so all of them stacked at one point. Each header is
  correct and predates the feature. **Each failed silently**, which is what a
  security header should do and what makes this class of fault nearly invisible
  from the source side. If a browser feature does not work and the code is
  provably right, check what the page is forbidden from doing.
- **Contention was eating text messages.** A DMR text is a sequence of
  single-frame data bursts, each with its own stream ID, so the contention key
  saw thirty different stations and refused all but the first. Seventeen frames
  offered, two delivered. Contention compares the station rather than the stream
  for data now.
- **A parrot replay of a private call cannot work** without decoding a DMR
  burst. The addressing lives inside the 33-byte burst, in the Link Control,
  under its own error correction, and a radio believes that rather than the
  wrapper. Swapping source and target in the HBP header looked right, passed a
  test, and produced five replays a radio muted.
- **Peer authentication had no throttling** while the console login did. Forty
  failed logins from one address in six minutes, and QSP answered every one.
- **A text message filled Last heard**, pushing out the voice traffic the panel
  exists to show, because each burst completed as its own call.

### The hotspot lessons, which are not QSP's but bite every member

A member's hotspot needs configuration QSP cannot supply, and none of it is
discoverable from either end:

- `TGRewrite` must cover the club's talkgroups or they never leave the hotspot.
  `TGRewrite0=2,2,2,2,10` passes TS2 talkgroups 2 through 11 unchanged.
- `PCRewrite` must cover **each member's own radio ID**, or private calls and
  texts addressed to them are dropped on the way in.
- Parrot's talkgroup is often outside the pass-through range and needs its own
  rule.
- A rule is found by content, never by line number: `sed -i '/^TGRewrite0=/d'`
  removed the line from *every* network block on one occasion and took the
  club's network down for an hour.

**This belongs on the join page and does not yet exist there.** Until it does,
every new member repeats the same afternoon.

### Two open questions, unresolved at handoff

**A hotspot behind a home router rebinds its NAT mapping every eight to nine
minutes.** Fifteen reconnects in two hours, on a cycle, from the day it first
connected. QSP dropped the mismatched keepalives in silence, so the peer only
recovered on its own timeout;
[ADR-0011](docs/adr/ADR-0011-nat-rebind.md) is amended and QSP answers with
`MSTNAK` now. The hotspot was also repointed from `qsp.hopto.me` to the LAN
address `192.168.1.247`, taking the router out of the path. **Neither change has
been measured yet.** Count with:

```sh
sudo journalctl -u qsp --since "20 min ago" -o cat | grep -c '"msg":"peer connected"'
```

**Private calls from KB9TYC to K9MLS are not heard**, while the reverse works
and both hear each other on talkgroups. Established: QSP relays them
(`relaying transmission ... to 3132910/TG3132910/TS2`), the frames reach the
hotspot, and DMRGateway forwards every inbound packet to MMDVMHost — a capture
on the Pi shows one loopback packet to port 62032 per packet received. What has
never been seen is an MMDVMHost line reading `network voice header from KB9TYC
to 3132910`. Next step is that grep during a private call; if the line is there,
the radio is muting it and the radio's own DMR ID is the thing to check.

**Three `PCRewrite` rules were added to the hotspot on a theory that the
capture later contradicted.** They are harmless and probably unnecessary. Do not
add more hotspot rules without evidence from a capture or a log — four were
proposed for this problem and none was the answer.

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
- Delivery: numbered patch files applied with `git am`. When one fails, run
  `git am --abort` before anything else — a half-applied patch blocks every
  later one, which turned a single failure into three this week.
- **Read the operator's `git log` before assuming a patch state.** "Already
  exists in index" means the patch landed, not that it half-landed.

### Working on somebody else's machines

Every one of these was learned by getting it wrong on a live network this week.

- **Find a line by content, never by number.** `sed -i '/^TGRewrite0=/d'`
  removed the line from every section of a config file, not the one intended,
  and took a club's network down for an hour. An index computed from `grep -n`
  is off by one against a zero-based array, which a Python `assert` caught only
  because it was there.
- **Read before writing.** A destructive edit proposed from inference and then
  confirmed by reading the file *after* the edit is not confirmation.
- **A theory that has failed twice does not get a third guess.** Four hotspot
  rules were proposed for one problem; a packet capture then showed the premise
  was wrong from the start.
- **When three fixes in a row change nothing visible, the loop is broken.** That
  is evidence about the feedback path, not a reason for a cleverer fix. Make the
  system report its own state instead — the map was fixed within an hour of it
  printing what it had measured.

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
between talkgroups on a schedule or on PTT, links outward over OpenBridge and by
dialling out as a peer, replays a transmission back to whoever sent it, resolves
radio IDs to names, and serves a console with five administration pages behind a
login. Around 740 tests; CI green.

**It carries a real network.** Two stations a thousand miles apart use it for
voice, private calls and text messages. Everything in §6a was found by that
happening rather than by the suite.

### What is honestly missing

This is a thin product and it is worth saying so plainly.

**This table listed five missing things. Four have been built**, and it is kept
with its answers rather than replaced, because what was missing and what closed
it is more use than a list of what exists today.

| Was missing | Consequence at the time | Now |
|---|---|---|
| **Admin interface** | every change is SSH and a text editor | built: access control, network settings, bridges and schedule, and a version history with restore |
| **Access control (layer 2)** | every peer received every talkgroup any peer transmitted on | built: registration, subscriber and per-timeslot talkgroup lists |
| **Per-peer talkgroup subscription (layer 3)** | a member could not choose what they hear | built: dynamic by transmitting, and static by configuration |
| **Authentication** | no login anywhere | built: accounts made from the shell, sessions in the database, proven end to end |
| **Persistence in use** | the schema existed and nothing wrote to it | accounts, sessions, configuration versions and audit events all write |

| Still missing | Consequence |
|---|---|
| **IPSC** | a club with a Motorola repeater cannot use QSP. Blocked on a capture, deliberately — see [ADR-0029](docs/adr/ADR-0029-ipsc-from-capture.md) |
| **A vocoder** | QSP relays audio without decoding it, which is why parrot works and transcoding does not |
| **Hotspot configuration guidance** | a member's own hotspot needs `TGRewrite` and `PCRewrite` rules QSP cannot supply, and the join page does not mention them. Every new member repeats the same afternoon. See §6a |
| **P25, vocoder, AllStar, Zello, EchoLink** | later phases, each reporting `unavailable` |

The live map is built, and `/api/peers` is deliberately unauthenticated: it
carries callsigns, radio IDs and coordinates, all of which are public
information in amateur radio. That has been raised and settled; do not raise it
again.

### Order I would take it

*Everything below was written before the network had two members. The first two
items are done; what replaced them is at the end of this section.*

1. **Prove audio between two real hotspots.** Never done. Twenty minutes with a
   second radio, and it either confirms layer 1 or finds what no test can.
2. **Layer 2, access control.** `TGID_ACL`, `REG_ACL`, `SUB_ACL` in HBlink's
   terms. Needed before any instance faces the internet, and needed before a
   club with strangers on it.
3. **IPSC**, once a capture exists. It is the only row in the parity table still
   marked missing, and the reason it is blocked is a decision rather than an
   obstacle: building from somebody else's implementation would make QSP's IPSC
   a derivative work permanently.
4. **An XLX reflector for outbound peer mode**, which is built and has never
   spoken to a real far end.
5. **A two-peer voice capture.** Every fixture is single-peer, and the repeat
   path, which carried the first QSO across this network, has never been tested
   against a recording of a real relay.

### Immediate, small

- The join page needs one path prefix so a reverse proxy needs one rule, not
  seven.
- A BrandMeister bridge request, since approval takes as long as it takes.

---

### Where a new session should actually start, as of 2026-08-29

1. **Hotspot configuration on the join page.** The single highest-value thing
   left, and the only one that is nobody's job but QSP's. A member's hotspot
   needs `TGRewrite` covering the club's talkgroups and `PCRewrite` covering
   their own radio ID, or their talkgroups never leave and their texts never
   arrive. §6a has the specifics. The page knows each member's radio ID, so it
   can generate the lines rather than describe them.
2. **A health check for the callsign resolver.** It can be rate-limited or
   unreachable and nothing anywhere reports it.
3. **Leave the soak alone.** Phase 3 wants a fortnight and has never had a week.
   Every restart so far is an explained deploy, which satisfies the letter and
   not the point.
4. **A newcomer follows the join page unassisted.** That is phase 2's gate and
   no substitute for it exists — the author cannot close it.

Blocked on hardware, and blocked correctly: an IPSC capture
([ADR-0029](docs/adr/ADR-0029-ipsc-from-capture.md)), a two-peer voice fixture,
an XLX reflector for outbound peer mode, and the BrandMeister request.

## 7b. What is built, as of 2026-08-29

The five layers of [ADR-0019](docs/adr/ADR-0019-master-repeats.md) are
complete, and most are proven on air.

| Layer | State |
|---|---|
| 1. Repeat | Proven on air, both directions, two stations |
| 2. Access control | Built: registration, subscribers, per-timeslot talkgroups |
| 3. Subscription | Built: dynamic by transmitting, static by configuration |
| 4. Bridging | Built, including OpenBridge |
| 5. Outbound peer | Built; never met a real far end |

Beyond the layers:

- **Authentication.** Accounts created from the shell (`qsp -config <path>
  adduser <name>`, flag before the subcommand), sessions in the database,
  lockout after five failures, `qsp unlock <name>` to clear it.
- **Peer login throttling.** Six refusals from one address in fifteen minutes
  and QSP stops answering it for five, including the challenge. Per address, not
  per repeater ID.
- **Five console pages**: overview, access control, network settings, bridges
  and schedule, configuration history with restore, plus the join page for
  members. Every section has a hint explaining itself.
- **Configuration from a browser**, versioned, with every save attributed and a
  restore that is itself a save.
- **Parrot**, as a group call. See §6a for why a private one cannot work yet.
- **Radio ID lookups** against RadioID.net, cached, one at a time, identifying
  the operator by a contact address they supply.
- **A live map**, drawn without a library.

---

## 8a. How this project finds its defects

Worth stating plainly, because it has been true every week and is the single
most useful thing to know before proposing work.

**Almost every defect that mattered was found by using QSP, not by testing it.**
The suite is large and green and has never once caught the thing that was
actually wrong. What it does is stop old faults returning, which is worth having
and is not the same job.

The pattern behind most of them: **two statements individually true, together a
lie.** Live settings were read at construction and the code that changed them
was correct, and a save reported success while nothing took effect.
`NeedsRestart` listed real fields and omitted parrot. A test asserted the
wrapper QSP writes rather than what a radio reads, and passed while five
replays went out to a muted radio. A security header was right, the feature
it silently broke was right, and the pair was wrong.

So: when something does not work and the code is provably correct, the fault is
in the gap between two correct things. Look at what the running system is
actually doing before proposing what to change.

---

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
| Database | `/var/lib/qsp/qsp.db`, schema 4. `sqlite3` is installed for inspection |
| Config | `/var/lib/qsp/qsp.json`, writable by `qsp`, editable from the console |
| Accounts | K9MLS. Create with `sudo -u qsp qsp -config /var/lib/qsp/qsp.json adduser <name>` — **the flag comes before the subcommand** |

**Deploying**, from Fedora:

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/qsp ./cmd/qsp
scp /tmp/qsp mike@192.168.1.247:/tmp/qsp
```

then on the VM:

```sh
sudo install -m755 /tmp/qsp /usr/local/bin/qsp && sudo systemctl restart qsp
```

**The two members' hotspots.** K9MLS runs WPSD at `192.168.1.155`, configured in
`/etc/dmrgateway` under `[DMR Network Custom]`, and now points at the LAN
address rather than `qsp.hopto.me`. Its logs are in `/var/log/pi-star/`, not the
journal: `DMRGateway-<date>.log` for sessions and `MMDVM-<date>.log` for what
actually reaches the radio. KB9TYC is at `198.51.100.172` in Wisconsin, Wisconsin.
