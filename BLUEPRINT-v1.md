# QSP — Blueprint v1

**Status: current. This document describes what is being built and why.**

[`BLUEPRINT.md`](BLUEPRINT.md) is frozen at v0.4 and records what was believed
before any code existed. Where the two disagree, this one is correct.

---

## 0. The rule that governs everything below

**QSP is being built for the amateur radio community, not for one club.**

Every value specific to a network — talkgroup numbers, upstream masters,
repeater IDs, passwords, timezones, callsigns — is **administrator
configuration**. None of it is hardcoded, assumed, or asked for at design time.

A club in Toulouse points its upstream at `2081.master.brandmeister.network`;
a club in Texas at `3102`. QSP neither knows nor cares which. When a design
question sounds like "which talkgroup does the club want?", the correct answer
is almost always "that is a field, not a decision" — and the real question is
what shape the field takes.

K9MLS's own club network is the **test bed**, not the specification. Build the
mechanism for everyone; validate it against the equipment in the room.

---

## 1. What QSP is

**A DMR call-routing server for amateur radio: a free, open alternative to
a commercial DMR server.**

The near-term deliverable is a private club network for hotspots — fifty to a
hundred of them — that can also link outward to the wider DMR world. The
long-term deliverable is the full suite: repeaters over IPSC, P25, analog
connectors, and the console that makes all of it operable by a club officer
rather than a specialist.

## 2. Why this exists

a commercial DMR server is the incumbent and it is commercial. Its moat is not capability, it
is that networks have already paid the setup cost and will not pay it twice.

A free alternative competes on the twenty minutes a club officer will spend
before concluding it does not work. **The evidence for what that means is this
project's own history.** Connecting one hotspot took two sessions across two
days, and QSP was never at fault. The obstacles were `Enabled=0` in
`/etc/dmrgateway` while the WPSD dashboard reported the network as enabled, and
a talkgroup rewrite that meant the number dialled was not the number that
arrived. A stranger hitting either concludes the software is broken.

## 3. How QSP relates to the existing networks

Understanding this stopped a wrong turn, so it is recorded rather than
rediscovered.

### QSP's routing model is a commercial DMR server's, not BrandMeister's

The a commercial DMR server manages talkgroups on an **always-on, scheduled, or on-demand
(PTT)** basis. That is exactly QSP's `enabled`, `schedule` and `triggers` — the
alignment is complete, and it was built before anyone checked. A a commercial DMR server is a
point-to-multipoint router, similar to VLAN trunking, with talkgroups as the
control points carrying traffic, routing and timers that hold off other traffic
on a timeslot. That is ADR-0013's pure routing decision plus ADR-0014's
contention.

### BrandMeister works differently, and that difference is not a defect

BrandMeister is subscription-centric: talkgroups exist implicitly, peers attach
to them, and the network creates any talkgroup ID on demand. Attachment comes in
three kinds — **static** (set per-peer, permanent), **dynamic** (created by
transmitting, times out after ~15 minutes without local traffic), and
**auto-static** (hotspot-only; persists until the user keys a different
talkgroup; TG 4000 clears everything).

**QSP follows the a commercial DMR server model:** the administrator sets the static
talkgroups; users select among them by programming their subscriber radios.

**Known gap.** QSP's PTT trigger opens a bridge **network-wide**. On
BrandMeister a dynamic talkgroup attaches to *one hotspot*. For a club of a
hundred, one member keying up should not open a talkgroup for everybody. Making
attachment per-peer is real work and is not yet scheduled.

## 4. Linking outward

### BrandMeister: OpenBridge only

**Settled, and not a matter of preference.** BrandMeister's documentation
requires the OpenBridge protocol for interconnecting another network, forbids
peer bridging via MMDVM or Homebrew, and explicitly asks people not to build
software that impersonates those protocols without an onboard radio.

QSP logging into a BM master as though it were a hotspot is precisely what they
have asked nobody to build. It would work today and break on a version bump,
unsupported.

**OpenBridge is small.** It is a simple protocol based on MMDVM carrying DMRD
packets only, with no connection establishment and no keep-alive — a shared
passphrase, a network ID, and frames on UDP. QSP's HBP codec already parses
DMRD. Approval is the hard part, not the code: bridges are granted at each
master's discretion.

**Configuration shape:** named upstream blocks, each with target address, port,
network ID, passphrase, and the talkgroups carried in each direction. Multiple
blocks — a club may bridge to BrandMeister *and* to a neighbouring QSP.

### IPSC: for real repeaters

Motorola XPR8300, XPR8400, SLR7500 and MTR3000 are what club sites actually run,
and they speak IPSC. **This is what makes QSP a a commercial DMR server alternative rather than
a hotspot server.**

Both directions are needed, because both exist in the wild: QSP as **IPSC
master**, with repeaters registering to it, and QSP as **IPSC peer**, joining an
existing IPSC system. `ipsc2hbp` supports both for that reason.

**Unblocked as of 2026-08-27.** [ADR-0008](docs/adr/ADR-0008-protocol-licensing.md)
is amended rather than rewritten. Two findings settled it.

There is **no published IPSC specification** — Motorola has not released one, so
the "protocol documents" route that HBP used does not exist. But the interim
rules already permit implementing from **captured traffic**, and the club runs
the exact repeaters IPSC is wanted for. That is the preferred route and it
needed no amendment.

And **DMRlink is GPL-3.0, not CC BY-SA 3.0** as ADR-0008 had recorded; its
source files carry a GPL v3-or-later header. Reading it where captures are
insufficient is therefore permitted into a GPL-3.0 project, at the cost of
attribution and a derivative-work notice that cannot be undone later. That is
now allowed explicitly and narrowly.

The CC BY-NC-SA question remains open. IPSC does not touch it.

## 5. Who uses it

**The network administrator.** Stands the server up, defines talkgroups and
bridges, links upstream, and thereafter wants to know it is healthy. Technical
enough to run a server. Does this a handful of times a year.

**The club member.** Owns a hotspot, wants on the network, will follow
instructions but will not debug DMRGateway. There are fifty to a hundred of
them, and they onboard themselves or the admin spends a hundred evenings on the
phone. Assumed to run WPSD or Pi-Star.

**The repeater trustee.** Has a Motorola repeater and an IPSC configuration.
Blocked until IPSC lands.

## 6. Deployment

**The target is a server or VM, not a Raspberry Pi.** a commercial DMR server is server
software and that is the right precedent. A club network with upstream links,
persistence and a hundred peers deserves more than a Pi, and a 4-vCPU VM is the
honest deployment target.

The Pi remains the *minimum*: armv7 cross-compilation is proven and stays in CI,
because a small club running one hotspot should not need a server.

Both paths exist already — `deploy/docker/` (Dockerfile and compose, console
bound to localhost, data volume) and `deploy/systemd/qsp.service` (hardened
unit). Ubuntu installs from either.

## 7. What fifty to a hundred hotspots changes

**The console cannot show a flat table.** A hundred rows with no search is
unusable. The peers panel must answer "is *my* hotspot connected" and "what is
talking now".

**Fan-out is measured, not estimated.** One transmission to 100 peers is
**1,650 deliveries per second of speech**; 242 frames to 99 destinations costs
about 10 ms of CPU (`internal/peers/fanout_test.go`). The routing core is not
the constraint. The UDP send path is untested at that rate.

**Peer identity matters.** With one peer a radio ID is a curiosity. With a
hundred, an admin needs to know which callsign belongs to which member and how
to remove someone. Nothing addresses that today.

## 7a. The layers, and which exist

Recorded after [ADR-0019](docs/adr/ADR-0019-master-repeats.md).

| Layer | What it is | State |
|---|---|---|
| **1. Repeat** | a group call reaches every other peer on the same talkgroup | **built 2026-08-27** |
| **2. Access control** | which talkgroups, which repeaters, which subscribers | missing |
| **3. Subscription** | which peers receive which talkgroups | schedule and triggers exist; per-peer attachment does not |
| **4. Bridging** | connect this master to other systems | built, including OpenBridge |
| **5. Outbound peer** | connect *out* to XLX, DMR+, IPSC2, another QSP | missing |

QSP built layer 4 first and mistook it for the model. Layer 1 — four hotspots on
TG 9 hearing each other — had no configuration until it was built.

**Layer 2 is now the most pressing gap.** With repeat on and no access control,
every peer receives every talkgroup any peer transmits on. That is workable for
a club and is not workable on the open internet.

## 8. Where the build actually is

| | |
|---|---|
| Protocol (HBP) | **done, hardware-validated** — 556 live frames, 0 dropped |
| Repeat within a talkgroup | **done 2026-08-27** — verified over real sockets and at 100 peers |
| Bridged relay between talkgroups | done — verified over real sockets, and at 100 peers |
| Peer lifecycle, routing, scheduler, PTT | built and tested |
| Member onboarding (`/api/join`, `/join`) | **done**, ungated |
| Console | read-only, four panels, functional and plain |
| Persistence | driver registered, schema migrates, **nothing writes to it** |
| Authentication | **none.** Every endpoint is unauthenticated |
| OpenBridge | **code complete**, never run against a real far end |
| IPSC | not started; unblocked, needs a capture |
| Unattended operation | fourteen-day soak not started |
| Two physical hotspots on one instance | never done |

## 9. Phases

Each gate is a claim about the world, not about the test suite.

| Phase | Delivers | Gate |
|---|---|---|
| **1** | HBP master core | ~~a hotspot keys up and its transmission decodes~~ **CLOSED 2026-08-25** |
| **3** | Scheduler proven | a scheduled bridge opens and closes unattended for fourteen days |
| **2a** | Member onboarding | a club member joins unassisted in under ten minutes |
| **4** | OpenBridge upstream | ~~code complete 2026-08-27~~ — **gate open**: a talkgroup carries traffic to and from a real far end, which needs an approved bridge |
| **2b** | Admin setup | a club officer stands up an instance without hand-editing JSON |
| **5** | IPSC | a Motorola repeater registers to QSP and passes audio |
| **2c** | Console at scale | an admin finds one member among a hundred peers in seconds |
| **6** | P25, vocoder, analog connectors | as the frozen blueprint describes |

Phase 3 runs first: fourteen days of wall-clock cannot be compressed and it
needs no further code.

**2a before 2b** reverses the frozen plan. Setup happens once; onboarding
happens a hundred times.

## 10. On the horizon

**A live node map.** Every hotspot already sends `Latitude`, `Longitude`,
`Height` and `Location` in its `RPTC` login — `internal/protocol/hbp/config.go`
parses them and QSP currently discards them. Storing and drawing them is
genuinely close. The obstacle is that a map library is a build-step-and-CDN
dependency, which cuts against the console's no-build-step rule; self-hosting is
possible and needs an ADR.

## 11. Decided

- **No membership vetting in v1.** HBP uses one shared secret per network, a
  club knows its own members, and an approval workflow needs admin sessions that
  do not exist. The peer registry already records who connected and when, so
  vetting is additive later.
- **The join page**: five steps, ten minutes promised, no credential shown.

## 12. Non-goals, for now

- Hotspot software other than WPSD or Pi-Star
- Public internet exposure without a proxy: there is no authentication, and
  `/api/peers` discloses callsigns, radio IDs and addresses
- A mobile app. The console must work in a phone browser; that is different
- Migration tooling from a commercial DMR server

## 13. Still open

1. Whether a second administrator exists, which decides whether roles and
   sessions are needed before the config write path.
2. Whether per-peer dynamic talkgroup attachment is built, or the network-wide
   PTT trigger is enough for a club.
3. Whether the console's map dependency is worth a build step.
