# QSP — Project Memory

**Single source of truth. Regenerated at major milestones.**
Last regenerated: 2026-08-25, at 0.1.4, after the Phase 1 gate closed and the
repository went to GitHub. **Duplicate sections collapsed 2026-09-02** — the
file had grown six copies of §8f in five versions, and a session read the wrong
one. Newest session notes are at the end of the §8 series; **read §8l first**,
then §8a.

---

## 0. Read this before proposing work

**QSP is built for the amateur radio community, not for one club.** Talkgroup
numbers, upstream masters, repeater IDs, passwords and timezones are all
administrator configuration. When a question sounds like "which talkgroup does
the club want?", the answer is "that is a field, not a decision". K9MLS's club
(BCARA) is the test bed, not the specification.

**Two rules break ties.** When a decision could reasonably go either way, these
settle it rather than leaving it to taste. Both are stated in full in §6c.

1. **Audio is king.** The best audio that can be delivered to the amateur
   community is the first requirement, and it overrules features, convenience
   and elegance.
2. **Talkgroup numbers are never renumbered.** 2 is 2 and 11 is 11, on both
   sides of a hotspot. See §6b.

**The layers, in order. Build downward before upward.**

| Layer | What it is | State |
|---|---|---|
| **1. Repeat** | a group call reaches every other peer on that talkgroup | **built 2026-08-27** |
| **2. Access control** | which talkgroups, repeaters, subscribers are permitted | **built**: all four lists, both protocols (ADR-0020, ADR-0044) |
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
not. The commercial options are proprietary and dealer-quoted; the open
alternatives require hand-editing INI files whose field names disagree with each
other.

**The feature that justifies its existence is scheduled and PTT-triggered
bridging.** Both are now implemented.

---

## 2. Current state

| | |
|---|---|
| Version | 0.1.29 |
| Tests | **888 test functions**, 4,202 results including subtests, all passing. Count them as `grep -rhoE '^func (Test|Fuzz|Example)[A-Za-z0-9_]*' --include=*_test.go . \| wc -l`, so the number means the same thing next time |
| Race detector | clean |
| Dependencies | **one direct** — `modernc.org/sqlite`, pure Go, no cgo (ADR-0017). QSP's own code is standard library only |
| Cross-compile | linux/amd64, arm64, armv7 — all `CGO_ENABLED=0` |
| Health report | **12** subsystems, plus one per configured link — `ipsc` became a real check at 0.1.18, having joined as unbuilt at 0.1.13 |
| Hardware validated | **yes** — live voice 2026-08-25, a two-station QSO 2026-08-28, and a three-station network with private calls working both directions 2026-08-30, see §6 and §6b |
| Members | **three**: K9MLS (Denton, TX), KB9TYC (Wisconsin, WI) and AD0MI (Post Falls, ID), joined 2026-08-30 |
| CI | green, **one job**, `github.com/K9MLS/qsp` (private). It runs on `workflow_dispatch`, weekly on Monday, and on a `v*` tag — **not on push**. Five of the old six jobs repeated what the development machine already runs before every patch; the two that do not are the three cross-compiles and `go mod tidy`. Run it with `gh workflow run CI` |
| Static analysis | `staticcheck` clean, pinned at 2026.2.1. **It runs in the development container**: the release binary comes from GitHub, which the network policy allows, unlike the module proxy |
| Migrations | **5** — configuration versions, audit events, users and sessions, callsign cache, call history (ADR-0033) |

### Phase gates (BLUEPRINT §16)

No phase advances on a passing test suite alone. By that rule:

| Phase | Gate | Status |
|---|---|---|
| 1 — HBP master core | A hotspot keys up and its transmission decodes | **CLOSED 2026-08-25** — 5 streams, 556 frames, 0 dropped |
| 2 — Console | A newcomer is running in under 10 minutes, unassisted | **CLOSED 2026-08-31** — AD0MI was given the join page and a password and got onto the network without help. He is the only evidence this gate will ever have: nobody is a first-time newcomer twice, and the next member joins a console he did not see |
| 3 — Scheduler + PTT | A scheduled net links and unlinks unattended for **two weeks** | open — code complete, soak running since 2026-08-27 but interrupted by daily deploys. See §9 |
| 4 — IPSC | A Motorola repeater is a peer of a QSP master | **gate met 2026-09-01.** An XPR8300 is registered to the production server and its transmissions are recorded. It is not yet routed; see §8d |
| 5 — P25 | P25 and DMR live on one instance | blocked on ADR-0008 and on a capture containing P25 voice. Native, never transcoded ([ADR-0034](docs/adr/ADR-0034-p25-is-native.md)) |

Phase 3's gate is two weeks of wall-clock time and cannot be compressed, so it
is the critical path. Phase 1 closing is what unblocked it.

**IPSC is ahead of P25 because of reach, not difficulty.** A club with a
Motorola repeater cannot use QSP at all today, and those clubs are already DMR
clubs running the talkgroups QSP routes — so IPSC converts a refusal into a
customer. P25 opens a mode nobody on this network operates yet. Both are blocked
only on captures and the operator has access to the equipment for both, which is
what makes the ordering a choice rather than a constraint.

**The soak has not had two weeks of anything.** It has been deployed to almost
daily since it began, and every restart is explained — but a fortnight of
explained restarts is not the unattended fortnight the gate describes. The clock
should be treated as running from the last deploy.

Phase 2 no longer gates anything; the soak is the only clock still running.

### Working

- **HBP codec** — every message in `testdata/hbp/` parses and round-trips
  byte-for-byte. Fuzzed past 1M executions.
- **Peer lifecycle** — login, authentication, registration, keepalives, clean
  disconnect, rejection, timeout, NAT-rebind policy.
- **UDP listener** — one goroutine owns the socket and all live state.
- **Call observation** — frames reassembled into transmissions; a stream that
  loses its terminator is closed and flagged, **on both listeners since
  0.1.62**. The Motorola side had no timeout at all until then and reported one
  transmission as running for seven hours; see §8j.
- **IP Site Connect** — voice both directions, group and private (ADR-0046),
  text (ADR-0045), parrot, access control (ADR-0044). A transmission is counted
  at three layers — received, converted, delivered — and the counts are in the
  end-of-call line.
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

P25, AllStar, Zello, EchoLink. Each registers a health check
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
| Last-heard is built from two sources with no dedup | A decision about which owns it. `DeliverFromIPSC` observes into the shared tracker *and* the IPSC listener contributes its own call views, so a Motorola over should appear twice. See §8j |
| A text over IPSC produces no call record | The text branch never reaches `recordVoice`, so it has none of the per-layer counters |

| No ACL check for `password_file` on Windows | POSIX hosts refuse a file readable beyond its owner; Windows cannot — `os.Stat` reports no ACL. Secure it with an ACL there |
| No authentication on any endpoint | Designed, unbuilt. `/api/peers` discloses callsigns, radio IDs and source addresses. Bind to `127.0.0.1`; reach the console over a tunnel |
| Docs can still over-claim | The accuracy gate catches absence claims, not promises of things that do not exist. That stays a review problem |
| `overall: healthy` with 11 of 12 unavailable | Correct by the current rule, but reads oddly. Revisit before wiring alerting |

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

- **Superseded by §6b. Do not follow this list for a QSP-only hotspot**, which
  needs no rewrite rules at all. It is kept because it describes what a hotspot
  carrying several networks still needs.
- `TGRewrite` must cover the club's talkgroups or they never leave the hotspot.
  `TGRewrite0=2,2,2,2,10` passes TS2 talkgroups 2 through 11 unchanged — and a
  range like this is exactly what broke when the club added a talkgroup outside
  it.
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

## 6b. What a second day on the network taught us, 2026-08-30

### A talkgroup number is the same on both sides of a hotspot

**This is settled and is not to be relitigated.** 2 is 2, 11 is 11. QSP
publishes a number, a member dials it, and nothing between them renumbers
anything.

The alternative was tried and cost most of a day. §6a above recommends
`TGRewrite0=2,2,2,2,10` and per-talkgroup rules, and both are now wrong: a rule
covering one number silently breaks the next talkgroup the club adds. The
console no longer offers an `Arrives` field, and `internal/hotspot` never emits
a rule that maps one number to another.

**A hotspot carrying only QSP needs no rewrite rules at all** — `PassAllTG` and
`PassAllPC` on both slots, nothing else, nothing to maintain. That is what most
members should run and what the generator produces for them.

### Turning off automatic rewrites does not remove the rules

WPSD writes `WPSD_AutoRewrites=0` and **leaves all fourteen generated rules in
place**. The dashboard toggle looks like it removed them. Restarting DMRGateway
reloads exactly the same rules and the log looks identical.

The Denton hotspot spent an afternoon connected, authenticated, keepaliving, and
dropping every transmission because a prefix-9 rule set survived the toggle and
had no entry for TG 2. MMDVMHost logged the RF at BER 0.4%; no voice packet ever
left the Pi.

**A rule set is confirmed by reading the DMRGateway log after a restart**, not
by the dashboard and not by the file:

```sh
sudo grep -A8 'QSP, Opening DMR Network' /var/log/pi-star/DMRGateway-$(date -u +%Y-%m-%d).log
```

No `Rewrite RF` lines under QSP is the goal.

### Rules written before ADR-0019 are still being found

Three separate validation rules assumed bridges were the only way traffic moves,
which stopped being true when the master learned to repeat:

1. Forwarding with no bridges was refused — the ordinary club network.
2. An enabled link was required to carry `export` or `import` lists, which
   contradicted the rule requiring a bridge to name it. **This one stopped the
   live network from starting** and took it down for twenty minutes.
3. A published join talkgroup was required to be on a bridge, which refused to
   save a configuration describing a network that works.

**When a rule mentions bridges, check whether `dmr.forwarding` makes it false.**
None of the three was caught by the suite: each test asserted the rule against
the world the rule assumed.

### Nothing could send traffic to a link

`routing.Endpoint` carried an `Upstream` field that was only ever set on the
inbound side, and `config.Endpoint` had no way to name a link at all. Traffic
could arrive from another network and never leave for one; outbound was
unreachable from any configuration a person could write.

`Upstream.Export` and `Upstream.Import` were validated, stored, documented, and
read by no routing code. **A bridge endpoint names a link now**, and an enabled
link nothing routes to is refused at startup.

### A config is checked before a restart, not by one

```sh
sudo qsp -config /var/lib/qsp/qsp.json -check
```

Reads the file, says whether it is valid, exits. No socket, no database, nobody
dropped. It needs `sudo` because `/var/lib/qsp` holds peer passwords.

The twenty-minute outage happened because a configuration edit was verified by
restarting the service. systemd then gave up after five attempts and needed
`systemctl reset-failed` before it would try again.

### Two servers have carried a frame between them

Alpha and bravo on one machine, peered over OpenBridge: alpha sent, bravo
received, the link reported healthy. **This is the first time any upstream path
has met a real far end.** `scripts/pair.sh` runs it; `docs/FEDERATION-TEST.md`
says what it does and does not prove.

What it exposed: there was nowhere in the console to see a link. That is what
the Links page and `/api/links` are for.

### The console is not yet a tool an administrator can rely on

Two findings from the evening of 2026-08-30, both from an operator asking why
something looked wrong rather than assuming it was fine. Neither is a broken
network; both are the console failing at the job it exists for.

**Last heard holds fifty entries and loses them on restart.** `calls.DefaultHistory`
is a package constant, not configuration, and the ring buffer drops the oldest
when full — confirmed on air by keying up six times at 47 and watching it stop at
50. It is in memory only.

The use case that decides the design is **net control taking check-ins**. A net
runs, twenty stations check in, and the log is the only record of who was
actually there when somebody's callsign was missed. That makes it a *record*
rather than a display, and a record has to hold a whole net and survive the
restart that follows a deploy. Three stations on one talkgroup filled a third of
the buffer in two hours; a net plus the conversation either side of it would push
the early check-ins off before anybody read them.

Persisting it because losing it is annoying is the weak argument. Persisting it
because net control needs it tomorrow is the one that also settles retention,
export, and how much to keep.

**The dropped-datagram counter cannot be investigated.** Production shows `2`
dropped, unchanged across three stations and thousands of frames, painted amber.
The reason for each drop is logged at **debug**, and production runs at `info` —
so the explanation was never written. Raising the log level requires a restart,
and the counter reads `SINCE START`.

**An operator cannot see why a number is what it is without destroying the
number.** That is the defect. The count is almost certainly the MSTNAK rebind
path of ADR-0011, which is QSP working correctly, and it is unverifiable.

A permanently amber number that means "working correctly" teaches an operator to
ignore amber — which `console.css` already argues about spending amber on
ordinary conditions. Candidate answers, none built: log a refusal at `info`;
count refused-and-answered separately from genuinely lost; and do not paint the
expected kind amber.

### The private call is resolved, and the cause was not isolated

K9MLS and KB9TYC held a private-call QSO on 2026-08-30. It had been failing in
one direction since 2026-08-28.

Three things changed on that path in between: the MSTNAK amendment to ADR-0011,
repointing the Denton hotspot at the LAN address, and **stripping fourteen
rewrite rules from the QSP block**, six of which were `PCRewrite` lines. §6a
records three of those as having been added on a theory a later capture
contradicted, so that is the suspicion — a `PCRewrite` naming the wrong ID drops
a private call exactly this quietly. It was not isolated and should not be
written up as though it were.

**§6a's advice to give each member a `PCRewrite` covering their own radio ID is
superseded**, in the same way its talkgroup advice is: a QSP-only hotspot needs
no rewrite rules at all, and removing them is what coincided with the fix.

### An echo that is still unexplained

During that QSO, after unkeying, K9MLS heard roughly a second of his own audio
return once.

QSP is provably not the cause. Every `relaying transmission` line in the journal
excludes the originating peer — a frame from 3132910 goes to 3155413 and 3127045
and never back — so the repeat path is correct. The third station is in Idaho and
cannot be transmitting into a Denton receiver.

Also unexplained, and possibly related: **the same peer produced the same 32-bit
stream ID twice, 34 seconds apart**, within that QSO. Two identical stream IDs by
chance is roughly one in four billion. An earlier session saw six `call started`
events in three seconds from one operator, six distinct IDs.

Do not theorise further without a packet capture. Four theories were proposed for
the private call and none was the answer.

## 6c. Two rules that break ties, 2026-08-31

### Audio is king

**The best audio that can be delivered to the amateur community is the first
requirement, and it overrules features, convenience and elegance.** This is the
rule that decides an argument nobody can win on the merits, and it has already
decided two.

**Talker Alias is passed through and never injected.** QSP carries the burst
verbatim, so an alias a radio already sends crosses untouched and costs nothing.
Injecting one means writing bursts B–E into the voice superframe, which is
reported to produce distorted or lost audio on Motorola repeaters and overwrites
the Link Control that a radio joining mid-transmission needs to know who is
talking. Trading audio quality for a name on a screen is exactly the trade this
rule forbids.

Note the correction that came with it: **BrandMeister does inject**, when a
radio sends nothing. It prefixes the callsign to the operator's SelfCare *APRS
Text* field, defaulting to `DMR ID:nnnnnnn`. It is not a name lookup against a
database, so what it actually delivers is smaller than its reputation suggests,
and copying it would cost more than it returns.

**P25 is native and is never transcoded to reach DMR**
([ADR-0034](docs/adr/ADR-0034-p25-is-native.md)). IMBE and AMBE+2 are different
vocoders; routing one through the other is tandem vocoding, and tandem vocoding
is the single worst thing that can be done to speech in this hobby. A P25
network and a DMR network on one instance are two networks, not one.

### A member is removed without changing everybody's password

[ADR-0035](docs/adr/ADR-0035-per-peer-passwords.md). `dmr.peer_passwords` names
a directory of files, one per radio ID. A peer with a file of its own
authenticates against it; every other peer uses the shared password unchanged.

One shared secret means removing one person costs a new password and every
remaining member reconfiguring a hotspot on the same evening — twelve members,
twelve reconfigurations, to remove one. It also leaks through whoever is least
careful with it and nobody can tell which of them it was; one of this network's
passwords reached a chat log inside a week.

**A per-peer password overrides rather than adds**, and revocation depends on
that property. If the shared password still worked for a peer that has its own,
deleting somebody's file would quietly return them to the secret they already
know, and an administrator would believe they had revoked access they had in
fact restored. Both issue and removal are in the console, and both write an
audit event naming the administrator and the radio ID, because *who removed
whom, and when* is the question a club asks afterwards.

Removing a password stops the **next** login, not the current session. The
access list is what puts somebody off the network now, and the panel says so.

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
  **In the development container it needs `CGO_ENABLED=1` explicitly.** The
  default there is 0, and `go test -race` then refuses with *"-race requires
  cgo"* rather than running and passing — a gate that declines to run looks
  nothing like a gate that passed, but it scrolls past the same way. `gcc` is
  present, so exporting the variable is the whole fix.
- **The development container ships without Go, and no allowed domain carries a
  Go binary.** `go.dev/dl` and the module proxy are both outside the egress
  allowlist; `golang/go` on GitHub publishes source, not binaries; Ubuntu's
  newest package is 1.22. So the toolchain is bootstrapped from the source tag
  on `codeload.github.com`, and 1.22 cannot build 1.27 directly — the bootstrap
  minimum is enforced at run time, not by a build tag, so the chain is
  **1.22 → 1.23 → 1.24 → 1.27**, about twenty minutes. Do it first, before
  writing anything, because a documentation-only patch still has to pass the
  accuracy gate and the accuracy gate is a Go test.
- **The container reaps background processes between commands.** Nothing
  survives a `nohup ... &`; a build started in the background is dead by the
  next command, with no error and an empty process table. It is also a single
  core, so "about twenty minutes" is optimistic.
- **The bootstrap chain is 1.22 → 1.23 → 1.24.6 → 1.27**, and the patch release
  matters: `go1.24.0` is refused with *"does not meet the minimum bootstrap
  requirement of go1.24.6 or later"*. `make.bash` cannot finish inside one
  command — it spends 3m39s on toolchain1, 2 and 3 before reaching the phase
  that takes minutes, and re-running repeats all of it. But those phases write
  `compile`, `link` and `go_bootstrap` into `$GOROOT/pkg/tool/linux_amd64` and
  they survive, so run `make.bash` once and then finish the last phase directly:

  ```sh
  cd $GOROOT/src
  $GOROOT/pkg/tool/linux_amd64/go_bootstrap install std
  CGO_ENABLED=0 GOFLAGS="-trimpath -ldflags=-w -gcflags=cmd/...=-dwarf=false" \
    $GOROOT/pkg/tool/linux_amd64/go_bootstrap install cmd
  ```

  That is the pair of calls Go's own dist bootstrap makes at the end of
  `make.bash`, with its `toolenv()` spelled out.
- **`modernc.org/sqlite` cannot be fetched in the container either — and moving
  `cmd/qsp/driver_sqlite.go` aside disarms the documentation gate.** Four files
  name that path, so removing it fails `TestDocumentedPathsExist` for a reason
  that has nothing to do with documentation. That is why the failure count read
  eight and why the count hid a real one: §7 requires a documentation-only patch
  to pass the accuracy gate, and the gate could not pass. Supply the import from
  a workspace above the repository instead, so the file stays where the
  documents say it is:

  ```sh
  mkdir -p /home/claude/sqlitestub
  printf 'module modernc.org/sqlite\n\ngo 1.27\n' > /home/claude/sqlitestub/go.mod
  printf 'package sqlite\n' > /home/claude/sqlitestub/sqlite.go
  printf 'go 1.27\n\nuse ./qsp\nuse ./sqlitestub\n' > /home/claude/go.work
  export GOWORK=/home/claude/go.work
  ```

  `go.work` lives outside the tree because it is **not** in `.gitignore` and the
  patch pathspec excludes only `go.mod` and `go.sum`. **The container baseline is
  seven failures, all in `cmd/qsp`, all from no registered driver:**
  `TestHealthReportsUnbuiltSubsystemsHonestly`, `TestPersistenceIsRealNow`,
  `TestSchemaSurvivesARestart`, `TestTheConnectionDoesNotTakeSQLitesDefaults`,
  `TestTheAuditTrailReachesTheDatabase`, `TestACompletedCallSurvivesARestart`,
  `TestRetentionIsByAgeAndCanBeNothing`. **None of this applies to the Fedora
  machine**, where the proxy works and a stub would shadow the real driver.
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
- **Ask the running binary which commit it is — and `qsp --version` cannot.**
  An afternoon went on diagnosing a bridge that was not deployed: the service
  was `active`, the deploy commands were right, and the binary was three commits
  old because the patch file had never reached the machine. `systemctl
  is-active` says something started; only the version says *what*. But
  `--version` executes the file on disk, which `install` has already replaced,
  so it answers identically before and after a restart, and in the container it
  reads `development` because the Dockerfile hardcodes it. **The check is the
  `starting` log line**, emitted by the process that is running, with the same
  string `--version` would print:

  ```sh
  sudo journalctl -u qsp -n 5000 --no-pager | grep -i starting | tail -3
  ```

  **`tail`, not `grep -m3`.** `journalctl` prints oldest first, so `-m3` stops
  at the three oldest starts in the window and answers with a version from the
  previous day — which it did, convincingly. In the container there is no
  journal, so the check is a string the new build introduced, confirmed unique
  by `git log -S` before it is trusted.
- **A field that is validated is not a field that was set.** `ipsc.colour_code`
  was a `uint8` checked for range, and 0 is a legal colour code — so a
  configuration that never mentioned it validated, started, and built every
  burst wrong. Where absent and zero mean different things, the type has to be
  able to say so.

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
- **`sudo` at the head of a pasted block eats the next line.** The password
  prompt reads standard input, so the following command goes in as the password:
  the block appears to run, one command silently does not, and the failure shows
  up only as `Permission denied, please try again`. It cost a `scp` retry this
  week and was the leading theory for half an hour of a deploy that had actually
  worked. `sudo -v` on its own line first, then paste.
- **A command in the wrong terminal can succeed and mean nothing.** A
  `systemctl restart qsp` meant for the server ran on Fedora, where there is no
  such unit, and `MainPID` came back `0` — so the next command read
  `/proc/0/exe`. The error was legible only because the unit was missing.
- **Fedora has a stray `/usr/local/bin/qsp`** of unknown age, at exactly the
  path the deploy documentation names. Nothing runs it, and that is the hazard:
  a server command that lands on Fedora is answered rather than refused.

---

## 7a. Current scope

`BLUEPRINT-v1.md` is the current plan. Read it before proposing work; the frozen
`BLUEPRINT.md` v0.4 predates any code.

**The rule that governs design decisions:** QSP is built for the amateur radio
community, not for one club. Talkgroup numbers, upstream masters, repeater IDs
and passwords are all administrator configuration. When a question sounds like
"which talkgroup does the club want?", the answer is "that is a field, not a
decision". K9MLS's club is the test bed, not the specification.

**QSP's routing model is the commercial one**, not BrandMeister's: always-on,
scheduled
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

## 8b. Where the next session starts, as of 2026-08-30

**Superseded by §8c.** Kept because its *settled, do not reopen* list is still
in force. Read §6b first; it supersedes parts of §6a.

### Settled today, do not reopen

- **Nothing renumbers a talkgroup.** 2 is 2, 11 is 11. `Arrives` is deprecated,
  the console does not offer it, and generated configuration never maps one
  number to another.
- **A bridge endpoint names a link.** That is how traffic reaches another
  network; `Export` and `Import` route nothing.
- **A peering is agreed by two people** ([ADR-0032](docs/adr/ADR-0032-peering-is-agreed.md)),
  by an invitation that travels separately from its passphrase. Nothing changes
  on the wire.
- **The loop rule already existed.** `routing.Core.route` refuses to send a
  frame that arrived on a link to any link, which makes loops unformable rather
  than detectable. [ADR-0031](docs/adr/ADR-0031-loop-prevention.md) was written
  before that was read and is amended to say so.

### Working, on air, as of 2026-08-30

Three stations across three states: Denton TX, Wisconsin WI, Post Falls ID. Voice,
private calls both directions, text and parrot. AD0MI's hotspot announces 0, 0
and is refused a position on the map until he sets one, which the console says
plainly.



The Denton hotspot carries **no rewrite rules** — `PassAllTG` and `PassAllPC` on
both slots, `Location=1`, and every talkgroup passes untouched. Both stations
are connected to production. Add a talkgroup in the console and it works with no
change on any Pi.

### Open, in the order I would take them

1. **Ask AD0MI what he had to work out for himself.** He is the first member who
   joined after this project started, which makes him the only evidence
   that exists about phase 2's gate. His answer decides whether the gate closes
   and what the join page is still missing. This costs one conversation and is
   worth more than any amount of code.
2. **The console as a tool an administrator relies on** — last heard as a record
   that survives a restart, and a dropped counter an operator can investigate.
   Both are in §6b. This wants an ADR before code, because "how much history does
   a club need" is a judgement about clubs.
3. **Paul's TYT model and firmware.** Decides whether Talker Alias is a
   pass-through that already works, a userdb flash on his radio, or a feature
   that has to be built. Ask before designing anything: BrandMeister does *not*
   look names up — it passes through what a transmitting radio sends.
3. **Six `call started` in three seconds**, six distinct stream IDs, from one
   operator. Never explained. Not obviously a fault, and not obviously not one.
4. **The peering retry**, now that the console flow and the config checker both
   exist. Production offers, Fedora accepts. Two administrators is one person
   with two terminals, but the mechanism is what club #2 will use.
5. **A second instance off this machine.** Loopback cannot produce NAT, a
   changing address, or a far end that restarts, which are the failures that
   actually break a peering.

### Not built, and named as required

- **Creating a link is in the console; nothing shows a peering's history.**
  ADR-0032 asks for an audit event and one is written. Nothing displays it.
- **A server identity block.** `UpstreamIdentity` carries callsign and
  coordinates *per link*, so three links means stating a callsign three times
  with nothing keeping them consistent. Decided: decimal degrees, matching what
  Pi-Star and the DMR config message already carry.
- **Subscription and a 4000-style unlink.** [ADR-0023](docs/adr/ADR-0023-talkgroup-subscription.md)
  is written and unimplemented. The unlink talkgroup should be configuration,
  not a constant, for the same reason parrot's is.

### Costs to respect

GitHub Actions minutes are finite and were at 90% of the month on 2026-08-30.
**CI runs on push, not on commit.** Apply patches and test locally; push once
when CI is actually wanted, or use `[skip ci]` in the commit message.

## 8c. Where a session started on the morning of 2026-09-01

**Superseded by §8d**, which is where a session starts now. Kept because its
*settled, do not reopen* list is still in force, and because the "open, in
order" list below shows what the day looked like before it: IPSC was blocked on
a capture that did not exist.

### Settled, do not reopen

Everything in §8b's list, plus:

- **Phase 2's gate is closed.** AD0MI joined unassisted from the join page and a
  password. Do not propose re-testing it; there is no second first-time member.
- **IPSC comes before P25.** Reach, not difficulty. See §2.
- **Audio is king**, and **talkgroup numbers are never renumbered**. §6c and §6b.
- **Talker Alias is pass-through.** Nothing injects bursts B–E.

### Open, in order

1. **IPSC. The captures exist; what is missing is voice and a listener.** On
   2026-09-01 two Motorola repeaters registered to each other over the internet
   with a capture host in the path — a K9MLS XPR8300 as master and a remote
   repeater as peer. Six fixtures in `testdata/ipsc/`, seven message types, and
   `internal/protocol/ipsc` parses the envelope they all share.

   **What is known:** byte 0 is the type, bytes 1–4 are the sender's own radio
   ID big-endian, confirmed across seven types and two repeater models.
   Registration is six packets, not two. An unregistered peer retries at ten
   seconds; a registered one keepalives at fifteen. A peer never sources from
   the master's port. ICMP unreachable is ignored, so a master cannot refuse
   anybody by staying silent.

   **What is not:** voice, private calls, text, disconnect. Nine of `0x91`'s
   sixteen bytes and thirty-nine of `0xf1`'s forty-four. The purposes of `0x85`,
   `0xf0` and `0xf1`. Whether a master behind NAT advertises an address a remote
   peer can reach — the link came up through NAT but nothing has been decoded
   that would say how.

   **Next:** a session with somebody at the far end who can key up. Voice on
   both timeslots, a private call, text, and a clean disconnect, in one capture,
   with the same bridge. A third repeater would make `0xf1` worth decoding,
   because one peer cannot distinguish a list from a fixed record.

   **[ADR-0029](docs/adr/ADR-0029-ipsc-from-capture.md) still holds** for
   everything not captured: no constant, no message type, no parser for anything
   the fixtures do not contain. DMRlink and HBlink3 remain unread and must stay
   that way — reading them binds the project to a derivative work permanently.

2. **The peering retry between two machines.** The console flow was built and no
   button has been pressed. The loopback pair proved the protocol and proved
   nothing about NAT, a public address, or a far end that restarts. The one real
   attempt took production down for twenty minutes, which is the argument for
   `qsp -config <file> -check` having been built first.

3. **Subscription on air.** Built, never run with a radio. Turning it on
   silences everybody who has not yet transmitted, so static attachments have to
   be configured first — and those are still not editable from the console. That
   ordering is the whole risk.

4. **P25**, once DMR is finished. `testdata/p25/CAPTURE-REQUEST.md` is written;
   the idle capture exists and contains no voice.

### Unexplained. Do not theorise without a capture

- **An echo.** K9MLS heard about a second of his own audio return after
  unkeying, once. QSP is provably not looping: every `relaying transmission`
  line excludes the originating peer, and the third station is 1,300 miles away.
- **A repeated stream ID.** One peer produced the same 32-bit stream ID twice,
  34 seconds apart, in the same QSO. Roughly one in four billion by chance. An
  earlier session saw six `call started` events in three seconds from one
  operator, with six distinct IDs.

Both are recorded rather than investigated because there is no capture of
either, and §7 says a theory that has failed twice does not get a third guess.

### Costs to respect

CI runs on `workflow_dispatch`, weekly on Monday, and on a `v*` tag — **not on
push**. One job, about six minutes. `gh workflow run CI`.

**A Motorola repeater has two port fields and they are not the same thing.**
`Master UDP Port` is the master it dials; `UDP Port` is the port it binds. Set
to 50000 and 50001, a master that looked correctly configured served a port
nobody was calling, sent no UDP at all for fifteen minutes, and answered every
request with an ICMP unreachable from its own IP stack. Two plausible theories
came before the right one and both were wrong; what ended it was `nmap -sU`
against the repeater, which found exactly one open port. **When a device's own
stack sends the refusal, ask the device what it bound rather than re-reading the
configuration page.**

`cmd/qsp` has known failures in the development container because no SQLite
driver is registered there. **List them by name rather than counting them** —
two new failures once hid inside a count that looked normal.

---

## 8d. Where a session started on the evening of 2026-09-01

> **SUPERSEDED — read [§8g](#8g-where-the-next-session-starts-as-of-2026-09-03) instead.** This section is kept for the reasoning in it, not for its
> state. **Its open list is wrong**, and wrong in the direction that wastes a
> session: it describes IPSC as having no voice and no listener, which was true
> when it was written and has not been since. A stale section that sits above
> the current one is read first by anybody going top-down.

**Superseded by §8e.** Kept for its *settled, do not reopen* list and because
its "open, in order" shows the state before the audio path was built.

Read §0, then §6b and §6c for the rules that break ties, then this.

**IPSC went from an empty directory to a Motorola repeater registered on the
production server in one day.** Seven fixtures, nine message types, two ADRs, a
listener in the binary and a proved-lossless audio conversion. None of it came
from reading anybody's implementation.

### What exists

- `internal/protocol/ipsc` parses nine message types. The envelope is a type
  byte and a big-endian **sender** ID — the sender, not the subject, which one
  repeater talking into silence could not have shown.
- `internal/ipsclink` is a listener wired to configuration, health and the
  lifecycle. A repeater registers, keepalives are answered at fifteen seconds,
  transmissions are recorded as calls. Live on `qsp-server:50000`.
- `internal/dmrfec` converts between IPSC's 49-bit vocoder parameters and the
  72-bit protected frames a DMR burst carries.
  [ADR-0037](docs/adr/ADR-0037-dmr-fec-is-a-wrapper-not-a-codec.md). **884 real
  bursts round-tripped bit-exact.**
- `cmd/ipsc-probe`, an experiment that answers a repeater with recorded bytes.
  Not part of `qsp` and should stay that way.

### Open, in order

1. **IPSC → HBP routing.** Motorola in, hotspots out. **Every piece exists** —
   parser, FEC, routing core — and it needs no new capture and no equipment.
   Key the XPR8300 and hear it in Wisconsin.

2. **HBP → IPSC, which is blocked and must stay blocked.** *Nothing has ever
   captured a master sending voice to a repeater.* What QSP would emit is a
   guess, and a repeater that receives malformed voice may key its transmitter
   with it. Build direction 1 first, then use it: send a burst known to be
   well-formed and watch whether the repeater transmits.

3. **The console page.** The listener holds peers and calls and nothing reads
   them, so a repeater is visible in `/healthz` and the journal but not on the
   dashboard.

4. **Two key-ups on different talkgroups.** Two minutes at the radio. Every
   captured transmission read destination 455, so nothing has ever *moved* that
   field — the Link Control in the same packet agrees with it, which is
   corroboration and not proof.

5. **Subscription on air**, then **P25**, unchanged from §8c.

### Settled, do not reopen

Everything in §8b and §8c, plus:

- **The FEC conversion is a wrapper, not a codec** (ADR-0037). Parameter bits
  are copied and never inspected. A change to `internal/dmrfec` that reads one
  is out of scope and needs a new ADR.
- **Where a published standard specifies the DMR side, implement the standard
  and use the captures as the test** ([ADR-0040](docs/adr/ADR-0040-the-air-interface-is-specified.md)).
  ADR-0029 governs IPSC unchanged, because IPSC has no specification. Reading
  ETSI is not reading another implementation; DMRlink and HBlink3 stay unread.
- **Read the flags before the payload.** Two versions of the terminator patch
  emitted zero terminators on real traffic, because the frame carrying the
  last-frame flag has no readable vocoder payload and both returned early. A
  frame that cannot be decoded can still be the end of a transmission, and
  ending one matters more than its last 60 ms of audio.
- **A wrong hypothesis scores zero, and so does a right hypothesis evaluated by
  broken arithmetic.** Thirty thousand candidate constructions scored zero while
  the correct field polynomial and generator roots sat inside the search space;
  the division applying them was wrong. The score cannot tell the two apart.
  **Where a standard gives both a generator matrix and a generator polynomial,
  use the matrix** — no division, and division is where this went wrong.
- **`0xf1` is not a peer list.** It contains neither the peer's radio ID in
  either byte order nor any address or port. Tested.
- **DMRlink and HBlink3 remain unread**, and one implementation that surfaced
  during research on 2026-09-01 was deliberately not opened. ETSI TS 102 361 and
  the P25 half-rate vocoder specification are published standards and reading
  them is a different thing entirely.

### What cost the most time, and would again

- **A repeater will not register with a master carrying its own radio ID.** An
  XPR8300 retried thirty-nine times over six minutes against correct replies
  sent promptly, and the failure was indistinguishable from a protocol fault.
  `ipsc.master_id` now refuses the collision at validation.
- **A Motorola repeater has two port fields and they are not the same thing.**
  `Master UDP Port` is what it dials; `UDP Port` is what it binds. Set to 50000
  and 50001, a master that looked correct served a port nobody was calling and
  answered every request with an ICMP unreachable **from its own IP stack**. Two
  plausible theories came before the right one and both were wrong; `nmap -sU`
  against the repeater ended it. **When a device's own stack sends the refusal,
  ask the device what it bound.**
- **Silence is the only refusal IPSC has.** ICMP port unreachable is provably
  ignored, and no capture contains a rejection.
- **Nothing says goodbye.** A repeater that is unplugged simply stops, so
  silence past a timeout is the only evidence of departure.
- **An IPSC port on a public address will be found.** One repeater turned up
  unannounced during a bench test.

### The method, stated plainly because it was proved four times in one day

**Every byte read by eye was wrong. Every differential was right.**

The trailer, the master ID, the "timeslot" that was a call counter, the
interleave geometry — each was settled by changing exactly one thing and
diffing, or by running candidate readings against thousands of real frames and
taking the one that scored 99% where the others scored zero.

Two captures differing in one known way beat ten differing in unknown ways. When
a reading is plausible and cheap to test, test it.

---

## 8e. Where the next session starts, as of late on 2026-09-01

> **SUPERSEDED — read [§8g](#8g-where-the-next-session-starts-as-of-2026-09-03) instead.** This section is kept for the reasoning in it, not for its
> state. **Its open list is wrong**, and wrong in the direction that wastes a
> session: it describes IPSC as having no voice and no listener, which was true
> when it was written and has not been since. A stale section that sits above
> the current one is read first by anybody going top-down.

Read §0, then §6b and §6c, then this.

**IPSC went from an empty fixture directory to a Motorola repeater on the
production server with a proved-lossless path to the rest of the network, in one
day.** Nineteen patches, 0.1.11 to 0.1.29. Nothing was derived from another
implementation: DMRlink and HBlink3 remain unread, and one that surfaced during
research was deliberately not opened.

### The audio path, which is finished as a matter of discovery

`internal/dmrfec` converts between IPSC's 49-bit vocoder parameters and the
72-bit protected frames a DMR burst carries, and builds every part of a burst:

| Piece | Evidence |
|---|---|
| Vocoder FEC | 884 real bursts round-tripped bit-exact |
| The 19-byte IPSC core | three 50-bit slots, spare bit trailing |
| The 48-bit middle | 740 captured middles rebuilt from a position and a colour code |
| EMB | one generator of 256 fits every captured value |
| Superframe order | 73 of 74 superframes agree |
| BPTC(196,96) | 252 of 252 rows valid, 28 bursts round-tripped bit-exact |

`internal/ipscbridge` turns a Motorola voice frame into a Homebrew burst: 54
produced from real traffic, one sync in six, every vocoder payload unchanged.

**The single strongest piece of evidence in the project**: a Motorola XPR8300
over IPSC and an MMDVM hotspot over Homebrew, captured on different days on
different equipment, produce the identical 49-bit vocoder frame for silence —
`0x1F003533F19C1`, 236 times in the Homebrew capture. That one observation
confirms the packing, the FEC and the claim that both protocols carry the same
audio.

### Open, in order

1. **Wire the converter to routing.** Hand bursts to peers so a Motorola
   repeater is audible on a hotspot. **No discovery left, only wiring.**
2. **Three small unknowns, each cheap.**
   - Which slot bit value means timeslot 1. **One sentence from the operator**;
     it was never written down at the radio. `SlotBitIsTimeslot2` is
     configuration until then.
   - The destination field. Every transmission ever captured reads 455, so
     nothing has *moved* bytes 9–11. One key-up on any other talkgroup.
   - The Link Control checksum, needed for voice headers and terminators.
     A fitting exercise against the 28 data bursts already in `testdata/hbp/`;
     no equipment.
3. **The console page.** The listener holds peers and calls and nothing reads
   them, so a repeater shows in `/healthz` and the journal but not the
   dashboard.
4. **Hotspots → Motorola, still blocked and must stay blocked.** Nothing has
   captured a master sending voice. Build 1 first, then use it: send a burst
   known to be well-formed and watch whether the repeater keys.
5. **Motorola → Motorola**, which needs no conversion at all and may be the
   simplest complete product. Needs KD9EJA's repeater on `allowed_peers`.

### Settled, do not reopen

Everything in §8b, §8c and §8d, plus:

- **The FEC is a wrapper, not a codec** ([ADR-0037](docs/adr/ADR-0037-dmr-fec-is-a-wrapper-not-a-codec.md)).
  Parameter bits are copied and never inspected; reading one needs a new ADR.
- **`0xf1` is not a peer list.** Tested: no peer ID in either byte order, no
  address, no port.
- **Byte 5 of a voice frame is a call counter, not a timeslot.** The timeslot is
  bit `0x20` of byte 17, and bit `0x40` marks the last frame.

### The method, now proved seven times in two days

**Every reading taken by eye was wrong. Every differential was right.**

The trailer, the master ID, the "timeslot" that was a call counter, a 49-bit
stride that is 50, the vocoder interleave, the EMB generator, the BPTC stride.
Each settled either by changing exactly one thing and diffing, or by running
candidate readings against thousands of real frames and taking the one that
scores near a hundred where the others score zero.

**A wrong hypothesis scores zero. That asymmetry is the evidence**, and it is
worth more than a citation.

### Two traps that cost real time

- **A count of failures hides new ones.** The container always fails
  `TestDocumentedPathsExist` for an unrelated reason, so a new failure kept the
  total at eight and was invisible. §7 already said list them by name; the rule
  was broken the same day it was written down. **Never count.**
- **The obvious deduplication is wrong.** The Homebrew captures hold every burst
  twice, once arriving and once relayed. Skipping *equal* neighbours collapses
  the two genuine continuation positions into one and yields a plausible
  sequence silently missing a burst. Take every second burst.

---

## 8f. Where the next session starts, as of 2026-09-02

> **SUPERSEDED — read [§8g](#8g-where-the-next-session-starts-as-of-2026-09-03) instead.** This section is kept for the reasoning in it, not for its
> state. **Its open list is wrong**, and wrong in the direction that wastes a
> session: it describes IPSC as having no voice and no listener, which was true
> when it was written and has not been since. A stale section that sits above
> the current one is read first by anybody going top-down.

Read §0, then §6b and §6c, then this. It supersedes §8e's ordering; everything
§8e settled remains settled.

**A Motorola repeater is audible on the network.** The converter is wired to
routing: `ipsclink` converts each voice frame and hands the burst to
`peers.Listener.DeliverFromIPSC`. Item 1 of §8e is done.

### Two defects, both found the same way

Neither came from the test suite, and neither was visible by reading.

**The converter had one set of counters for two timeslots.** A repeater carries
two transmissions at once. Interleaved, they reset each other every frame: 66
real frames produced 54 bursts on one slot and **18** across two, where 108 are
due. Found by a differential — the same frames with one bit flipped — because
asserting it from the code would have been another reading taken by eye.

**`routing.Core` was reached by two goroutines and had no lock**
([ADR-0038](docs/adr/ADR-0038-routing-core-is-shared.md)). **This shipped, on a
path an operator can configure.** An upstream link calls `DeliverFromUpstream`
from the link's own read goroutine while `Core` documented itself as owned by
the DMR socket's. It never fired because no upstream has met a real far end. The
race detector reported it on the first run of the first IPSC test, which is the
third real defect it has caught and the reason it is a blocking gate.

**The lesson worth keeping:** ADR-0002 claimed races were "structurally
impossible rather than merely tested against". Nothing enforced it. **A comment
claiming a concurrency invariant reads like a fact and is in truth a request.**
Where an invariant matters, enforce it in the type.

### Open, in order

1. **Confirm on air.** The XPR8300 keys, a hotspot hears it. Nothing else is
   evidence; §8a is emphatic that this project's defects are found by using the
   running system.
2. **The Link Control FEC, and it is probably why there is no audio.** Voice
   headers and terminators need Reed-Solomon (12,9) over the nine Link Control
   bytes — ETSI TS 102 361-1 Annex B.3.6, and the standard is a free download
   from ETSI, so this needs no reading of another implementation.

   **It is checkable without equipment.** Compute the parity over the Link
   Control of each of the 28 real data bursts in `testdata/hbp/` and compare
   with the parity they already carry. A wrong construction scores zero, which
   is the asymmetry this project runs on, and 28 of 28 would settle it.

   **Why it moved to the top.** TS 102 361-2 describes a receiver un-muting on
   an embedded Link Control PDU in the voice superframe carrying a matching
   address. §8e assumed late entry would cover a missing voice header; that
   assumption is a reading, it has never been tested, and readings have gone
   nought for nine. Bursts are reaching peers and no audio has been confirmed,
   so this is the first thing to build rather than a refinement to add later.
3. **The console page.** The IPSC listener holds peers and calls of its own
   that nothing reads, so a repeater shows in `/healthz` and the journal but not
   the dashboard. Traffic now reaches last heard; the peer list does not.
4. **The Pi-Star login drops every six or seven minutes** and re-authenticates,
   on the *local* path `192.168.1.155` to `192.168.1.247` that is supposed to
   bypass NAT rebinding. Between the failure and the next login a delivery to
   that peer goes nowhere. Unexplained, affects every member rather than IPSC,
   and deliberately not mixed into an IPSC diagnosis.
5. **Hotspots → Motorola, still blocked and must stay blocked.** Nothing has
   captured a master sending voice to a repeater.
6. **Motorola → Motorola.** Needs KD9EJA's repeater (315544) on `allowed_peers`,
   and is worth deferring until one repeater is proved audible: two peers in
   play means a fault has two possible sources.

### Settled by observation on 2026-09-02

- **A hotspot user heard a Motorola repeater.** KB9TYC heard KD9EJA's SLR5700
  over the bridge. **This is the first confirmation that the path produces
  audible audio** rather than well-formed bursts: header, vocoder payloads and
  terminator all reach a radio and open its squelch. Everything from
  `internal/dmrfec` upward is now proved on air.
- **A second repeater model works.** The SLR5700 registered and passed traffic
  with no change to the listener. Every byte in `internal/protocol/ipsc` came
  from one XPR8300 on one firmware, and ADR-0029 says so explicitly; a different
  model speaking the protocol the way QSP expects is new evidence, and it is the
  first of its kind.
- **The destination field is real.** One repeater, one source, three values:
  455, then 2, then 11. `Voice.Destination` is an observation, not a reading.
- **`slot_bit_is_timeslot2` is `true`.** With it set, a transmission keyed on
  TS2 is delivered on TS2, matching where the network already carries that
  talkgroup.
- **The XPR8300 is on colour code 11**, confirmed at the radio rather than taken
  from a fixture.
- **Terminators fixed the dropped over.** Before 0188, three key-ups seconds
  apart produced one set of relayed frames: destinations stayed reserved until a
  two-second timeout and the rest were refused. After it, three overs under a
  second apart all relayed, with no `call ended without a terminator` and no
  `released a destination held by an abandoned transmission`.

### A health status must name its subject and be able to recover

A count of refusals without the radio ID that caused them is not actionable, and
a lifetime total never falls, so it reads degraded until a restart. Both were
true of the IPSC check, and together they hid a member's repeater knocking every
ten seconds for hours behind the number 2144.

**The first diagnosis of that number was wrong.** It was read as internet
scanning against a port opened to the world hours earlier, and the proposed fix
was to stop degrading on refusals at all — which would have removed the only
thing that surfaced it. Every datagram was from one address, one radio ID, at
the documented unregistered-peer cadence. Look at what the system is reporting
before deciding the report is noise.

### An operational limit worth knowing

**A remote IPSC peer must be reprogrammed by hand whenever the master's WAN
address changes.** Motorola CPS takes a literal Master IP; there is no name to
point a repeater at. On a dynamic address this fails silently — the repeater
retries at a stranger and gives no indication why, exactly as one does when the
master ID collides with its own. Anyone running IPSC across the internet should
know this before they need to.

### Settled, do not reopen

Everything in §8b's list, plus:

- **Phase 2's gate is closed.** AD0MI joined unassisted from the join page and a
  password. Do not propose re-testing it; there is no second first-time member.
- **IPSC comes before P25.** Reach, not difficulty. See §2.
- **Audio is king**, and **talkgroup numbers are never renumbered**. §6c and §6b.
- **Talker Alias is pass-through.** Nothing injects bursts B–E.

### Open, in order

1. **IPSC. The captures exist; what is missing is voice and a listener.** On
   2026-09-01 two Motorola repeaters registered to each other over the internet
   with a capture host in the path — a K9MLS XPR8300 as master and a remote
   repeater as peer. Six fixtures in `testdata/ipsc/`, seven message types, and
   `internal/protocol/ipsc` parses the envelope they all share.

   **What is known:** byte 0 is the type, bytes 1–4 are the sender's own radio
   ID big-endian, confirmed across seven types and two repeater models.
   Registration is six packets, not two. An unregistered peer retries at ten
   seconds; a registered one keepalives at fifteen. A peer never sources from
   the master's port. ICMP unreachable is ignored, so a master cannot refuse
   anybody by staying silent.

   **What is not:** voice, private calls, text, disconnect. Nine of `0x91`'s
   sixteen bytes and thirty-nine of `0xf1`'s forty-four. The purposes of `0x85`,
   `0xf0` and `0xf1`. Whether a master behind NAT advertises an address a remote
   peer can reach — the link came up through NAT but nothing has been decoded
   that would say how.

   **Next:** a session with somebody at the far end who can key up. Voice on
   both timeslots, a private call, text, and a clean disconnect, in one capture,
   with the same bridge. A third repeater would make `0xf1` worth decoding,
   because one peer cannot distinguish a list from a fixed record.

   **[ADR-0029](docs/adr/ADR-0029-ipsc-from-capture.md) still holds** for
   everything not captured: no constant, no message type, no parser for anything
   the fixtures do not contain. DMRlink and HBlink3 remain unread and must stay
   that way — reading them binds the project to a derivative work permanently.

2. **The peering retry between two machines.** The console flow was built and no
   button has been pressed. The loopback pair proved the protocol and proved
   nothing about NAT, a public address, or a far end that restarts. The one real
   attempt took production down for twenty minutes, which is the argument for
   `qsp -config <file> -check` having been built first.

3. **Subscription on air.** Built, never run with a radio. Turning it on
   silences everybody who has not yet transmitted, so static attachments have to
   be configured first — and those are still not editable from the console. That
   ordering is the whole risk.

4. **P25**, once DMR is finished. `testdata/p25/CAPTURE-REQUEST.md` is written;
   the idle capture exists and contains no voice.

### Unexplained. Do not theorise without a capture

- **An echo.** K9MLS heard about a second of his own audio return after
  unkeying, once. QSP is provably not looping: every `relaying transmission`
  line excludes the originating peer, and the third station is 1,300 miles away.
- **A repeated stream ID.** One peer produced the same 32-bit stream ID twice,
  34 seconds apart, in the same QSO. Roughly one in four billion by chance. An
  earlier session saw six `call started` events in three seconds from one
  operator, with six distinct IDs.

Both are recorded rather than investigated because there is no capture of
either, and §7 says a theory that has failed twice does not get a third guess.

### Costs to respect

CI runs on `workflow_dispatch`, weekly on Monday, and on a `v*` tag — **not on
push**. One job, about six minutes. `gh workflow run CI`.

**A Motorola repeater has two port fields and they are not the same thing.**
`Master UDP Port` is the master it dials; `UDP Port` is the port it binds. Set
to 50000 and 50001, a master that looked correctly configured served a port
nobody was calling, sent no UDP at all for fifteen minutes, and answered every
request with an ICMP unreachable from its own IP stack. Two plausible theories
came before the right one and both were wrong; what ended it was `nmap -sU`
against the repeater, which found exactly one open port. **When a device's own
stack sends the refusal, ask the device what it bound rather than re-reading the
configuration page.**

`cmd/qsp` has known failures in the development container because no SQLite
driver is registered there. **List them by name rather than counting them** —
two new failures once hid inside a count that looked normal.

---

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

## 8g. Where the next session starts, as of 2026-09-03

> **SUPERSEDED — read [§8i](#8i-where-the-next-session-starts-as-of-2026-09-04)
> instead.** Kept for the reasoning. **Its open list is wrong**: it says text
> messages work, and repeater-to-hotspot text does not.


Read §0, then §6b and §6c, then this. It supersedes §8f entirely; everything
§8f settled remains settled except where named below.

**A Pi-Star and a Motorola repeater held a conversation.** The bridge carries
audio in both directions, on air, confirmed by the operator: K9MLS on a hotspot
and KD9EJA's repeater over IPSC. Layer 1 is complete for both protocols.

### The defect, and why nothing caught it

**QSP was sending a shape no repeater has ever sent**
([ADR-0042](docs/adr/ADR-0042-the-outbound-frame-shape-is-measured.md)):
33-byte headers where a repeater sends 54, and 66 bytes for every voice frame
where a repeater cycles 52 57 57 57 66 57. 494 frames reached a member's
repeater on 2026-09-02 and were ignored.

**None of ADR-0041's four assumptions could have been the cause.** The output
was not the shape of an IPSC frame before the protocol question arose, so the
list of things to check was a list about the wrong layer. A hypothesis about a
protocol cannot be tested by a program that is not speaking it.

All of it was measurable against fixtures already in the repository — 326 voice
frames and 93 headers and terminators, two repeater models, no equipment and no
capture session. The transmit path had existed for two patches with a green
suite and no assertion that its output resembled anything.

### A master sending voice was captured, and QSP matches it

`testdata/ipsc/ipsc-master-voice.pcap` is the first capture in this project of a
master talking to a repeater. Every other IPSC fixture is the other direction.
It was taken with `cmd/ipsc-peer` against the XPR8300 in master role on
2026-09-03, three transmissions on TG 2 TS2.

**It confirms the shape ADR-0042 derived from peer captures alone**: 54-byte
headers and terminators, voice frames of 52, 57 or 66, byte 31 equal to
`len - 32` across 276 voice frames with no violations, and the order
`HHH (AfffLf)* T` in all three transmissions.

**22 of the 24 bytes from byte 30 onward are what QSP already builds**, including
the Reed-Solomon parity `90 b2 a0` — computed from the Link Control rather than
copied, and present in no earlier capture. Only bytes 52 and 53 differ, and QSP
writes zeros there deliberately.

Byte 52 is constant within a session and varies between them; byte 53 changes
frame to frame. No checksum over any range reproduces either. One stable byte and
one wandering byte, per device and per session, unrelated to the frame's
contents, is the shape of a measurement rather than derived data. **That is a
reading of the numbers, not a decoding.**

### Two things learned at the bench that are easy to lose

- **A master with no peers registered sends nothing at all.** Four minutes of
  capture on a live link produced not one datagram from it. There is no
  announcement behaviour QSP is missing.
- **IPSC has a second refusal vocabulary.** §7 says ICMP unreachable is ignored
  and silence is the only way it says no. A master whose port is not bound
  answers with **ICMP port-unreachable**, one per registration attempt. The peer
  ignores it and retries, which is correct — but the ICMP is there, and a
  `tcpdump` filter containing the word `udp` hides it. **An hour went into
  theorising about protocol bytes while the answer was in the packets the filter
  had dropped.** Three separate times this session a filter hid the answer.

### Parrot runs for Motorola repeaters

It did not, and the reason recorded in `DeliverFromIPSC` was that there was no
path back to an IPSC peer. **That path was built in 0195 and a repeater keyed on
it the same evening.** The comment outlived the fact and was switching off a
feature — which is what §7 means by a stale comment being a defect.

The recorder is reused unchanged. The replay timing moved into `parrot.Player`
so the sixty-millisecond interval exists in one place; `internal/peers` keeps its
own sink and all ten of its playback tests.

**The IPSC listener has its own `parrot.Recorder`**, not the DMR listener's. Both
key by radio ID and the protocols share the DMR ID space — this network had
3132910 registered on both listeners at once — so a shared recorder would merge
two operators' recordings and replay one into the other's radio, silently.

**`dmr.parrot.timeslot` and `ipsc.slot_bit_is_timeslot2` interact and nothing
relates them.** They live in different configuration sections, and a parrot on
timeslot 2 never claims frames from a repeater whose slot bit converts to
timeslot 1. A test found this by failing.

**It works on air**, confirmed 2026-09-03 on the operator's XPR8300.

That proves more than parrot. The replay went out through `SendVoiceTo`, a path
nothing else uses — send to one repeater rather than to every repeater but the
origin — and it was paced by `parrot.Player`, the timing loop extracted from
`internal/peers` in 0201. Both were written, tested against fixtures, and never
run until then. Frames built by the new encoder, arriving sixty milliseconds
apart, and a radio unmuted them.

**The talkgroup must be a group contact in the repeater's codeplug**, on the
timeslot the frames convert to. QSP cannot check either, and a missing codeplug
entry looks exactly like a QSP fault from the operator's chair.

### Settled on air, 2026-09-02 evening

- **Header bytes 52 and 53 do not matter to a receiving repeater.** They were
  the fifth and weakest ADR-0041 assumption: 87 distinct values across 93
  captured frames, no CRC or checksum fits, and byte 52 drifts like a
  measurement taken at the sending repeater. QSP writes zero and KD9EJA's
  repeater keyed anyway. **This is the only one of the five settled by
  observation rather than by reasoning**, and it is settled in the direction
  that costs nothing.
- **A capture of a master sending voice is no longer blocking.** It was the one
  thing standing between two Motorola operators hearing each other, and the
  inference under ADR-0041 turned out to be right where it mattered. It is
  still the thing that would confirm the envelope rather than infer it, and it
  is now a nice-to-have rather than a gate.

### Settled long ago, and looked up rather than re-decided

**Parrot is talkgroup 9990, group call, timeslot 2, in production since
2026-08-31** and used by hotspot members. `dmr.parrot.talkgroup` is one setting
and both listeners read it; IPSC does not get its own number.

9990 was chosen because **most radios already have it programmed**. It must be
a *group* contact, not a private one — [ADR-0028](docs/adr/ADR-0028-parrot.md)
records why, and the reason is structural rather than conventional: a DMR voice
header carries its addressing inside the burst, in the Link Control, under its
own error correction, and a radio believes that rather than the wrapper. An
attempt to answer a private call by swapping source and target in the wrapper
sent five replays at correct timing that the radio muted. Rewriting the Link
Control means decoding and re-encoding a burst, which is what QSP does not do
and what lets parrot exist without a vocoder.

**This was looked up on the internet in a later session and nearly changed.** A
session researched BrandMeister's convention, recommended moving to 9998, and
was corrected by the operator — the answer was in ADR-0028 and in the changelog
entry for 2026-08-31 the whole time, and the reasoning in the repository was
better than the reasoning found outside it. **Search the repository before
searching the web.** A settled decision that has been on air for weeks does not
need a second opinion from a forum.

### Carried forward from §8f, still true

- **`slot_bit_is_timeslot2` is `true`**, settled from the journal, which logs
  the raw bit beside the slot it was read as. A transmission keyed on TS2 is
  delivered on TS2, matching where the network already carries that talkgroup.

  The encoder used to hardcode this polarity while `Converter` read the
  setting. That was **latent rather than live**: the hardcoded value happened
  to equal the configured one, so it cost no audio and would have bitten the
  first operator who set it `false`. Both now read the setting.
- **The destination field has moved**: 455, then 2, then 11. `Voice.Destination`
  is an observation rather than a reading, and bytes 9 to 11 are named.
- **Parrot and unlink do not run on the IPSC path.** Both answer a member by
  sending audio back. Consuming a frame and delivering nothing is worse than
  not running: the transmission vanishes while the journal says it was handled.
  Triggers do run.
- **The one-way rule is structural.** Destinations resolve through the DMR
  listener's peer table and an IPSC repeater is never in it, so a bridge naming
  one still delivers nothing and a test asserts exactly that. Audio reaches a
  repeater out of the IPSC listener's own socket, which is a different path and
  a deliberate one.

### Superseded by ADR-0042

- **"The colour code has no default"** is no longer the whole story.
  `ipsc.colour_code` is still required and still refuses to default, because a
  wrong colour code presents as silence rather than as a misconfiguration. But
  it is now only the fallback: each peer's own colour code is learned from the
  frames it sends and mirrored back to it. A network carrying colour codes 1
  and 4 at once — which `ipsc-two-peers.pcap` shows is a real case — cannot be
  served by one number.

### Settled by decision, 2026-09-02

- **QSP is the master and a club runs no second one**
  ([ADR-0043](docs/adr/ADR-0043-qsp-is-the-master.md)). Every Pi-Star and every
  Motorola repeater points at QSP; no Motorola master repeater alongside, and
  nothing commercial. **QSP is never an IPSC peer in production**, which removes
  half the
  protocol from the project's obligations permanently.

  It is replace, not augment: a club whose existing IPSC master they cannot
  reconfigure cannot adopt QSP incrementally. Accepted deliberately.

  A bench instrument may play a peer in order to observe a real master. That is
  a diagnostic in the same category as `cmd/ipsc-probe`, never ships in
  `cmd/qsp`, and is not a route back to peer support.

  **The limit worth remembering:** QSP has authority over delivery, not over
  transmission. A repeater receives everything and filters by its own codeplug,
  which QSP cannot learn and must not guess at.

### Closed since §8f was written

- **Motorola repeaters appear on the dashboard** (0197), labelled by protocol,
  with the cells IPSC cannot fill saying why rather than showing a dash.
- **The learned colour code is confirmed on air.** The operator's XPR8300 is on
  colour code 11 and KD9EJA's is not, and both worked at once. A single global
  `ipsc.colour_code` could not have served both, so ADR-0042's mirroring is
  proved rather than coincidental.
- **The Traffic panel no longer lies** (0203). Its voice frame count came from
  the DMR listener alone, so a network carrying only Motorola audio showed zero
  and the hint beneath advised checking a hotspot that was not involved.
- **`VERSION` is read by something** (0203). It had been bumped in three
  consecutive patches while `qsp --version` reported a pseudo-version.

### IPSC layer 1 is finished

Said without hedging, 2026-09-03. Audio both directions; the frame shape
verified byte for byte against a real master; colour code mirroring proved on
air with two different codes at once; access control on all three checks;
repeaters manageable from the console without a restart; parrot working.

What remains is listed below and none of it blocks the network.

### Text messages are decoded, and no code is written for them yet

[ADR-0045](docs/adr/ADR-0045-ipsc-text-messages.md), from
`testdata/ipsc/ipsc-text.pcap`. **`0x83` is a group text, `0x84` a private one**,
carried as real DMR data bursts — CSBK, Data Header, Rate 1/2, Rate 3/4 — inside
the same envelope as voice, constants block and all. Byte 30 is the DMR data
type and agrees with the low nibble of byte 51 in 153 of 162 frames.

**There is no acknowledgement to send, and the reading that said there was got
it wrong twice.** The repeats are the operator pressing send, not a radio
retrying — four to five seconds is how fast a person works a keypad. And the
radio reported success while QSP dropped every burst, so the recipient received
nothing: the acknowledgement came from the repeater on RF, one hop away, and the
master is not part of it.

**A repeating pattern in a capture looks identical whether a machine or a person
made it.** Two questions to the operator settled in seconds what no amount of
reading timestamps could, and they should have come before the record was
written rather than after.

**Outbound text fails silently today.** A text handed to `SendVoice` reaches
`Encode`, which looks for a vocoder core, finds none, and returns nil — no
frames, no error, no log line. That is the failure §7 exists to prevent and it
has been in the code since the transmit path was written.

### Open, in order

1. **Text messages work in both directions and have never run on air.** Built
   under ADR-0045 and asserted end to end against `ipsc-text.pcap`, including
   the full bridge round trip. The outbound shape is inferred exactly as voice
   was under ADR-0041; a capture of a real master relaying a text would confirm
   it. Rate 3/4 bursts are refused rather than truncated, so a long text may
   still arrive incomplete — that is the next thing to watch for on air.
2. **`/api/peers` is unauthenticated and now carries repeater radio IDs and
   addresses.** Settled as unauthenticated long ago, but 0197 enlarged what it
   exposes. That was named as a decision to make rather than a defect, and it
   is the last piece of the access story still open.
3. **Private calls from Paul to Mike**: QSP delivers them and MMDVM never logs
   them arriving. Still unexplained, and it blocks a private parrot.
4. **The echo and the repeated stream ID.** Still no captures. The duplicate
   radio ID found on the dashboard is the best candidate yet for the echo:
   `SendVoice` skips the origin, so two peers sharing an ID means one of them
   is excluded from every delivery.
5. **Subscription on air**, then **P25**, unchanged. P25 is weeks away at the
   earliest and the operator wants DMR and IPSC finished first.

### The table in §0 was stale, and §0 is what every session reads first

On 2026-09-03 a session spent several turns calling access control "the oldest
missing thing" because §0's table said `missing — next`. **Line 764 of this same
file said it was built, and the code agreed with line 764.** All four lists were
parsed, wired, and carrying traffic.

The table is corrected. The lesson is not "check the table" but **the section
with the most authority is the one most worth doubting**, because everyone reads
it and nobody re-reads it. Two of the day's worst turns came from trusting a
summary: this one, and recommending a change to the parrot talkgroup that
ADR-0028 had settled weeks earlier.

**Search the code for the thing, not the file for a claim about it.** The
diagnostic that works is §8a's — *what is declared and read by nothing* — run
against the feature you are about to build, before building it.

### The list-by-name rule was broken again, in a new disguise

§7 says list CI failures by name and never count them. On 2026-09-03 a session
obeyed the letter and defeated the purpose: it ran

    go test ./... 2>&1 | grep -cE '^--- FAIL' >/dev/null

before committing, which counts the failures **and discards the count**. A
documentation-accuracy failure it had just introduced went into a delivered
patch. The next command in the same session, comparing failures by name against
the baseline, caught it immediately.

**The rule is not "do not count", it is "read the names".** A pipeline that ends
in `/dev/null`, `wc -l`, `-c`, or `| tail -1` is the same defect wearing
different clothes. The only safe form is the one that diffs the sorted names
against the recorded baseline and prints the difference.

### staticcheck is the gate that cannot be run in the container, and it matters

The development container has no `staticcheck` and cannot get one: the module
proxy is outside its allowlist. Patches 0195 through 0204 were all delivered
with that gate unverified.

On 2026-09-03 it caught a real one. A doc comment reading
`// go:embed cannot reach outside its own directory` is a **malformed compiler
directive** to staticcheck (SA9009): a real directive has no space after the
slashes, so a sentence that begins with `go:` at the start of a comment line
looks like a typo for one. `gofmt` and `go vet` both pass it.

**The damage was not the lint.** The operator's gate chain is
`gofmt && go vet && staticcheck && go test && go test -race`, so a staticcheck
failure stops the chain and **the test suite never runs at all**. The build and
scp on the following lines are not part of the chain and ran anyway, putting an
untested binary on the server.

Two rules follow. **Never begin a comment line with a word that a compiler
directive could start with** — `go:`, `line:`, `export:`, `extern:` — and if the
prose needs one, put it mid-sentence or in backticks. And **a patch delivered
from the container has one unverified gate**, so the failure mode to expect is a
chain that stops before the tests rather than a test that fails.

### The method, now proved ten times

**Every reading taken by eye was wrong. Every differential was right.**

The tenth: `body[20]` was recorded as a frame marker reading `0x67` on headers
and `0x07`/`0xe7`/`0x87` on the three voice shapes. It is the low byte of the
32-bit timestamp. The timestamp advances by exactly 480 per frame, so its low
byte falls by `0x20` each time, and across the first superframe after the
headers it really does read those four values against those shapes. Across the
next one it does not. **A field was invented out of a sampling window one
superframe wide.**

### What this file cost, and the rule that follows

**This file held six copies of §8f, in five different versions, and two of
§8d.** 1,174 lines of 2,726 were stale duplicates. A session read the first
copy, treated it as current, and drew two wrong conclusions from it: that the
destination field had never moved, and that the slot polarity might be a live
defect. Both were already correctly recorded — in a copy further down the file.

**A file that calls itself the single source of truth and contains five
versions of the same section is worse than one that admits it is a log.** The
duplicates were collapsed by content hash, keeping the most complete version of
each and folding every unique fact from the others into this section; nothing
was deleted by line number.

---

## 8h. IPSC repeaters on the dashboard, and what it would take

> **BUILT in 0197.** Kept for the reasoning about labelling and about what an
> IPSC peer cannot announce, which still governs. The work itself is done.

Asked by the operator on 2026-09-02: can IPSC peers appear in Connected Peers?

**Yes, and it is wiring rather than discovery.** The listener already holds
everything the panel renders. `ipsclink.Listener.Peers()` returns each
repeater's radio ID, address, registration time, last-heard time, keepalive and
voice-frame counts, and its most recent call — and since ADR-0042, its colour
code, learned from the frames it sends.

### Why it does not appear today

`server.PeerSource` is one interface with three methods — `PeerViews`,
`CallViews`, `Traffic` — and `cmd/qsp/app.go` passes exactly one
implementation, the Homebrew master. There is no second source and no
composition. The IPSC listener satisfies none of the interface, not because its
data is missing but because nobody wrote the three methods.

This is the ninth-and-tenth pattern in a different dress: **the data is
declared, populated, and read by nothing.**

### The shape of the work

Two honest options, and the choice matters more than it looks.

**Compose two sources.** `PeerSource` becomes a slice, and `handlePeers`
concatenates. Cheapest, and it puts Homebrew hotspots and Motorola repeaters in
one list — which is what an operator running one network wants to see.

**Distinguish them in the view.** A `PeerView` gains a protocol field, and the
panel groups or labels. More work, and probably right: the two are not the same
kind of thing and an operator troubleshooting needs to know which. A Homebrew
peer announces a callsign, a location and its talkgroups; an IPSC peer
announces none of them, so half the columns are empty for a repeater and the
emptiness means "this protocol does not carry that", not "this station did not
say".

**The second is recommended.** Silently showing a repeater with a blank
callsign and no talkgroups invites the reading that it is misconfigured, and
§7's rule against fake anything covers a blank that means two different things.

### What must not be lost

- **`/api/peers` is unauthenticated** and that is settled (§8f, and the
  middleware lists it beside `/api/join` and `/healthz`). Adding repeaters adds
  radio IDs and addresses to a public endpoint. An IPSC peer's address is
  already public in the sense that it is dialling a public port, but this
  should be a deliberate decision rather than a side effect, and it may be the
  argument for putting repeaters behind the session instead.
- **A repeater announces no talkgroups**, so the panel must not imply it has
  none. It filters by its own codeplug and QSP cannot see that.
- **Colour code is now per peer and learned.** Showing it is genuinely useful:
  it is the fastest way for an operator to see whether the mirroring in
  ADR-0042 is working, and a repeater with no colour code shown is one that has
  never transmitted.

---

## 8i. Where the next session starts, as of 2026-09-04

Read §0, then §6b and §6c, then this. It supersedes §8g; everything §8g settled
remains settled except where named.

**Everything on this list was found by running the system.** Not by a test, not
by a bug hunt, and in two cases not by reading the code at all.

### The defect that mattered: one timeslot never worked

**Byte 30 of an IPSC voice frame carries the timeslot in its high bit.** A voice
frame on the slot whose bit is set reads `0x8a`; one on the other slot reads
`0x0a`. `FrameVoice` was recorded as `0x8a`, from captures that were all on one
timeslot, so `Payload` refused every frame on the other and the converter
produced nothing for it.

**One whole timeslot of audio never crossed the bridge**, from the day the IPSC
listener was written until 2026-09-04. It hid because this network carries its
traffic on TG 2 timeslot 2.

The bit agrees with the slot bit in byte 17 on **all 528 captured voice frames**,
across four captures and two repeater models, and is never set on a header or a
terminator — which is why signalling worked and the fault presented as a
talkgroup problem rather than a timeslot one.

**102 of the refused frames were sitting in `ipsc-slot-tg.pcap`** — a fixture
captured *for the slot bit* — the whole time. Three bug hunts and a green suite
walked past them.

### How it was found, which is the transferable part

The operator keyed up on talkgroup 11, timeslot 1, and read the journal:

```
subsystem:"ipsc"     destination:11 timeslot:1 slot_bit:false   ← logged
subsystem:"network"                                             ← absent
```

**The gap between those two lines was the entire diagnosis.** `AsVoice` reads the
flags and succeeds; `Payload` reads the marker and refuses. So the IPSC listener
logged a call and the DMR side never saw one.

Nothing else found it. Not the test suite, not three rounds of code reading, not
four captures. **Log the same fact at two layers and the gap between them is a
diagnostic** — that is why the raw slot bit is logged beside the slot it was read
as, and it earned its place.

### And then the second half was a codeplug

With the defect fixed, TG 11 still did not reach the operator. The Last-heard
panel showed why in four rows:

    KD9EJA  3155373  TG 11  TS1
    KD9EJA  3155373  TG 11  TS1
    K9MLS  3132910  TG 11  TS2   ← the operator

**A talkgroup on a different timeslot is a different destination.** His radio was
programmed for TS2 and everyone else was on TS1. Reprogrammed, it worked
immediately.

**When two stations cannot hear each other, read Last-heard before anything
else** and check they are on the same slot. Four rows answered what an evening of
captures had not, because a talkgroup appearing on two timeslots is invisible in
a log and unmissable in a table.

### Text messages: built, half-proved

[ADR-0045](docs/adr/ADR-0045-ipsc-text-messages.md). `0x83` is a group text and
`0x84` a private one, carried as DMR data bursts — CSBK, data header, Rate 1/2,
Rate 3/4 — inside the same envelope as voice.

- **Hotspot → repeater works on air.**
- **Repeater → hotspot does not, and QSP is not the reason.** Verified from a
  capture: routing refused nothing, 46 of 46 bursts were converted and
  delivered, every DMRD field is correct, the bursts are valid BPTC with the
  right colour code and slot type, and sequences run 0–22. **The remaining hops
  are MMDVMHost and the radio**, and the Pi-Star's own log at
  `/var/log/pi-star/MMDVM-*.log` is the place to look.

**A real possibility nobody has ruled out:** Motorola's text is TMS carried as an
IPv4 UDP datagram, whose source address is `0x0c` followed by the sender's
24-bit radio ID. Whether a non-Motorola radio displays that at all is unknown,
and if it does not, no change to QSP fixes it.

**Rate 3/4 bursts are refused rather than truncated**, deliberately: they carry
22 octets where a burst holds 12, and half a message delivered looks like it
worked. None appeared in the captured texts, so this has never yet mattered.

### Open, in order

1. **Confirm text on air both ways**, or establish that Motorola TMS cannot
   reach an MMDVM radio. The Pi-Star's MMDVM log settles it.
2. **`/api/peers` is unauthenticated** and carries repeater radio IDs and
   addresses since 0197. A decision to make, not a defect.
3. **Private calls from Paul**: delivered by QSP, never logged arriving by
   MMDVM. Still unexplained, and it blocks a private parrot.
4. **The echo and the repeated stream ID.** The duplicate radio ID that was the
   best candidate is fixed, so this may already be gone; watch for it.
5. **The vocoder**, which is a decision and not work in progress. BLUEPRINT §7
   settles the shape — QSP ships no codec and orchestrates the operator's
   hardware. The operator is buying a **DVSI USB-3003-P25**: the standard
   AMBE-3000 in a ThumbDV or DVstick 30 does **not** do P25 full rate, only the
   P25 variants do, and that would have been discovered months later.
6. **Subscription on air**, then **P25**.

### What changed on the console

Four metrics on Traffic — datagrams in, voice frames, collisions, ignored —
summed across both listeners in the console rather than the payload, so the API
keeps every counter apart. Motorola repeaters appear in Connected peers with a
Link column and a looked-up callsign marked as a lookup. **A routing refusal is
logged at info now, once per destination and reason**, because it was at debug
and production runs at info: the reason was counted and never explainable, which
is what cost the evening.

---

## 8j. Where the next session starts, as of 2026-09-05

**Superseded by §8k.** Its account of the Rate 3/4 block layout is backwards
and its open list is stale; everything else it settled still stands.

Read §0, then §6b and §6c, then this. It supersedes §8i; everything §8i settled
remains settled except where named. **§8a is the section that matters most** and
this day did nothing but confirm it again.

### The whole day, in one sentence

**A message type QSP had never accepted, and a call record that never ended.**
Both were found by reading a journal and a dashboard on a running system, and
neither could have been found any other way, because the code was doing exactly
what it was written to do in both cases.

### `0x81` is a private voice call, and it had never worked

[ADR-0046](docs/adr/ADR-0046-ipsc-private-calls.md).
`testdata/ipsc/ipsc-private-voice.pcap`.

QSP had refused the leading byte `0x81` since the IPSC listener was written. It
arrived from both repeaters, in runs as long as somebody holds a key down, and
every datagram was counted as unparsed and thrown away. **No private call from a
Motorola repeater had ever crossed the bridge.** It hid the way the timeslot
defect hid: this network's traffic is group calls on TG 2, and a member who
tries a private call and hears nothing assumes the other station is not there.

**It is `0x80` with a radio ID where the talkgroup goes.** Diffing a private
header against a group header from the same repeater fourteen seconds apart, the
envelope differs at thirteen of its first thirty-eight bytes: the leading byte,
the call counter, the three destination bytes, the stream ID, and six bytes that
vary frame to frame within one transmission anyway. **The same diff on the other
repeater model differs at exactly the same offsets.**

Two encodings of the call type agree. Byte 38 of a header or terminator is the
DMR Full Link Control opcode — `0x00` Grp_V_Ch_Usr against `0x03` UU_V_Ch_Usr —
matching the leading byte on **all 32 header and terminator frames** in the
capture. The destination is carried twice, in the envelope and in the Link
Control, and agrees on all 32.

**The experiment is worth copying.** Two private calls in opposite directions
between the same two radios, with group calls either side. Source and
destination move in opposite directions between them, so neither field can be
confused for the other, for a constant, or for the envelope's sender ID. One
capture, two fields settled, and it took the operator four minutes to key.

`Voice.Destination` stops being marked unverified after three weeks. It carried
that warning because no capture had ever moved it; this one moves it three ways
in two minutes.

### A transmission that never ended was reported as running for seven hours

The console showed a live transmission of **7h14m18s** in 45 frames, with a
station counted as transmitting, while the radios were silent.

An IPSC call ended on its last-frame flag and on nothing else. There was no
timeout anywhere, and a peer that keeps keepaliving is never dropped, so a
transmission whose terminator never arrived stayed open for as long as the
repeater stayed up. `CallViews` then rendered it as *now minus started*,
unbounded.

**Three true statements that lie together**: peers expire on silence, calls end
on a terminator, and a peer that keeps keepaliving keeps a call whose terminator
never came. Every one of those is correct alone. This is the §8a shape and it is
worth recognising on sight.

The fix is a silence timeout at `calls.StreamTimeout`, imported rather than
restated so the two listeners cannot drift, plus a ceiling of four minutes.
**Every radio on an amateur network has a time-out timer**, 180 seconds on this
one, so frames still arriving after four minutes are a stuck record rather than
a long over. The ceiling is keyed on the start time where the timeout is keyed
on the last frame, so the two cannot fail together, and it is meant to be dead
weight. If it ever fires, that is a defect report.

A superseded stream was also being discarded silently: a new stream ID replaced
the record outright, so a call whose terminator never arrived left a `call
started` with no `call ended` anywhere and vanished with nothing said about it.

### Counting the same transmission at three layers

The console reported an IPSC transmission of 45 frames while the DMR side
recorded 22 for the same stream in the same second, and **neither number said
where the other 23 went**.

Conversion is ruled out by measurement rather than by argument: every
transmission in `ipsc-private-voice.pcap` through the converter gives 136 in for
134 out, 148 for 146, 88 for 86 — the arithmetic of three repeat headers
becoming one and a terminator being added. Joining part-way through costs at most
one superframe, by design.

So a call now carries **frames received, bursts converted, bursts delivered**,
and the end-of-call line reports all three. On air it reads
`"frames":28 "converted":26 "delivered":26`, which is the healthy shape. **The
gap itself is still unexplained and is now instrumented rather than argued
about.**

The end of a transmission is logged after delivery rather than at the frame
carrying the terminator, because delivery happens outside the peer lock and a
line written earlier would under-report every call by exactly the last
datagram's worth.

### Two warnings that had stopped meaning anything

**Seventeen `call started` lines for one press of one button.** Each data burst
carries its own stream ID, so each is its own call. The history had merged runs
of bursts into one entry since the text work; the journal had not, so the
console and the journal disagreed about how many things had happened, from the
same process, about the same second. `DataBurstWindow` is now exported so the
two cannot drift.

**Four `abandoned transmission` warnings per text message.** A text reserves its
destinations and then ends without a terminator, because data has no terminator
and is not meant to. The call tracker had learned this; the routing reaper had
not, because they are separate mechanisms and only one had been read carefully.
That warning is also how a peer that lost power mid-over is noticed, and one an
operator has learned to scroll past does not do that job.

**A voice header is a data frame type**, so it takes the reservation one frame
before the audio arrives. Classifying on that alone would have filed every real
transmission as data and silenced the warning permanently — the one way that
patch could have hurt audio, and the reason the flag is corrected upwards.

### The first UI review this console has had

An operator looked at the sidebar and said the section headings looked like
links. **The stylesheet agreed with him**: `.nav__heading` and
`.nav__link[aria-disabled="true"]` were both `--color-foreground-subtle`, at the
same left inset, in the same column. Size, tracking and uppercase were carrying
the whole distinction, and none of them wins against colour.

That prompted the review the console had never had — and prompted a question
worth recording: `/mnt/skills/user/ui-ux-pro-max` had been installed for this
project and never once read. **A skill nobody opens is the same defect as a
symbol nobody calls**, which this file has documented nine times about Go and
had not noticed about itself.

**Three of my own quick readings during that review were wrong**, which is the
same lesson as §8a in a different medium:

- I said `tokens.css` admitted a ratio under the 4.5:1 floor. It records a
  *rejected* option and why it was rejected. Measured, every pair clears the
  floor.
- A regex reported 33 unlabelled form fields across six pages. It could not
  handle a tag spanning two lines. The real number was two.
- I flagged the join page as having no focus rings. It loads `console.css`
  first and inherits all twelve.

The real findings were four, not the many I predicted. **The console was in
better shape than either of us assumed**, and the way to find out was to measure
rather than to look.

### What changed, and the two decisions behind it

**Planned links moved to `--color-unavailable`**, the token that already means
"not built" and sits beside the phase badge saying which phase brings it, so a
heading no longer shares a colour with anything clickable. Headings also gained
space above and lost it below: proximity is the strongest grouping signal there
is, and a heading floating equidistant between two groups introduces neither.

**Two fields on the links page were captioned with a `<p>`** carrying the label
class — right size, right colour, right position, no association with the
control. A `<label>` is inline where the paragraph was block, so the fix needed
a display rule or it would have slid both captions onto the same line as their
controls: a correct accessibility change that looks like a layout bug.

**The administration group ships hidden and `nav.js` reveals it.** It used to be
listed to everybody with a note saying to sign in, on the grounds that hiding a
page makes the console lie about what exists. The operator overruled that, and
the implementation matters: revealing on a confirmed session means the default
is the state a visitor should see, there is no flash of admin links on every
load, and a fetch that never returns leaves a signed-out console rather than a
signed-in-looking one. One line stands where the group was, because a console
with no visible way to administer anything reads as a console that cannot.

**`ipsc.peer_names` gives a Motorola repeater the callsign it never announces.**
A repeater on a private radio ID — 999999 and 999998 here — is in no registry
and never will be, so the console showed a bare number for precisely the peers
carrying the network. Two decisions inside it:

- **It is a separate field from `allowed_peers`**, because the allow list
  decides who is answered and a name decides only what somebody reads. Folding
  a label into the admission list would put display text on the path that
  decides whether a datagram is processed. The console still presents one
  field — a line reads `315544 KD9EJA` and the page splits it — because two
  lists kept in step by hand is how one goes stale.
- **`PeerView.CallsignLookedUp` became `CallsignSource`**, because there are
  three claims and a bool holds two: announced by the peer, looked up in a
  public registry that may describe whoever registered the ID years ago, or
  written down by the operator who owns the repeater. The operator's label
  outranks the registry. All three render differently, by underline style
  rather than colour, so the distinction survives a screenshot and a
  colour-blind reader.

A name for a repeater not in `allowed_peers` is refused at validation. It would
otherwise be invisible — never shown, never explained — which is the
declared-and-read-by-nothing pattern again. An empty allow list admits
everybody, so nothing is orphaned that way.

### What the review did not cover

**Two components out of eleven pages.** The sidebar and one access panel were
read closely; the peers table, traffic, health, history, bridges, links, the
join flow and the maps were looked at and not measured. "Fewer findings than
expected" is not "none", and the pages an operator uses most have not had this
treatment.

The nav markup is **copied into seven pages**. Nothing shares it, so every rule
about it is seven rules, which is why the tests for it count the copies.

### The evening: six defects, none of them audible

All six were found by reading captures byte by byte, after a regression test that
sounded perfect. **The audio was fine every time.** None of these would have been
found by listening, and none by reading the code — three of them were in files
open on the screen at the time.

**A relayed transmission carried the sender's radio ID where its stream ID
belongs.** `streamFor` packs the IPSC stream into the top of a Homebrew stream
ID and the radio ID into the bottom; the encoder wrote the low half. Two overs
nineteen seconds apart, on different talkgroups and timeslots, both went out as
`0x0cdee`. A receiver tells one transmission from the next by that field.

**Every voice transmission was announced as a group text.** A voice Link Control
header travels in a data-sync burst, the same frame type a text uses, so
dispatching on frame type alone sent MMDVMHost's header to the text encoder and
it left as `0x83`. Three captures of real Motorola masters hold 288, 66 and 326
voice datagrams and every one is `0x80`; not one `0x83` appears in any of them.

**A private call between two Motorola repeaters was blocked by a Homebrew
verdict.** Routing resolves a private call by locating the radio among the
Homebrew peers; a radio behind an IPSC repeater is not there, so a reason was
set, and `sendToIPSC` bailed on any reason at all — a path that never needed the
lookup, because a repeater receives everything and filters in its codeplug. The
call in the other direction worked because that radio happened to sit on a
hotspot, so the defect hid behind where two operators keep their radios.

**`Result.Reason` was never logged.** Read in exactly one place to make a
decision and printed nowhere, so a transmission carried nowhere produced a `call
started` line and silence. That is the trap the drop reasons fell into, in the
same function, fixed for those and left here.

**`SendVoice` reported nothing, ever.** The Homebrew side logs a line per
destination; the repeaters got their traffic in silence, so "KD9EJA did not
receive my text" could not be answered without a packet capture.

**One text went out as eighteen transmissions.** MMDVMHost gives every data
burst its own stream ID and the encoder kept its per-transmission state under
that ID, so every burst restarted the transmission: new call counter, sequence
back to zero, never the first-frame flag. The repeater at the other end sends
one transmission of twenty-one.

### Four corrections to patches written the same evening

Worth recording as a rate rather than as incidents.

- `0228` warned on data bursts that had not been abandoned. Fixed.
- Two patches later `0240` warned on voice headers dropped on purpose — **the
  same defect, reintroduced in a different subsystem within the hour**, and it
  filled the journal during an ordinary rag-chew.
- `0239`'s new log line said "transmission not carried" in exactly the case
  where the Motorola repeaters do carry it. The operator read it and reasonably
  concluded his text had been thrown away.
- An amendment to ADR-0029 was recommended to permit reading the standard. **No
  amendment was needed**: ADR-0040 settled it weeks ago and says so at the top
  of `internal/dmrfec/linkcontrol.go`, a file that had been edited all day.

Three tests written that evening also passed on nothing until they were broken
on purpose: a banned radio that never reaches the code under test, stream IDs
differing only in a half the encoder does not write, and an assertion about
frame counts that was arithmetic about the test's own clock.

**The pattern is not carelessness in any one of them.** It is that a change made
at the end of a long session gets the same confidence as one made at the start,
and the evidence says it should not.

### What now works

Private calls in both directions, proved on air and in a capture: `0x81`
inbound, and QSP sending `0x81` to a repeater — which no capture anywhere held
before 2026-09-06, so ADR-0046's inferred half is now measured. Two consecutive
private overs carry distinct streams. A text is one transmission. Voice is
announced as voice.

### Private text has never worked, and the reason is a missing codec

**Every data block of a text message is dropped, silently, in both directions.**
One text from a radio through the XPR8300 on 2026-09-06, every burst:

```
17:52:11.195  .233 -> QSP   54 bytes  Data header   relayed
17:52:11.258  .233 -> QSP   60 bytes  data block    dropped
   ... six blocks, none relayed
```

The preamble and the header cross. **Not one content block does.** MMDVMHost at
the far end receives a header promising blocks and never gets them, which is
exactly what §8i recorded as "the five Rate 1/2 blocks after the header are the
suspect" and could not explain.

Note the lengths: everything QSP relays is 54 bytes, every block is 60. Six
octets more, which is an 18-octet information block where a 54-byte datagram
carries 12.

**ADR-0045 predicted this in writing**: *Rate 3/4 bursts are refused rather than
truncated. None appeared in the captured texts, so this has never yet
mattered.* The one text captured for that ADR was short enough to fit in bursts
`DecodeBPTC` can read. A real message is not.

`Encoder.text` calls `dmrfec.DecodeBPTC`, which cannot read a Trellis-coded
burst, and returns false. Since 0242 that at least warns; before it, nothing.

**It explains every symptom.** KD9EJA's texts reach K9MLS because his repeater's
bursts are in a coding QSP accepts. K9MLS's never reach KD9EJA because the
content is dropped. Neither radio gets an acknowledgement because no message is
ever assembled to acknowledge. Group text on the local repeater works because
it never crosses the bridge.

### What it takes, and the permission question that does not exist

Rate 3/4 data uses a Trellis code. `internal/dmrfec` implements BPTC(196,96),
Golay, Reed-Solomon and the Data Type table and nothing else.

**ADR-0029 does not apply and an amendment was proposed in error.** That ADR
governs IP Site Connect, which has no published specification, and forbids
reading other people's *implementations* because of the derivative-work
consequence. The DMR air interface is the other side of the bridge and
[ADR-0040](docs/adr/) settled it: `internal/dmrfec/linkcontrol.go` says so at
the top, and the package already carries Golay, Reed-Solomon (12,9,4) and
BPTC(196,96) from ETSI TS 102 361-1, each cited by clause. **Rate 3/4 is the
same kind of constant on the same side of the bridge.**

Everything needed is in ETSI TS 102 361-1 V1.1.1 Annex B.2.4:

- **Table B.7**, the encoder state transition table: eight FSM states by eight
  input tribits, giving one of sixteen constellation points. The FSM has the
  property that the current input is the next state, which makes decoding a
  table lookup rather than a Viterbi search when the burst is clean.
- **Table B.8**, constellation point to dibit pair.
- **Table B.9**, the 98-entry interleave schedule.
- **Table B.10**, transmit bit ordering.
- **Table B.6**: 48 tribits in, 98 dibits out, (196,144). A flushing tribit of
  zero is appended.

The tables are not transcribed here. ETSI's copyright notice forbids
reproduction, the document is a free download, and a clause reference is what
this project cites elsewhere.

**Build decode first.** `qsp-session.pcap00` holds real Rate 3/4 bursts in both
directions whose content the operator typed, so a decoder that recovers a known
message from those bytes is proof no unit test can match. Encode second,
verified by round-tripping the same bursts — decode can be checked against
reality and encode can only be checked against decode.

### Open, in order

1. **Rate 3/4 Trellis in internal/dmrfec**, then the text path handling all
   three block codings. This is the largest single piece of DMR work left and
   the last thing between the network and working text.
2. **Private calls on air: done.** Proved in both directions on 2026-09-06.
   KD9EJA's private call to K9MLS should produce `call started` with
   `"private":true` and a `relaying transmission` line. **Whether the far end
   rings is a separate question** and is the same one Paul's private calls have
   been posing for a week: QSP delivering and MMDVMHost presenting are different
   things.
2. **Who owns Last-heard.** `DeliverFromIPSC` already calls `observe`, so the
   shared tracker records every IPSC transmission — with history, merging, end
   reasons and callsign lookup — and `/api/peers` appends the IPSC listener's own
   `CallViews` on top with no dedup. **Every Motorola over should be appearing
   twice**, with different frame counts, looking like two transmissions. It was
   not visible on 2026-09-05 only because the IPSC row was the stuck one and sat
   in Active rather than Recent. The choice is which source owns the panel; the
   tracker has everything, but a parrot transmission is consumed before
   `Deliver` and would vanish. **This is a decision for the operator, not a
   defect to fix quietly.**
3. **The 45-versus-22 gap**, now instrumented. Wait for it to recur and read the
   three counters.
4. **The rest of the UI review.** Nine pages have not been measured, and the
   peers table is the one an operator looks at most and the one the callsign
   work just changed.
5. **Repeater-to-hotspot text.** MMDVMHost accepts the preambles and the data
   header — it reads the block count out of it — and then ends the transmission.
   The five Rate 1/2 blocks after the header are the suspect. Next step is
   MMDVMHost at debug on the Pi-Star, which will say whether they arrive.
6. **Text over IPSC has no call record.** The text branch never touches
   `recordVoice`, so a text from a repeater produces no `ipsc` line and none of
   the new counters. The layer-by-layer accounting covers voice and not data.
7. **`/api/peers` is unauthenticated** and carries repeater radio IDs and
   addresses. A decision, not a defect.
8. **The vocoder**, then **subscription on air**, then **P25**.

**P25 is researched but not started.** [docs/P25-PLANNING.md](docs/P25-PLANNING.md)
records what a P25-only network would take, written after the operator asked
whether one could be built without a vocoder dongle. It can: ADR-0034 says P25
is never transcoded to reach DMR, so a P25-only network moves IMBE frames as
opaque payload and no dongle is involved. **The reflector side is ordinary work
and can be captured today on the Pi-Star at no cost**; the Quantar side is not
IP at all but synchronous serial carrying HDLC, which cannot be captured with
tcpdump and needs the Cisco router the operator already owns.

One decision waits on the operator and belongs in an ADR before any P25 code
exists: **ADR-0029 forbids reading other implementations and says nothing about
published standards.** TIA-102.BAHA is a specification rather than somebody's
code, and the V.24 reverse engineering is a capture somebody else took and
published, but this would be the first protocol knowledge to enter from a
document.

### The method, now proved twelve times

**Every reading taken by eye has been wrong. Every differential has been right.**
On 2026-09-05 an eye reading put 220 vocoder frames in a capture that holds 228,
inside a test comment, and the test caught it before the patch shipped.

Two diagnostics did all the work again: *log the same fact at two layers and read
the gap*, and *read Last-heard first when two stations cannot hear each other*.

### A test that could not fail, for the fourth time

The first version of the routing-reaper test asserted that no warning was
written — and arranged, without meaning to, for there to be nothing to warn
about: with no peers ready the burst reserved nothing. **It passed with the fix
removed.** It now asserts a reservation was taken before it asserts anything
about the journal.

This keeps happening to tests that assert an absence. A test that something did
*not* happen has to establish that the thing had a chance to happen first, and
the only way to know it does is to break the code and watch the test fail.

---

## 8l. Where the next session starts, as of 2026-09-07 night

Read §0, then §6b and §6c, then this. It supersedes §8k, whose account of the
text path still stands; everything about the console does not. **§8a is still
the section that matters most.**

### The day in one sentence

**Private text over IP Site Connect was finished and proved on air, the
container install was proved on a clean machine, and then the Links page took
production down.**

### Two defects to fix before anything else

**"We listen on" is written unvalidated.** The accept form took
`qsp.hopto.me:62045` — a public name resolving to the router — and wrote it into
an upstream. QSP cannot bind an address this host does not have, refused to
start, and systemd crash-looped to its start limit. Recovery took two rounds of
hand-edited JSON on a live server.

The field beside it *is* validated: 0259 refuses `0.0.0.0` in "They send to",
because a bind address is not somewhere a far end can reach. **The two fields
are exact opposites and only one was checked**, in the same form, the same
afternoon.

**`-check` passed the configuration the process then died on.** It reported
`is valid` and the service failed at bind time. A gate that gives false
assurance is worse than no gate, and the operator used it exactly as intended.
It should attempt the binds it can.

### The Links page failed five times in a row, all by design

Every one surfaced within ten minutes of an operator clicking through, and none
had surfaced in the code review that preceded it:

1. The offer form had **no callsign box** while the invitation is refused
   without one — an error naming a field that did not exist, followed by advice
   about two fields that were correct.
2. The address field accepted `https://` on a UDP host and port.
3. The reciprocal **demanded a passphrase that does not exist**: only one
   passphrase exists in a peering and the offering side generated it.
4. **The exchange could not terminate.** A reciprocal was built
   unconditionally, so accepting a reply produced another reply, forever. Three
   messages were spent telling the operator where to paste while the page
   manufactured an infinite regress.
5. **A link could not be removed**, from anywhere.

### The rule that was broken, and it is general

**Anything a page creates, it must be able to remove.** Nothing in this project
checked that on any page. Worth auditing the other console pages for the same
shape before adding to them.

### And the failure underneath all of it

**Five things were designed from scratch and found to be already built** — the
`/api/peers` redaction, IPSC `CallViews`, the `data` pill, the hint button, and
the entire Links page, proposed as new work while it was on screen. Each was one
grep away.

The deeper version, which is what actually cost the evening: **the peering flow
was reviewed by reading it and never by using it.** This project's whole method
is that defects come from running the system. That was applied rigorously to the
radio side all day — 54 bursts, 42 blocks, an opcode measured from sixteen
preambles — and not once to the console.

### What is finished

**Private text over IPSC**, confirmed on air by the operator. The trellis codec
is proved against 54 real MMDVMHost bursts: 54 of 54 decode, and **0 of 54** with
the tables that shipped in 0242. Both Rate 1/2 and Rate 3/4 have fixtures. One
text is one row in Last heard.

**The container install**, run on a clean Ubuntu VM: nine defects found, the
worst a database landing outside the volume — silent data loss on every rebuild.

### Open, in order

1. Validate the listen address where the accept handler writes it.
2. `-check` should attempt its binds.
3. **Deploy 0260**, committed and never shipped. It turns tonight's recovery
   into two clicks.
4. [ADR-0049](docs/adr/ADR-0049-first-account-setup-token.md): the first
   administrator account from the home page. Decided, not built.
5. No peer has ever registered with a containerised instance.
6. The remaining console pages have never been reviewed by using them.

---

## 8k. Where the next session starts, as of 2026-09-06 night, text complete

**Superseded by §8l.** Its account of the text path stands; its open list does
not.

Read §0, then §6b and §6c, then this. It supersedes §8j; everything §8j settled
remains settled except where named. **§8a is still the section that matters
most.**

### The whole day, in one sentence

**A text message has never carried its content, and the codec written to fix
that was wrong in a way no test in this repository could have caught.**

### Text now works, and the block is measured

[ADR-0047](docs/adr/ADR-0047-rate-34-text-blocks.md).
`testdata/ipsc/ipsc-text-rate34.pcap`.

Every content block of every text was dropped in both directions, because
`ConvertText` refused anything that was not twelve octets and `Encoder.text`
handed every burst to `DecodeBPTC`. A Rate 3/4 block is eighteen octets and
needs a trellis.

**The block is sixteen octets of user data, then a seven-bit serial number and
a nine-bit CRC**, and three independent things say so: the user-data halves of
one transmission's six blocks concatenate into an IPv4 datagram whose addresses
are Motorola's radio-IP encoding of the two radio IDs in the envelope, carrying
UDP on 4007 whose payload reads *"I can't talk right now..."*; the serial
numbers run 0 to 5; and the CRC-9 verifies on 42 blocks out of 42.

**Clause 8.2.2.2 draws the block the other way round**, control pair first. The
two octets are identical either way and only their position differs. That is
the one unmeasured step; see below.

The CRC is not the one clause B.3.11 describes — B.3.11 puts the serial first
and adds an inversion, and that matches none of the 42 blocks under any
nine-bit generator. All 256 were searched against seven message orderings and
both inversions; exactly one combination matches everything, and it is
B.3.11's own generator over the message in IPSC's order with no inversion.

### The defect that no test could see, and why that matters more than the fix

`trellis.go` shipped with all sixteen constellation entries wrong: it mapped
`+1 → 01, -1 → 00, +3 → 11, -3 → 10` where table 10.3 gives `01 → +3,
00 → +1, 10 → -1, 11 → -3`.

**The wrong mapping is a permutation of the four dibit values**, so encode and
decode agreed with each other perfectly, every shape test passed, and the
package was internally consistent and externally useless. Wiring it in as it
stood would have transmitted well-formed bursts no radio could read, with a
symptom identical to the one being fixed.

The file's own header had said the tables were *checked only by this package
agreeing with itself*. **A file that documents why it cannot be trusted is not
the same as a file that has been checked**, and a session read that sentence,
wrote a handover saying "wire the codec in", and moved on.

The transferable rule: **a round trip through your own tables proves wiring,
never correctness.** The tests that carry weight are the ones ending at a fact
outside the repository — an IPv4 header, a CRC over somebody else's bytes, a
sentence the operator typed. Every test in `rate34_test.go` that could pass
with wrong tables says so in its own comment.

### Two offsets that had been measured and filed as exceptions

ADR-0045 recorded that byte 30 agrees with the low nibble of byte 51 in 153 of
162 frames and that *the nine exceptions are the 60-byte Rate 3/4 frames, where
byte 51 is not the Slot Type.* **That was the whole defect, written down as a
footnote three days before anybody looked at it.** A 60-byte datagram carries
an eighteen-octet block, so everything from byte 38 sits six bytes later and
the Slot Type is at 57.

Bytes 32 to 37 are not constant either: `00 0d 80 0a 00 90` against
`00 0a 80 0a 00 60`, and byte 37 is the payload bit count, 96 against 144.

**Nine frames where a documented offset does not hold is a measurement of a
different layout.** It is not an exception, and calling it one costs whatever
the feature was worth.

### The capture was taken, and it settles everything

`testdata/hbp/hbp-text-rate34.pcap`: **54 Rate 3/4 bursts from MMDVMHost**,
recorded while the operator typed `Hi` into a handheld.

| Constellation mapping | Bursts decoded |
|---|---|
| Table 10.3, corrected in 0243 | **54 of 54** |
| The mapping that shipped in 0242 | **0 of 54** |

**49 verify their CRC-9 control-first and none verify control-last**, so
`dmrfec.Rate34AirOrder` is measured. **QSP's encoder reproduces all 49 bursts
byte-for-byte** — the only check here a wrong table cannot pass, because the
other side came out of somebody else's encoder. The three blocks reassemble
into an IPv4 datagram carrying UTF-16 little-endian `Hi`.

And `testdata/ipsc/ipsc-text-rate34-out.pcap` shows QSP transmitting twelve
Rate 3/4 datagrams in production from build 0.1.85, where the same path
recorded eight hours earlier carried none.

**The capture existed for eight hours before anybody read it.** It was recorded
at 22:39 while a stale binary was being chased, and three further requests were
made for a capture already on disk. *Run the system and read what it says* has
a corollary: read it when it arrives, not when the argument runs out.

### Open, in order

**Text is done.** Confirmed on air repeater to repeater. Hotspot delivery works
without the sending radio's confirmation, which is accepted rather than open:
the ack comes from a repeater on RF one hop from the radio and a hotspot has
none. The stream IDs turned out not to be a second defect — 224 single-CSBK
preambles and 18 header-and-blocks streams, decomposing without remainder.

**Going public is now the shape of the work.** [ADR-0048](docs/adr/ADR-0048-container-install.md)
records the container install decisions; the operator wants `docker compose up`
to work on first run for people who are not advanced. Its first step is a
decision rather than code, and `deploy/docker` turns out never to have been run
— no UDP ports, Go 1.22, the wrong volume path.

1. **`/api/peers` is unauthenticated**, and it now gates something: ADR-0048's
   console bind address cannot default to `0.0.0.0` until it is settled, and
   `127.0.0.1` is a wall on minute one for a newcomer. A decision, not a patch.
2. **Text over IPSC still has no call record.** The text branch never touches
   `recordVoice`, so a text from a repeater produces no `ipsc` line and none of
   the transmission counters. Carried forward from §8j.
4. **Who owns Last-heard.** Unchanged from §8j: `DeliverFromIPSC` calls
   `observe` and `/api/peers` appends `CallViews` on top with no dedup, so
   every Motorola over should appear twice. A decision for the operator.
5. **The 45-versus-22 gap**, still instrumented, still waiting to recur.
6. **The rest of the UI review.** Nine pages unmeasured.
7. **`/api/peers` is unauthenticated.** A decision, not a defect.
8. **The vocoder**, then **subscription on air**, then **P25**.

### Corrections to §7 and to the last handover

**staticcheck runs in this container.** §7 and `HANDOVER.md` both say it cannot,
and that a patch will fail as a gate chain stopping before the tests. Built
through the full 1.22 → 1.23 → 1.24.6 → 1.27 chain it runs clean over the whole
tree in about a minute. The baseline is still seven named failures in
`cmd/qsp`, and still nothing else.

**`qsp-session.pcap00` was cited by §8j and by `trellis.go` and was not in the
repository.** It is 2,5 MB and mostly voice; the 166 datagrams that matter are
now committed as `testdata/ipsc/ipsc-text-rate34.pcap` with the parent's md5 in
its provenance note. Two documents rested on evidence nobody else could check,
and one of them described the block backwards.

### The method, now proved fourteen times

**Every reading taken by eye has been wrong. Every differential has been
right.** Two more this session, both caught by a test rather than by review:
the Text Messaging Service header is ten octets rather than twelve, and its
text is UTF-16 little-endian rather than big.

And a new one worth keeping: **a constant that round-trips is not a constant
that is correct.** Sixteen wrong table entries survived a full test suite
because they were wrong consistently.

---

### Review it by using it, not by reading it

**Five defects in the peering flow surfaced within ten minutes of an operator
clicking through, and none had surfaced in the code review that preceded it.**
An offer form with no box for a required field; an address field accepting a URL
where a host and port belong; a reciprocal demanding a passphrase that does not
exist; an exchange that could not terminate; and a page that creates
configuration it cannot remove.

This project's method is that defects come from running the system. That was
applied rigorously to the radio side — 54 bursts to prove a codec, 42 blocks to
prove a CRC, sixteen preambles to name an opcode — and **never once to the
console**, which is the half an operator actually touches.

A page is run by clicking every control on it in the order an operator would.
Reading it finds none of this.

### Check whether it exists before designing it

**Five things in one day were designed from scratch and found to be already
built**: `/api/peers` address redaction, IPSC `CallViews` returning nil, the
console's `data` pill, the hint disclosure button, and the whole Links page with
its offer-and-accept peering flow.

Every one was a single grep away, and the cost was not only wasted work — twice
the near-miss was shipping a *second* way to say the same thing, which is how a
codebase stops having one answer to anything.

The rule below is about open items. This is the wider one: **before writing a
design, grep for the thing.** It takes ten seconds and it has been wrong five
times out of five.

### Anything a page creates, it must be able to remove

The Links page wrote an upstream, a bridge and a passphrase file, and nothing in
the API or the console could undo any of it. The operator found out on a live
production server, at the point where the thing it had written stopped QSP from
starting, and recovery meant hand-editing JSON twice.

**Nothing in this project checked that rule on any page.** It is worth auditing
the others before adding to them.

### Verify an open item before working it

**Three items on §8k's list turned out already done, in one afternoon.**
`/api/peers` redaction was built and commented; IPSC `CallViews` returns nil
with "the tracker owns Last heard" written above it; and the console has
labelled data with a muted `data` pill since the call tracker learned that one
text produced fifteen entries.

Each was carried forward across handovers verbatim. **An open item that has
survived several sessions is a claim about the past, not the present.** Verify
the defect still exists — one grep — before working it, exactly as a capture is
read before a protocol is reasoned about. The alternative is building a thing
twice and shipping two ways to say it, which nearly happened with a `TEXT` pill
beside the `data` pill that was already there.

## 8a. How this project finds its defects

> **2026-09-04, four for four.** Every defect found that day came from running
> the system: a whole timeslot of audio that never crossed the bridge, a
> negative duration on the dashboard, fifteen false warnings per text, and a
> talkgroup on the wrong timeslot in a codeplug. Three bug hunts and a green
> suite found none of them.
>
> **Two diagnostics did the work, and neither is a test.**
>
> *Log the same fact at two layers and read the gap.* The IPSC listener logs a
> call started and so does the DMR side. When the first appeared and the second
> did not, that was the whole diagnosis of the timeslot defect.
>
> *Read Last-heard before anything else when two stations cannot hear each
> other.* A talkgroup appearing on two different timeslots from two stations is
> invisible in a log and unmissable in a four-row table.


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

### The question that keeps finding things: what is declared and read by nothing?

Six defects in one day came from asking it, and none of them from running
anything:

- `Upstream.Export` and `Upstream.Import`: validated, stored, documented as what
  crosses in each direction, consulted by no routing code.
- `--target-min`: a 44px touch target declared in `tokens.css` and referenced by
  nothing, next to a 20px button.
- `database.busy_timeout`: documented, defaulted to five seconds, validated on
  startup, applied to no connection. `sql.Open` took SQLite's defaults, so
  contention failed instead of waiting, a writer blocked every reader, and the
  `ON DELETE CASCADE` in migration 0003 had never fired.
- `ActionUserLogin`, `ActionUserLogout`, `OutcomeDenied`: declared, listed in
  `knownActions`, and emitted by nothing. **No authentication was ever
  audited**, while SECURITY.md said the audit trail records who did what.
- The peering invitation took its network ID from an existing link, so the first
  peering an instance ever attempted could not be generated.

**The tenth instance was not a field but a call site**, and it was found by
reading the three ingress paths side by side rather than by grepping one name.
`internal/peers/listener.go` has three: `forward`, `DeliverFromIPSC` and
`DeliverFromUpstream`. The first two end in `sendToIPSC` and the third did not,
so no frame arriving over a link was ever offered to a Motorola repeater. The
method generalises: **when a function is called from some paths of a set and not
all, list the set and check each one.**

```sh
grep -n "sendToIPSC" internal/peers/listener.go
```

Two call sites against three entry points is the signal, and it is as legible
as zero hits.

A field that is documented, defaulted, validated and included in a map of known
values looks maintained. None of that means anything reads it.

```sh
grep -rn "\.FieldName\b" --include=*.go . | grep -v _test
```

Zero hits outside the declaring package is the signal.

### A new instrument is a claim, and it is the last thing anybody doubts

**2026-09-08 evening.** A deploy was checked with a new check — an md5 of
`/proc/PID/exe` against `/usr/local/bin/qsp`, proposed that same session to
replace `qsp --version`. It reported a mismatch. It was wrong: `cmp` on the same
pair says identical, `stat -L` gives one inode, and the process had been running
the new build since before the first reading. Twenty-five minutes went on it.

What matters is the order in which things were doubted. Three theories were
offered about the estate — a `sudo` prompt eating the `systemctl stop`, a
deleted inode, a mount namespace — and §7's rule against a third guess was
quoted immediately before the third guess was made. **The instrument was
doubted last, and it was the newest thing in the room.** Every other element had
weeks of use behind it.

So, alongside "what is declared and read by nothing?": **when a measurement
surprises you, how old is the thing doing the measuring?** A check introduced
this session has no track record, and the first thing it reports is also its
first test. It should be the first suspect, not the last.

Two details worth keeping, because they are what made it convincing:

- **It was reproducibly wrong**, not flaky — the same wrong digest twice, across
  two PIDs. A flaky instrument advertises itself. A consistent one gets acted
  on.
- **It was wrong in the same direction as the expected failure.** A stale
  deploy was exactly what was being looked for, and the instrument said stale.
  An instrument that confirms the hypothesis you brought is not evidence for it.

The check that settled it, `grep` for a string the build introduced, was already
in §8o from the container work and was not thought of as available on
production. **Ask what already works on one machine before inventing something
for another.**

### Check what a deletion would break before deciding it is small

**2026-09-09.** Removing two configuration fields that nothing read looked like
tidying. `config.Load` refuses unknown fields — deliberately, so a typo cannot
leave a default in place — and both fields were declared without `omitempty`, so
every save wrote them. Deleting them from the Go struct would have made every
configuration already on disk unparseable, and **the failure would have arrived
at the next restart** rather than at the change: a server carrying traffic,
stopped hours later by an edit that looked like nothing.

The check was one grep and one look at `Load`. It turned a deletion into a
mechanism the project needed anyway, and the mechanism was then proved on a live
server that would otherwise have refused to start.

So, before removing anything from a document a server reads: **what happens to a
document already written that contains it?** The answer is in how it is parsed,
not in how it is used, and "nothing reads it" is an answer to the wrong
question.

The same morning gave the smaller half: an unreachable state. The
administration page reported a lookup that was on and could not run, and
`config.Validate` refuses that combination — so no server can hold it, the
branch rendered nothing, and the test covering it asserted against a struct
built by hand. **A state prevented by validation does not also need reporting,
and a test that builds its own subject cannot fail.** Seventh time.

### A recommendation that requires what it just ruled out is wrong

**2026-09-09.** Research into bridging DMR to Zello concluded, correctly, that
QSP must never contain a vocoder: it copies AMBE payloads and never inspects
them, which is why DMR-to-DMR needs no codec and why the project has no patent
question. The same note then recommended that QSP speak **USRP** — a protocol
carrying 8 kHz PCM, which can only be produced by decoding the audio.

**The two halves of one recommendation contradicted each other**, and both were
argued at length, which is what made it convincing. It reached the handover and
would have cost a session writing a connector with nothing to put in it.

The check is mechanical and takes a moment: **read the recommendation against
the constraint it was written under.** If the constraint is "this program never
does X" and the recommendation requires X, the recommendation is wrong however
well it is argued.

The corrected answer was in the same documents the research had already read.
Analog_Bridge has two sides: TLV frames carrying AMBE on the one an `xx_Bridge`
connects to, and PCM over USRP on the other. Reading one stanza further would
have settled it.

### A rule written into a test does not reach the shell

**2026-09-09.** A gate was written that morning to keep one product name out of
the repository, and it was carefully word-bounded on both sides — because an
earlier draft had matched `func bridgeState`, the *c* of `func` followed by a
space and `bridge`, and because it must never see the `ipscbridge` package,
where the *c* is preceded by an *s*.

An hour later, verifying a history rewrite before force-pushing it, **three
consecutive ad-hoc greps made exactly that mistake**: a case-insensitive search
with no word boundary, which matched all twelve files of the IPSC bridge
package, then the same on commit messages, then again. Each produced a non-zero
count that read as "the rewrite failed", on an operation where believing it
would have meant either abandoning a correct rewrite or, worse, pushing an
unclean one while chasing a phantom.

The two checks that exist for this both fired on the first draft of this very
section — one on the search pattern quoted literally, one on a path written with
an ellipsis in it. That is the checks working, and the reason this paragraph
describes the pattern rather than spelling it.

**A test encodes a rule; a command typed afterwards does not inherit it.** When
a check exists for something, reuse its expression rather than writing a fresh
one from memory — the test had the right pattern in it the whole time.

The wider version: **a verification command is a claim like any other**, and one
written in a hurry against an operation that cannot be undone is the worst place
to be casual. §8a already records that a new instrument is the last thing
anybody doubts. This is the same failure with the instrument written three times
in five minutes.

### Rewrite a file rather than patch it a fifth time

**2026-09-09.** Four consecutive pattern-matching edits to one console script —
each replacing a string, each looking right — deleted a function while leaving
its call site. The script threw at load, every page lost its administration
navigation, and it shipped, because the whole gate chain is Go and reads no
JavaScript.

The edits were individually correct and collectively destructive: each was
written against a file the previous edit had already changed, and none of them
looked at the result. **After the second edit to one file in a session, stop
replacing strings and read the whole thing** — or, as here, take the last
known-good copy and apply the change once.

And the gap it exposed: a language outside the gate chain gets no checking at
all until somebody writes one. There is now a check that a console script
defines every function it calls, which is crude and stops the failure that
takes a page's chrome down without a word.

### Fix the half that is called, not the half that is named

**Added 2026-09-09, having broken this rule the same day it was written.** The
version was added to the login response instead of the session response — one
is asked once, the other on every page load, and the console reads the second.
The sidebar stayed empty through a correct build, a correct deploy and a correct
version check.

**The rule was written as a debugging habit and is needed as an authoring one.**
When adding a value for a caller, open the caller. A struct literal that
compiles and a field that is populated prove nothing about whether the thing
that wanted it ever sees it — and "assert the field is set" is a different test
from "assert the caller receives it". Write the second.


**2026-09-09.** A QSP link was offered the OpenBridge port. The fix gave the
offer its own address default — and put it on the *fallback* used when the
request arrives with an empty address, which the console never sends, because
the page prefills the box from a different function. The corrected code was
unreachable. A server running the fix produced the same wrong port, and the
patch's own tests passed.

**The hazard had been written down four patches earlier**, in the handover's
loose threads, in as many words: two functions naming the same idea differently
is the shape that produced the wrong port. Writing it down was not enough; the
second place the value lived was never looked for.

So, when fixing a value that is wrong: **find every place it is produced before
changing any of them.** A grep for the *value* rather than the function name
finds them; a grep for the function you are about to edit finds only the one you
already know about. A value in two places disagrees with itself, and fixing one
of them looks exactly like fixing it.

The same night gave the other half of this: a link whose name was not lowercase
could not be sent to, because the routing core stored names lowercased and built
targets from the key while the registry looked links up by the configured name.
One value, two questions, and they disagreed. **Ask what each reader needs
before storing one form of anything.**

### A design is true of a premise, and premises change

**2026-09-08, twice in one evening.** Two defects came from reasoning that was
correct when it was written and was not revisited when the thing making it
correct changed.

0284 removed the Remove button from inbound links, because an inbound link is in
nobody's configuration — this server received a registration, not a document, so
there was nothing to delete. Forty minutes later 0288 made the offering side
allocate a DMR ID and a password for exactly those links. The button stayed off.
A server could not refuse a neighbour from its own console, which ADR-0052 rule
1 requires of a federation, and the operator found it by looking at the page.

The same evening, `defaultLinkAddress` was reused on the QSP link path. It
hardcodes the OpenBridge port, which was right for the only caller it had. The
link path made that false, and the form then suggested an address no link can
dial — valid `host:port`, correct configuration, unreachable.

So the question to ask beside "what is declared and read by nothing?" is **what
was this true of, and is that still the case?** A comment explaining why
something is absent is the place to look: it names its own premise, which is
what makes it checkable. Both of these said so in as many words.

### A test that has never failed is a test you do not believe

Three tests written the same day asserted something adjacent to the thing that
mattered and passed against the code they existed to reject:

- A wrapping check searched for `white-space: pre`, which `pre-wrap` contains.
- A `[hidden]` check found the phrase inside the comment explaining the bug.
- A logout ordering check found `EndSession` in an interface declaration above
  the handler.

Each was caught by deliberately breaking the code and watching the test stay
green. **Do that before trusting any new assertion.**

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

## §8m — 2026-09-08, the night the linking system was used

Supersedes nothing; extends §8a, which remains the section that matters most.

### The number that settles the argument

**Nine defects came from running the system. Three came from reading it.**

The reading was not casual: an hour of directed bug hunting, over one
subsystem, with the specific questions §8a recommends. It found four real
defects. In the same session, using the page and reading a log found five more —
including one sitting four lines from code three patches had been built on.

The clearest single case. 0262 added `reconcileLinks` so that a link which is
configured but not yet open appears on the page. It was written after a live
failure, tested nine ways, gated, reviewed and shipped. **It sat four lines
below an early return that fires in exactly the case it was written for**, and:

- the bug hunt went past it,
- three later patches were built on top of it,
- the suite was green throughout,
- and one peering, run by hand, produced it in ten minutes.

Do the bug hunt. It is worth an hour. **Then use the thing**, because the hunt
does not replace it and never has.

### The shape every defect had

All seven this session were **a rule enforced somewhere other than where it was
written**:

- One box fed the bind address and the reply address, which are opposites.
- The page read the running set; the remover read the configuration.
- Upstream names were checked for duplicates; their listen addresses were not.
- The exchange ended in memory, and only if a box was left blank.
- A name was a display string in one place and a file path in another.
- Actions were declared in one file and emitted as free-form strings from
  another.
- `NeedsRestart` knew, and the page never asked.

**The question that finds these: where else is this fact stated?** If the answer
is "two places", one of them is already wrong or will be. The fixes all took the
same form — put the rule in the single gate everything passes through, and
delete the second copy. `config.Validate` is that gate for configuration;
`cmd/qsp` had grown its own listener list and no longer has one.

### Breaking a test to prove it is harder than it looks

Eleven breaks were attempted across the session. **Five were wrong**, and every
one of them looked like a weak test rather than a bad break:

- Two did not compile, and `grep -E "^--- FAIL"` rendered the build error as
  silence — which reads exactly like a passing test. **A break harness must
  detect a build failure explicitly.**
- One patched the first match of a string that appeared in two functions, so it
  edited a different handler entirely.
- One patched only one side of a comparison whose other side already matched.
- One deleted a line, orphaning a variable, and did not compile either.

And three tests, written this session, **passed against broken code**:

- `err != nil` on a configuration that was invalid for unrelated reasons.
- `strings.Contains(css, "min-width")` over a whole stylesheet holding a dozen
  other rules.
- A field name searched file-wide, found in the render function, while the save
  had it deleted.

**Scope an assertion to the thing it is about.** File-wide string searches pass
for the wrong reason, and this project now has six recorded instances.

### Running the system: what it actually told us

- Two audit lines and no warning proved SECURITY.md's claim true for the first
  time. The claim had been in the document for weeks and never in the code.
- `-check` on a live server reporting three addresses in use and one bindable
  proved the classification is right in both directions at once.
- `frames=106 converted=104 delivered=104` alongside `every destination refused
  the frame` separated a working codec from a broken route in one line.
- A 121-byte response repeating every five seconds was the whole diagnosis of
  the reconcile defect, and matched a broken build byte for byte.

**Read the numbers on both sides of a link.** `Sent 50 / Received 0` on one end
and `Received 28 / Sent 0` on the other located a one-way failure immediately
and pointed at the bridge rather than the link.

### On being asked for perfection

The instruction was to hunt bugs rather than run the system, and it was the
right instruction to give — four defects. It was also not sufficient, and
saying so at the time was worth more than agreeing. Both happened, and the
session is better for having done both in that order.

### §8m addendum — what 2026-09-08 midday settled

**A stale caveat is worse than no caveat.** The IPSC relay announced on every
start that no capture of a master sending voice existed. It had existed since
2026-09-03 — `testdata/ipsc/ipsc-master-voice.pcap`, 347 packets, 288 of them
voice, from the XPR8300's own RF, read by four tests. That sentence was quoted
twice as evidence the transmit direction could not be checked, and it sent an
hour in the wrong direction while the reference sat in the tree.

The rule this gives: **a document that names something as missing must fail when
that thing arrives.** `TestNoCaveatDeniesAFixtureThatExists` reads app.go's
string literals through the AST — not the file, because the comment explaining
the fix quotes the old caveat and made the first version of the test fail
against corrected code.

**Two servers on one LAN must address each other by LAN address.** Production's
link pointed at `192.168.1.27` and the test server's at `qsp.hopto.me`. The LAN
direction carried; the public-name direction left the network for the router and
never came back. Frames were sent, nothing arrived, and **nothing was rejected
anywhere** — a NAT hairpin produces no error on either side, only two counters
that disagree. §7 had already recorded this for the Pi-Star and it was not
generalised.

`Sent 50 / Received 0` on one end beside `Received 28 / Sent 0` on the other is
the signature. Read both ends' counters before theorising about routing.

**The XPR8300 needs `ipsc.slot_bit_is_timeslot2: true`** with TG2 on TS2 in the
codeplug. Audio arrived reporting `timeslot=1` against a TS2 bridge and every
destination refused it. Diagnosed by reading `timeslot=` on the IPSC line and
the network line of the same transmission — two log lines, not reasoning about
slot bits.

**Matching the wrong stream ID reads as proof.** A transmission was declared to
have crossed the link because a stream ID appeared on both servers 5 ms apart.
It was a different transmission; the repeater's stream appeared in no journal on
production at all. **Grep for the stream ID the source logged**, not the one
nearest in time.

**A fifth wrong-reason test**, bringing the session's total to five: an
assertion searched a whole file for a function name and found the function's own
declaration, passing against a file with the call deleted. Scope an assertion to
the thing it is about — the call site, the rule body, the string literal.

**OpenBridge forces timeslot 1, and the peering form did not know.** Every
bridge the accept handler wrote put the operator's chosen timeslot on the
*upstream* endpoint, so nothing arriving from the link ever matched it. Two
instances peered, both healthy, no audio either way for a day. The signature is
one instance reading TS2 for a transmission the other reads as TS1, and counters
that move on one side only. `internal/protocol/openbridge/openbridge.go`
had said so in a comment the whole time.

The general form, which is the fourth instance of it this session: **a protocol
constraint stated in one package and not enforced where configuration is
written.** The accept form asks a question the protocol has already answered.

**A fix that is necessary is not therefore sufficient.** The OpenBridge timeslot
correction above made link-sourced frames reach routing, and it was written up
as the cause of the silence. It was not: `sendToIPSC` then dropped them anyway.
Two faults on one path, and fixing the first made the second visible rather than
making the symptom go away. **When a change is necessary but the symptom
persists, do not restate the change as the answer — look for the next gate.**


## 8n. What the afternoon of 2026-09-08 taught us

**A handover's diagnosis is a hypothesis, and it was wrong.** The morning
handover opened on `sendToIPSC` returning early at a named line, with the code
quoted and the log lines beside it. Every word of it was true about that
function and it was not the fault: `DeliverFromUpstream` never called
`sendToIPSC`, so the gate was never reached. The evidence had been read
correctly and the conclusion did not follow, because the reason in the log was
true of something else at the same moment.

The general form: **a written diagnosis carries the authority of having been
investigated, and inherits none of the verification.** Open the file the
handover names and read the call sites before writing the fix it asks for. It
cost ten minutes here and would have cost a patch that changed a condition
nobody evaluates.

**The version in a handover is a claim about the past.** The file said 0.1.107
and patches 0261–0265; the tree was 0.1.112 with five more commits, two of them
documentation-only patches that recorded the defects without fixing them. A
handover appended to across a session grows a stale head. Read `VERSION` and
`git log`, not the sentence.

**A validation rule's first run is a survey.** The new
`config.Validate` rule refusing TS2 on an OpenBridge endpoint was written for
the accept form and immediately failed two shipped example configurations,
`deploy/pair/alpha.json` and `deploy/pair/bravo.json`, which nobody had
suspected. `TestThePairFacesItself` had exercised that pair for as long as it
existed and asserted only that it loaded and faced itself, never that it could
carry a frame. **When a new rule fires somewhere unexpected, that is a find, not
a false positive** — read every hit before relaxing anything.

**Prove each half of a two-defect fix separately.** Both faults sat on one path
and either alone produced the same silence, so a test written after both were
fixed would pass with either one reverted and nobody would know. Reverting them
one at a time takes two commands and turns "the tests pass" into "each defect is
covered".

**Refuse rather than silently correct, when the configuration being refused is
already broken.** `config.Validate` could have moved a TS2 link endpoint to TS1
and started. It refuses instead: the document would otherwise say one thing
while the network does another, and the configuration in question is one where
the link carries nothing. A startup error naming the fix is strictly better than
a day of healthy counters and silence. This is not general licence to add
refusals to a running network — §7 records a service that would not start
because two rules disagreed — and the distinction is whether the refused
configuration could ever have worked.


## 8o. The evening two QSP servers heard each other, 2026-09-08

**Audio crossed a QSP-to-QSP link in both directions for the first time**, and
the log line that proves the design is `timeslot=2`. Every previous run read
TS1, because OpenBridge forces it and two instances of the same software were
using OpenBridge to talk to each other.

The whole configuration on the dialling side is one block: name, address, DMR
ID, password file, callsign. `bridges=0`. No listen address, no export list, no
import list, no timeslot, no port forward. If a link ever needs more than that
again, something has been added that ADR-0051 says should not exist.

**Three faults today had one cause, and the cause was a mechanism rather than a
mistake.** The only way to get traffic to a link was to write a bridge; a
bridge joins endpoints; an endpoint carries a timeslot. So the accept form
asked an operator a question the protocol had already answered, and there was
no right answer to give. Look for the missing mechanism when a form asks
something nobody can answer.

**A deploy check that always prints something is not a check.** `qsp --version`
runs the binary on disk, which `install` has already replaced, so it answers
identically before and after a restart; in the container it reads
`development`, because the Dockerfile hardcodes `-X main.version=development`.
The check §7 relies on has never worked on either server. What did work:

```sh
sudo md5sum /proc/$(systemctl show -p MainPID --value qsp)/exe /usr/local/bin/qsp
docker cp qsp:/qsp /tmp/q && grep -c "<a string only this build has>" /tmp/q
```

**And the first grep string was wrong**, matching text that had existed for
weeks and reporting success for a container that did not have the patch. Pick a
string the new build introduced, not one it merely still contains.

**`install` over a running binary raced its own restart.** The md5 of
`/proc/PID/exe` differed from the file. `systemctl stop`, install, `start` was
the fix, and `systemctl is-active` reported `active` throughout — on the old
binary.

**A build interrupted with ^C leaves the previous image**, and `up -d` then
recreates from it and says `Started`. Twice. The compile stage takes two
minutes; let it finish.

**Commands for the wrong machine, three times in one afternoon.** The cause was
not careless labelling: the documentation described one deploy and the estate
has three, so commands were written from the handover's picture rather than
from what is there. Fedora runs no sshd and nothing pulls from it; production
is systemd; the test server is Docker Compose with a build override, and
without that override `up -d` reaches for a registry tag that has never been
published, is denied, and silently leaves the old container running.

**A research question is worth asking before a design question.** "Should this
be a new protocol on a new port?" was answered no by looking at what XLX, DPlus
and the homebrew protocol actually do about NAT. XLX interlink and DExtra both
cost a forward at each end and are known sore points; the homebrew peer dials
out and needs none, which is why a Pi-Star works behind a domestic router with
nothing configured. The answer was already in the tree — `UpstreamHomebrew`
existed, built and never pointed at a real far end.
