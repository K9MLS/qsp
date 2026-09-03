# ADR-0044: Access control covers IP Site Connect, with no new configuration

**Status:** Accepted
**Date:** 2026-09-03
**Extends:** [ADR-0020](ADR-0020-access-control.md), which stands

## Context

ADR-0020 built four access lists and checked them in two places. It was written
before IP Site Connect carried any audio at all, so it is not wrong about IPSC —
it is silent. IPSC now carries audio from three repeaters, two of which the
operator does not own.

Reading the code rather than the summary, the position was better than expected
and worse in one place:

| Check | Homebrew peers | IPSC repeaters |
|---|---|---|
| Registration | `dmr.access.registration` | `ipsc.allowed_peers` — covered |
| Talkgroup | routing ingress | routing ingress — covered |
| Subscriber | the master's data path | **not checked** |

Talkgroups were already covered because `DeliverFromIPSC` routes through
`routing.Core`, which tests the origin talkgroup, and `sendToIPSC` returns when
routing set a reason — so the repeater-to-repeater path is covered too.
Registration is covered because `ipsc.allowed_peers` already is a registration
list under another name.

## Decision

**The same lists apply to both protocols. There is no IPSC access
configuration.**

The subscriber check now runs in `DeliverFromIPSC`, using the same
`access.Lists` and producing the same refusal wording as the Homebrew path.

### Why a subscriber list must cross protocols

**It bans a radio, not a repeater.** Without this, a banned operator was refused
on a hotspot and carried by a Motorola repeater — the same person, the same
network, two answers, decided by which door they walked through. That is not an
incomplete feature; it is an access control system that can be walked around by
keying a different radio.

### Why there is no fourth list and no per-repeater policy

An IPSC repeater announces no callsign, no location and no talkgroups, so there
is less to describe, not more. Everything the checks need — the subscriber ID
and the destination talkgroup — is in every frame, and by the time a burst
reaches `DeliverFromIPSC` it is an `hbp.Data` with the same fields the Homebrew
path uses. One network, one policy, and an operator who bans a radio does not
have to remember to ban it twice.

**Inbound is not the codeplug's business.** ADR-0043 says QSP has authority over
delivery and none over transmission, and that is about frames leaving. What
enters the network is entirely QSP's decision, so a talkgroup check on arriving
IPSC audio duplicates nothing.

## Consequences

**Fixed on the way: two of the four lists were saved and did nothing.** The
console edits all four and posts the whole configuration back. Talkgroup lists
reached the routing core through `SetAccess` on reload; the registration and
subscriber lists were read when the master was constructed and never again, and
`NeedsRestart` named neither. **An operator banning a radio got a successful
save, no restart warning, and a ban that was not in force** — the same failure
already recorded three lines into `NeedsRestart` for parrot, found the same way.
`Master.SetAccess` now applies them, and the reason they are absent from
`NeedsRestart` is written there rather than left to be rediscovered.

**Accepted: a refused transmission is logged once, not per frame.** Sixty frames
a second of one radio would be a journal nobody reads. Constitution §18 still
holds — the transmission is named, with the subscriber, talkgroup and timeslot.

**Accepted: the refusal happens after the console has seen it.** An operator
looking at the dashboard to find out who is transmitting is better served by
seeing the station being refused than by it vanishing, which is the same order
the Homebrew path uses.

**Not changed: `ipsc.allowed_peers` stays.** It is the registration list for
that protocol and renaming it would break every deployment to gain a word.
