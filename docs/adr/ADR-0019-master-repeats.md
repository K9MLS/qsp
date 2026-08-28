# ADR-0019: A master repeats; bridging is a layer on top

**Status:** Accepted
**Date:** 2026-08-27

## The mistake this records

QSP was built with **bridging as its routing model**. A bridge joins talkgroup A
to talkgroup B, and traffic moves between them. Everything — `routing.Table`,
`routing.Core`, the scheduler, PTT triggers, contention, the OpenBridge work —
sits on that.

**It has no way to express the thing a DMR network does.** Four hotspots on
TG 9, all hearing each other, has no configuration in QSP. Two endpoints on the
same talkgroup are rejected as a duplicate; a single endpoint is rejected for
being one endpoint; and naming every peer explicitly would mean editing the
configuration each time a member joins.

That is not a missing feature. It is the primary function of a DMR master, and
it was never built, because the bridging layer was built first and mistaken for
the whole.

## What the established implementations actually do

From HBlink's own configuration comments, which are unambiguous:

> `# Repeat - if True, the master repeats traffic to peers, False, it does
> nothing.`

That single flag is the core. **A master receives a frame from one peer and
repeats it to every other peer connected to it**, on the same talkgroup and
timeslot. Every published HBlink configuration sets `REPEAT: True`, because a
master with it off is inert.

Bridging is separate and sits above. In HBlink it lives in a different file
entirely — `rules.py` — and connects *systems*: master to master, master to
OpenBridge, master to an outbound peer. Within one master, peers hear each other
because the master repeats, not because a rule says so.

This is also why HBlink treats all peers on a master as one entity in a bridge
rule: "If you have 10 PEERS connected to a single MASTER, each of those PEERS
will behave the same. You can only define 1 rule for each MASTER in a
talkgroup."

## The layers, correctly ordered

**1. Repeat.** A group call arrives on TG X, TS Y from peer A. It is sent to
every other peer on this master. This is the product. Nothing else matters if
this does not work.

**2. Access control.** HBlink has `TGID_TS1_ACL`, `TGID_TS2_ACL`, `REG_ACL` and
`SUB_ACL` — which talkgroups are permitted per slot, which repeater IDs may
register, which subscriber IDs may transmit. QSP has none of these. Without
them, a master repeats everything to everyone, which is workable for a club and
not workable at scale.

**3. Talkgroup subscription.** Which peers receive which talkgroups. Two models
exist and they are genuinely different. BrandMeister attaches talkgroups
per-peer — static, dynamic and auto-static, with dynamic created by transmitting
and timing out. a commercial DMR server has the administrator define talkgroups centrally, with
always-on, scheduled or PTT activation. **QSP's existing schedule and trigger
work is the a commercial DMR server model and is correct**; it was simply built on top of
nothing.

**4. Bridging.** Connecting this master to other systems: another master, an
OpenBridge link, an outbound peer connection to XLX, DMR+, IPSC2 or another QSP.
This is what QSP built.

**5. Outbound peer mode.** QSP can accept connections; it cannot make one. That
is how HBlink reaches XLX and IPSC2, and it is missing.

## Decision

**Build layer 1, and rebuild the routing decision around it.**

`routing.Core` currently answers "which bridges name this talkgroup?" It must
answer "who else should hear this?", of which bridging is one contributing
source among several. Concretely:

- A group call is repeated to every other configured peer on the same talkgroup
  and timeslot, by default, without a bridge.
- Bridges continue to do what they do now — move traffic *between* talkgroups —
  and are additive to repeat rather than a replacement for it.
- The schedule and PTT triggers keep applying to bridges, which is what they
  were always for.
- Contention (ADR-0014) applies to repeat as it does to bridging: one
  transmission per talkgroup per slot.

**Repeat is on by default**, and configurable off. A master that does not repeat
is a deliberate choice, not an accident of an empty bridge list.

## What this costs

`internal/routing` is the most heavily tested package in the project and this
changes its central question. The existing tests are the regression check: peer
to peer bridging, translation, contention, the fan-out at a hundred peers, and
the captured-frame relay must all still pass.

The work already done is not wasted, but its status changes:

| Built | Was thought to be | Actually is |
|---|---|---|
| Bridging, schedule, triggers | the routing model | layer 4, correct but incomplete without layer 1 |
| OpenBridge | linking to other networks | layer 4, correct |
| Fan-out at 100 peers | proof of scale | proof the *bridging* path scales; repeat is untested |
| Captured-frame relay | proof relay works | proof *bridged* relay works |

**The honest statement is that QSP has never repeated a talkgroup**, and every
test that appeared to prove relaying was proving the bridging path.

## Consequences

- Phase 2a's gate cannot be met until layer 1 exists. Inviting testers before
  then means inviting them to a network where nobody hears anybody.
- The 14-day soak continues to test the scheduler, which is real, but it is
  testing layer 4 on a network with no layer 1.
- Access control (layer 2) becomes the next gap after repeat, and should be an
  ADR of its own before a club network is exposed to the wider internet.
- Outbound peer mode (layer 5) is what reaches XLX, DMR+ and IPSC2, and is the
  natural companion to the IPSC work.

## Why this was not caught earlier

The protocol was validated against real hardware, the routing core was tested
exhaustively, and 368 tests passed — all of the bridging path. The gap was in
the model, not the code, and no test can find a question nobody asked.

The reference implementations were read for their *protocol* and not for their
*architecture*. HBlink's `REPEAT` flag is one line in a sample configuration
file, and it is the whole design.
