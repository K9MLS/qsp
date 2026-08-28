# ADR-0023: Peers attach talkgroups, and mostly attach them by talking

**Status:** Proposed

## Context

Layer 3 in ADR-0019's model is *which peers receive which talkgroups*, and QSP
answers it with "all of them". Repeat resolves a group call to every ready peer,
so a member hears every talkgroup anybody on the network transmits on.

For four hotspots in one club that is exactly right and needs no configuration.
It stops being right the moment a network carries more than one conversation: a
member sitting on their local talkgroup does not want a statewide net arriving
on the same hotspot, and on a simplex hotspot with one timeslot they cannot have
both anyway.

ADR-0019 named two established answers and did not choose between them.

**a commercial DMR server** has the administrator define talkgroups centrally, each
always-on, scheduled, or activated by PTT. **QSP already has this**: it is what
bridges, `dmr.schedule` and `dmr.triggers` are, and ADR-0019 records that the
work is correct. Nothing here replaces it.

**BrandMeister** attaches talkgroups per peer. A talkgroup can be *static* —
configured and always delivered — or *dynamic*, created by the member
transmitting on it and timed out after a period of silence.

## Decision

**Build the BrandMeister model, because it is the half QSP does not have, and
because the dynamic part is the half that matters.**

A per-peer static list alone would be unusable. A member who wants to work a
talkgroup for ten minutes would have to ask an administrator to edit a file
and restart, and the administrator would accumulate a list entry per member
per talkgroup forever.

**A peer attaches a talkgroup by transmitting on it.** The attachment lasts a
configurable time after the last activity and then lapses. That is what a
hotspot operator already expects, because it is what every network they have
used does, and it needs no administrator at all.

Static attachments remain available for the cases dynamics cannot serve: a
club's calling channel, which must be there before anybody speaks, and a
repeater that should always carry its regional talkgroup.

### Both models coexist, and neither overrides the other

A peer receives a talkgroup if **any** of these is true: it is statically
attached, it is dynamically attached, or a bridge carries that talkgroup to it.

The union rather than a precedence order, for the same reason ADR-0016 merged
the schedule and PTT triggers with a logical OR: an operator who has said a
talkgroup should arrive by two different routes wants it to arrive, and a
precedence rule would silently disable one of the two things they configured.

### The default attaches everything

**With no subscription configured, every peer receives every talkgroup**,
which is what QSP does today. A club running on one talkgroup writes nothing
and notices no change.

Subscription switches on per instance rather than per peer. Turning it on
without configuring anything would leave a network where nobody hears anything
until somebody transmits, which is a surprising thing to happen on upgrade. So
the absence of the setting means "off", and off means "everything", exactly as
now.

## What this is not

**It is not access control.** ADR-0020's lists decide what an instance is
*willing* to carry; this decides what a member *wants* to hear. A talkgroup
refused by the access list is refused whatever anybody subscribes to, and the
access check runs first. Conflating them would let a member subscribe their way
past a refusal.

**It is not auto-static.** BrandMeister promotes a peer's first non-static
dynamic talkgroup to something longer-lived. It is a refinement on dynamic
attachment and can be added later without changing anything here; adding it now
would mean three lifetimes to reason about before the first one has run on real
hardware.

## Consequences

- **Dynamic attachment is learned state, like subscriber location.** It belongs
  beside the peer registry for the same reasons: it is written on every accepted
  frame, aged out on a timeout, and owned by the single writer of ADR-0002.
  ADR-0021 built the same shape for radios; this is the same shape for
  talkgroups.
- **The first frame of a transmission must attach and be delivered.** The
  obvious implementation observes the frame, attaches the talkgroup, and lets
  the *next* one through — clipping the first syllable of every transmission
  onto a newly-attached talkgroup. That is the mistake ADR-0016 records making
  with PTT triggers, and the fix there is the fix here: attach before routing,
  in the same pass.
- **A peer attaches by transmitting, which means the transmitter always hears
  the replies.** Anybody answering them is on the same talkgroup, and the
  attachment is what carries the answer back.
- Contention is unaffected. An unsubscribed peer is not a destination, so it
  takes no reservation and cannot block anybody — which is the same rule
  ADR-0022 applies to a destination refused by an access list.
- **The console will need to show attachments.** A member asking why they cannot
  hear a talkgroup is the most common question on any DMR network, and the
  answer is a list QSP holds and does not currently display.
