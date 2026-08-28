# a commercial DMR server parity, and what QSP intends beyond it

**Status:** reference. Not a roadmap — BLUEPRINT-v1 and PROJECT_MEMORY §8 own
the order of work. This document answers a different question: *what does
a commercial DMR server do, what does QSP do, and where is the line today?*

a commercial DMR server is the thing QSP is an alternative to, so "are we there yet" should be
answerable from a table rather than from memory or opinion. It is the floor.
The last section is the part that is not about a commercial DMR server at all.

---

## 1. a commercial DMR server's model, in a commercial DMR server's own words

The architecture is worth stating in its native vocabulary before mapping it,
because operators arriving from a a commercial DMR server network will use these words and
QSP's documentation should be findable by them.

**Managers.** A connection to one repeater or peer. Crucially, each DMR
timeslot on a connected repeater is treated as an independent routing
interface, so one physical repeater presents two Managers.

**Bridge Groups.** A mapping from a talkgroup ID to a specific Manager and
timeslot. This is the unit that says "TG 3148 lives on this repeater's TS1".

**Super Groups.** The virtual switchboard. A frame arriving at any Bridge Group
that belongs to a Super Group is replicated to every other Bridge Group in it.
This is where the actual fan-out decision is made.

**Conference Connections.** Trunking between a commercial DMR servers — hub-and-spoke or mesh
— so that two operators' networks interconnect.

**Static and dynamic routing.** A static route replicates unconditionally. A
dynamic route lies dormant until a voice header arrives on its talkgroup and
timeslot, then adds the requesting Manager to the distribution list and holds it
there under an inactivity timer, typically ten to fifteen minutes, reset on
every local transmission.

**Hold-off.** Priority logic that stops one talkgroup preempting another
already in progress on the same timeslot.

**CallWatch.** Live telemetry: active source ID, target talkgroup, slot, loss
and jitter, dynamic timer state, and per-Manager connection status.

---

## 2. Vocabulary

QSP does not adopt a commercial DMR server's nouns, because twenty decision records and a
working codebase already use its own and renaming them would cost more than it
explains. The map matters more than the names:

| a commercial DMR server | QSP | Note |
|---|---|---|
| Manager | a peer, plus a timeslot | QSP's `Endpoint` carries peer, talkgroup and timeslot together |
| Bridge Group | an `Endpoint` in a bridge | the same mapping, expressed inline |
| Super Group | a bridge | QSP's bridges hold N endpoints and fan out between them |
| Conference Connection | an upstream link, or outbound peer mode | OpenBridge covers part of this; see §4 |
| Static route | a bridge with `enabled: true` and no schedule or trigger | |
| Dynamic route | a PTT trigger with a hang time | ADR-0016 |
| Hold-off | contention | ADR-0014 |
| CallWatch | the event bus and console | server-sent events, no dependency |

**One difference is not a naming difference.** In a commercial DMR server, peers on the same
master hear each other because a Super Group says so. In QSP they hear each
other because a master repeats, which needs no configuration at all; bridges are
additional and move traffic *between* talkgroups. That is ADR-0019, and it is
the more correct model. It is how HBlink and every homebrew master behaves,
and it means a club with four hotspots on one talkgroup writes no routing
rules whatsoever.

---

## 3. Where QSP stands against a commercial DMR server

| Capability | QSP layer | State |
|---|---|---|
| Timeslots as independent interfaces | 1 | **built** — timeslot is part of every endpoint |
| Peers on one talkgroup hear each other | 1 | **built** — ADR-0019 |
| Talkgroup-to-talkgroup mapping | 4 | **built** — bridges with translation |
| Fan-out to many destinations | 4 | **built** — tested at a hundred peers |
| Static, always-on routes | 4 | **built** |
| Dynamic PTT activation with an inactivity timer | 4 | **built** — ADR-0016 |
| Scheduled activation | 4 | **built** — ADR-0015; a commercial DMR server has this, most free tools do not |
| Hold-off between competing transmissions | 4 | **built** — ADR-0014, per talkgroup per slot |
| Frames relayed without transcoding | — | **built** — source, target and sync preserved verbatim |
| Live call telemetry | — | **built** — console and event bus |
| Which talkgroups a repeater may use | 2 | **built** — checked on arrival and per destination |
| Which repeaters may register | 2 | **built** — checked at login, before the password |
| Per-peer talkgroup attachment | 3 | **built** — static, and dynamic by transmitting |
| Trunking to another bridge | 4, 5 | **partial** — OpenBridge links outward; QSP cannot yet dial out |
| Motorola repeater support | — | **missing** — IPSC; see §4 |
| Administration without a text editor | — | **missing** |
| Private calls, radio to radio | 1 | **built** — routed to the radio's peer |
| Text messaging, GPS, data | — | **missing** — ADR-0021 |

QSP is ahead of a commercial DMR server on scheduling, on being free and self-hosted, and on
the repeat model, and level with it on per-peer attachment. It is behind on
being administrable by anyone who is not comfortable with SSH.

---

## 4. Beyond a commercial DMR server

This is the part of the project a commercial DMR server does not define, and where "a complete
DMR suite for the amateur radio community" means more than parity.

### Outbound peer mode

QSP can accept a connection. It cannot make one. That single gap is what stands
between QSP and XLX, DMR+, IPSC2, and any other QSP — every interconnection
where the far end expects to be dialled rather than to dial. It is layer 5, it
is the honest equivalent of a Conference Connection, and it is the largest
single missing capability by reach.

*Decided but not built.* [ADR-0024](adr/ADR-0024-outbound-peer-mode.md) settles
the configuration, which is accepted now so the admin interface can be built
against it; the protocol follows. It must not be pointed at BrandMeister, whose
operators define peer bridging as prohibited — see
[ADR-0018](adr/ADR-0018-openbridge.md).

### IPSC

Homebrew covers MMDVM. **A club running MMDVM boards on its repeaters can
connect to QSP today** — that is what `internal/protocol/hbp` is, and it has
been validated against real hardware. What Homebrew does not cover is a
Motorola repeater speaking IPSC: XPR8300, XPR8400, SLR7500, MTR3000, the
equipment a great many clubs actually own.

IPSC is what makes QSP a drop-in for those clubs rather than a reason to replace
their repeaters. The remaining obstacle is a capture: a registration sequence
from a cold power-cycle, ten minutes of steady state, and one transmission.
ADR-0008 records the licence provenance question and is open rather than
blocking.

### Private calls, and data

Both are wanted, both are missing, and they turn out to need the same thing.
[ADR-0021](adr/ADR-0021-private-calls-and-data.md) records why.

**Private calls are built.** Layer 1 work that was missed in the same way
repeat was missed, not a feature sitting above the model. A call whose target
is a radio resolves to the one peer that radio is behind. Untested on hardware.

**Data is two jobs of very different size.** A text message to a talkgroup is a
group call carrying data bursts, and may already traverse the repeat path
unchanged — a hardware question, not a design one. A text message to another
radio is a private call and needs everything above. IP over DMR and CAI are a
third thing again, because they mean interpreting a field no capture has
established.

Both rest on **subscriber location**: which peer a radio was last heard through.
It cannot be configured, because it changes when somebody drives to work, so it
is learned from traffic and aged out.

### Further out

**P25.** `internal/protocol/p25` exists with a health check standing by, and the
capture in `testdata/p25/` holds polling traffic only. Not the current focus;
the point is that there is somewhere for it to land.

**Analog and other digital modes.** AllStar, EchoLink, Zello and a vocoder all
have registered health checks and no implementations, which is deliberate: an
absent capability that says so is better than one that is silently missing.

---

## 5. What this document is not

It is not a commitment to build everything above, and it is not the order of
work. The order lives in PROJECT_MEMORY §8, and the ordering lesson lives in
ADR-0019: build downward before upward. Reaching for outbound peer mode before
access control exists would repeat exactly the mistake that ADR-0019 records —
not because the work would be wrong, but because the sequence would be.
