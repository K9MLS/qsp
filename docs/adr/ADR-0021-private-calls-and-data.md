# ADR-0021: Private calls and data are in scope, and share one missing thing

**Status:** Proposed

## Context

QSP carries group voice. It carries nothing else, and two of the gaps are
things amateur operators use constantly rather than curiosities.

**Private calls.** `routing.Core` repeats only when `CallType == CallGroup`, and
nothing routes a private call at all. A bridge could in principle name a radio
ID where a talkgroup goes, but nobody configures that and it is not what a
private call means. Radio-to-radio calling simply does not work.

**Data calls.** Text messaging, GPS and location reporting, IP over DMR. The
decoder already preserves these faithfully. `hbp.Data` carries `FrameType` and
`DataType`, and `DataType` is exposed raw rather than decoded because the
capture never established its meaning. So the wire is carried; the meaning is
not acted on.

These arrived as separate items on the a commercial DMR server parity list. They are recorded
together because they turn out to need the same missing piece.

## The thing they share

**A private call's target is a radio, and QSP does not know where radios are.**

A group call's destination is a talkgroup, which is a property of the network's
configuration. A private call's destination is a subscriber ID, which is a
property of where somebody happens to be standing. To route one, the master must
know which connected peer that subscriber was last heard through.

Nothing in QSP tracks that today. `Peer` records the repeater ID, the address,
the login state and the announced configuration; it does not record which radios
have been heard through it. That map — subscriber ID to the peer it was last
heard on, with a last-seen time. That map is the prerequisite for private
calls, and it is the same map a radio-to-radio text message needs.

It also cannot be configured. Which radio is on which hotspot changes when
somebody drives to work, so it must be learned from traffic and aged out, in the
way every DMR network does it.

## Decision

**Both are in scope, and neither is built here.** This record exists so the
prerequisite is visible before either is designed, and so the layer model stops
implying that a destination is always a talkgroup.

Three things follow.

**1. Subscriber location is a new piece of state, learned from traffic.** A map
from subscriber ID to the peer it was last heard on and when. It is written on
every accepted frame and aged out on a timeout. It belongs beside the peer
registry, which already owns per-peer state and is already single-writer under
ADR-0002.

*Built 2026-08-28.* `Master.Locate` answers the routing question and
`Master.Locations` the operator one; they differ deliberately, since a radio
whose peer has left is still worth showing and is not somewhere a call can be
sent. The observation happens after the subscriber access check rather than
before, which is how one list ends up governing both transmission and
reachability. `dmr.subscriber_timeout` defaults to two hours: a working shift,
and a guess informed by nothing but plausibility, which is why it is a field
rather than a constant.

*Built 2026-08-28.* A private call resolves the called radio to the one peer it
is behind and is delivered there and nowhere else. The called radio's ID stays
in the target field, since that is what opens the receiving radio's squelch;
only the timeslot comes from where the radio was heard, because a peer's two
slots are independent paths. An instance with no subscriber lookup refuses
private calls with an explanation and keeps routing group calls, which is a
working configuration rather than a fault.

**2. The layer model gains no layer.** Private calls are not a sixth layer;
they are the same question — *who else should hear this?* — with a different
kind of answer. Repeat resolves a talkgroup to every other peer on it; a private
call resolves a subscriber to the one peer it is behind. Both are layer 1.
Calling it a new layer would suggest it could be built after layer 4, which is
the mistake ADR-0019 records.

**3. Data splits into two jobs of very different size**, and they should not be
estimated together.

A text message to a talkgroup is a **group** call carrying data bursts. It may
already traverse the repeat path unchanged, since repeat tests the call type and
not the frame type. That is a hardware question rather than a design one, and
the experiment is two minutes with two radios.

A text message to another radio is a **private** call and needs everything
above.

Anything richer — IP over DMR, CAI, confirmed delivery with retries — is a
separate question again, because it involves interpreting `DataType`, which no
capture has established. It is named here so it is not mistaken for part of the
same work.

## What this costs

**The contention model needs revisiting, not just extending.** ADR-0014
reserves a destination endpoint so two transmissions cannot interleave. A
private call's destination is a peer and a subscriber rather than a peer and a
talkgroup, so what exactly is reserved has to be decided rather than assumed.
Reserving the whole timeslot is probably right and is not obviously right.

*Settled by [ADR-0022](ADR-0022-timeslot-contention.md).* Asking the question
exposed a group-call bug: two talkgroups could be delivered to one peer's
timeslot at once. Keying reservations on the slot fixes that and makes group and
private calls contend identically, with no special case for either — a better
answer than the one that would have been invented for private calls alone.

**Access control gains a second reason to exist.** ADR-0020's subscriber list
already names radio IDs. Private call routing names radio IDs too, and the same
list should govern both. A subscriber refused permission to transmit should not
be reachable as a private call destination either. Building the subscriber
handling once, for both, is why this record comes before either is written and
not after.

**A learned map is a disclosure surface.** Subscriber location is exactly the
information "which repeater is this operator near", and QSP currently
authenticates nobody. It must not reach an unauthenticated endpoint. That is a
constraint on the admin interface rather than on this work, and it is recorded
here because this is where the data starts existing.

## Consequences

- Private call routing is layer 1 work that was missed, in the same way repeat
  was missed. The parity document and PROJECT_MEMORY should say so plainly
  rather than listing it under data.
- Group data may already work. If the hardware test shows it does, that is worth
  stating as an existing capability rather than a planned one.
- Neither is next. Access control is unenforced, and an instance that routes
  private calls between strangers before it can refuse a station is the wrong
  order again.
