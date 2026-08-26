# QSP — Blueprint v1

**Status: current. This document describes what is being built now.**

[`BLUEPRINT.md`](BLUEPRINT.md) is frozen at v0.4 and records what was believed
before any code existed. It is kept as a record and is not a description of the
build. Where the two disagree, this one is correct.

> **This draft was written by Claude from conversation and needs Mike's review.**
> Sections marked **[ASSUMPTION]** are inferred, not stated, and are the most
> likely places to be wrong.

---

## 1. What QSP is, now

**A private DMR network for a radio club's hotspots.**

Fifty to a hundred hotspots register with one QSP instance. Members talk to each
other on club talkgroups without routing through BrandMeister or any commercial
network. The club owns its own infrastructure.

That is the whole of the near-term target. It is not the whole ambition —
§7 sketches where this goes — but it is what the next phases build and what the
gates measure.

## 2. Why this exists

The commercial option for this is a commercial DMR server. Its moat is not capability, it is
that networks have already paid the setup cost and will not pay it twice. A free
alternative competes on the twenty minutes a club officer will spend before
concluding it does not work.

**The evidence for what that means is this project's own history.** Getting one
hotspot to reach QSP took two sessions across two days, and QSP was never at
fault. The obstacles were `Enabled=0` in `/etc/dmrgateway` while the WPSD
dashboard reported the network as enabled, and the talkgroup rewrite that meant
the number dialled was not the number that arrived. A stranger hitting either of
those concludes the software is broken.

That is the problem to solve. The protocol work is done and validated; the
remaining risk is entirely in onboarding.

## 3. Who uses it

Two audiences with almost nothing in common. Confusing them is what made the
earlier console proposal aim at the wrong target.

**The club admin.** Stands the server up once, configures talkgroups and
bridges, and thereafter wants to know it is healthy. Technical enough to run a
Pi. Does this a handful of times a year.

**The club member.** Owns a hotspot, wants to be on the club network, and will
follow instructions but will not debug DMRGateway. There are fifty to a hundred
of them, and they onboard themselves or the admin spends a hundred evenings on
the phone. **[ASSUMPTION]** They run WPSD or Pi-Star; other hotspot software is
out of scope for now.

Member onboarding is therefore the highest-value work available, and it is
distinct from the setup wizard the frozen blueprint described.

## 4. What fifty to a hundred hotspots changes

Every previous test had one peer. Three assumptions do not survive the jump:

**The console cannot show a flat table.** A hundred rows with no search, sort or
filter is unusable. The `Connected peers` panel needs to answer "is *my* hotspot
connected" and "what is talking right now", not list everything.

**Forwarding is a different engineering problem.** One member keying up, relayed
to a hundred peers, is **1,650 datagrams per second outbound** — measured, not
estimated, in `internal/peers/fanout_test.go`. Routing 242 live frames to 99
destinations costs about 10 ms of CPU, so the routing core is not the
constraint. The constraint is the UDP send path, which that test does not
exercise. **[ASSUMPTION]** the target host is a Pi 4 or better.

**Peer identity matters.** With one peer, a radio ID is a curiosity. With a
hundred, the admin needs to know which callsign belongs to which member, who is
allowed on, and how to remove someone. Nothing addresses that today.

## 5. Where the build actually is

| | |
|---|---|
| Protocol (HBP) | **done and hardware-validated** — 556 live frames decoded, 0 dropped |
| Peer lifecycle, routing, scheduler, PTT triggers | built, tested, unproven at scale |
| Console | read-only, four panels, functional and plain |
| Configuration | hand-edited JSON. No write path, no wizard |
| Authentication | **none.** Every endpoint is unauthenticated |
| Persistence | driver registered, schema migrates, **nothing writes to it** |
| Unattended operation | untested; the fourteen-day soak has not started |

## 6. Phases, restated for this target

Supersedes the frozen §16. Each gate is a claim about the world, not about the
test suite.

| Phase | Delivers | Gate |
|---|---|---|
| **1** | HBP master core | ~~A hotspot keys up and its transmission decodes~~ **CLOSED 2026-08-25** |
| **3** | Scheduler proven | A scheduled bridge opens and closes unattended for fourteen days |
| **2a** | **Member onboarding** | A club member with a hotspot joins the network unassisted in under ten minutes |
| **2b** | Admin setup | A club officer stands up a new instance without hand-editing JSON |
| **2c** | Console at scale | An admin finds one member among a hundred connected peers in seconds |
| **4** | Multi-peer forwarding | Audio relays between two *physical* hotspots. Synthetic peers already prove the logic; this proves the wire |

Phase 3 runs first because fourteen days of wall-clock cannot be compressed, and
it needs no further code.

**2a before 2b** reverses the frozen blueprint. Setup happens once; onboarding
happens a hundred times. **[ASSUMPTION]** the admin — you — can keep
hand-editing JSON in the interim.

**Phase 4 is narrower than it looked.** Relay between two peers over real
sockets has been tested since before the fan-out work — `forward_test.go` covers
talkgroup and timeslot translation, whole transmissions, and that a bridge
gated by schedule or PTT carries the opening frame. What was missing was scale
and provenance, and both are now covered: a captured transmission survives the
wire intact, and fan-out holds at a hundred peers.

What remains is hardware. Two physical hotspots have never been connected to one
instance, and no radio has received relayed audio.

## 7. Beyond this

P25, the vocoder pool, and the AllStar, Zello and EchoLink connectors remain the
direction. Each registers a health check naming the phase that brings it, so the
running instance always states what it does not yet do.

The full a commercial DMR server alternative is the destination. A club network that works is
the step that proves it is worth building.

## 8. Non-goals, for now

- Hotspot software other than WPSD or Pi-Star **[ASSUMPTION]**
- Federation between QSP instances
- Public internet exposure. Deployment assumes a LAN or a tunnel; there is no
  authentication and `/api/peers` discloses callsigns, radio IDs and addresses
- A mobile app. The console must work on a phone browser; that is different
- Migration tooling from a commercial DMR server

## 9. Open decisions

1. Is there a second admin, or is one shared credential honest for v1?
2. Does the club vet who joins, or does anyone with the password get on? This
   decides whether Phase 2a needs approval workflow or just instructions.
3. Which talkgroups does the club actually want, and do they bridge to anything
   outside?
4. Is there a member who has never seen this, who would test Phase 2a's gate?
   It cannot be self-certified.
