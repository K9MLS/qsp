# QSP — Project Memory

**Single source of truth. Regenerated at major milestones.**
Last regenerated: 2026-08-25, at 0.1.4, after the Phase 1 gate closed and the
repository went to GitHub. **Duplicate sections collapsed 2026-09-02** — the
file had grown six copies of §8f in five versions, and a session read the wrong
one. Newest session notes are at the end of the §8 series; read §8g first.

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
- **Ask the running binary which commit it is.** `qsp --version` prints a
  pseudo-version naming the commit it was built from, and the same string is in
  the `starting` log line. An afternoon went on diagnosing a bridge that was not
  deployed: the service was `active`, the deploy commands were right, and the
  binary was three commits old because the patch file had never reached the
  machine. `systemctl is-active` says something started; only the version says
  *what*. Check it after every deploy, before keying a radio.
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

## 8g. Where the next session starts, as of the evening of 2026-09-02

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
  Motorola repeater points at QSP; no Motorola master repeater alongside, no
  a commercial DMR server. **QSP is never an IPSC peer in production**, which removes half the
  protocol from the project's obligations permanently.

  It is replace, not augment: a club whose existing IPSC master they cannot
  reconfigure cannot adopt QSP incrementally. Accepted deliberately.

  A bench instrument may play a peer in order to observe a real master. That is
  a diagnostic in the same category as `cmd/ipsc-probe`, never ships in
  `cmd/qsp`, and is not a route back to peer support.

  **The limit worth remembering:** QSP has authority over delivery, not over
  transmission. A repeater receives everything and filters by its own codeplug,
  which QSP cannot learn and must not guess at.

### Open, in order

1. **The console page.** The IPSC listener holds peers and calls and nothing
   reads them, so a repeater is visible in `/healthz` and the journal but not
   on the dashboard. `server.PeerSource` is one interface with three methods
   and `app.go` passes exactly one implementation. See §8h for the shape of it.
2. **Confirm the learned colour code on air.** KD9EJA's repeater may simply
   share `ipsc.colour_code`, in which case the mirroring is untested and the
   right answer arrived for the wrong reason. The journal line is
   `learned a peer's colour code`. A third repeater on a different colour code
   is the real test.
3. **Access control**, which is layer 2 and still the oldest missing thing in
   §0's table.
4. **Subscription on air**, then **P25**, unchanged.

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
