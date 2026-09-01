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
| Version | 0.1.12 |
| Tests | **825 test functions**, 3,733 results including subtests, all passing. Count them as `grep -rhoE '^func (Test|Fuzz|Example)[A-Za-z0-9_]*' --include=*_test.go . \| wc -l`, so the number means the same thing next time |
| Race detector | clean |
| Dependencies | **one direct** — `modernc.org/sqlite`, pure Go, no cgo (ADR-0017). QSP's own code is standard library only |
| Cross-compile | linux/amd64, arm64, armv7 — all `CGO_ENABLED=0` |
| Health report | **12** subsystems, plus one per configured link — `ipsc` joined the unbuilt list at 0.1.13 |
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
| 4 — IPSC | A Motorola repeater is a peer of a QSP master | **next after DMR is finished**, promoted ahead of P25 on 2026-08-31 at the operator's direction. Blocked on a capture ([ADR-0029](docs/adr/ADR-0029-ipsc-from-capture.md)), and on nothing else |
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

IPSC, P25, vocoder pool, AllStar, Zello, EchoLink. Each registers a health check
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
- **The development container ships without Go, and no allowed domain carries a
  Go binary.** `go.dev/dl` and the module proxy are both outside the egress
  allowlist; `golang/go` on GitHub publishes source, not binaries; Ubuntu's
  newest package is 1.22. So the toolchain is bootstrapped from the source tag
  on `codeload.github.com`, and 1.22 cannot build 1.27 directly — the bootstrap
  minimum is enforced at run time, not by a build tag, so the chain is
  **1.22 → 1.23 → 1.24 → 1.27**, about twenty minutes. Do it first, before
  writing anything, because a documentation-only patch still has to pass the
  accuracy gate and the accuracy gate is a Go test.
- **`modernc.org/sqlite` cannot be fetched in the container either.** Move
  `cmd/qsp/driver_sqlite.go` aside and the tree builds with the standard library
  alone, which is the point of ADR-0017. Move it back before generating a patch.
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

## 8c. Where this session starts, as of 2026-09-01

Read §0, then §6b and §6c for the rules that break ties, then this.

### Settled, do not reopen

Everything in §8b's list, plus:

- **Phase 2's gate is closed.** AD0MI joined unassisted from the join page and a
  password. Do not propose re-testing it; there is no second first-time member.
- **IPSC comes before P25.** Reach, not difficulty. See §2.
- **Audio is king**, and **talkgroup numbers are never renumbered**. §6c and §6b.
- **Talker Alias is pass-through.** Nothing injects bursts B–E.

### Open, in order

1. **IPSC, and specifically the capture that unblocks it.** The largest gap by
   reach: a club with a Motorola repeater cannot use QSP at all. There are now
   two repeaters available — the operator's XPR8300 in his lab and an SLR5700
   belonging to a colleague — which is what a peer list and a real registration
   need. [`testdata/ipsc/CAPTURE-PLAN.md`](testdata/ipsc/CAPTURE-PLAN.md) is
   the operational plan for this equipment;
   [`testdata/IPSC-CAPTURE-REQUEST.md`](testdata/IPSC-CAPTURE-REQUEST.md)
   remains the version handed to a stranger.

   **[ADR-0029](docs/adr/ADR-0029-ipsc-from-capture.md) holds: no IPSC wire
   format code before a capture exists — not a parser, not a constant, not a
   message type.** And DMRlink and HBlink3 stay unread until a capture is in
   hand and has been shown not to answer something, because reading them binds
   the project to a derivative work permanently and cannot be undone.

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

A field that is documented, defaulted, validated and included in a map of known
values looks maintained. None of that means anything reads it.

```sh
grep -rn "\.FieldName\b" --include=*.go . | grep -v _test
```

Zero hits outside the declaring package is the signal.

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
