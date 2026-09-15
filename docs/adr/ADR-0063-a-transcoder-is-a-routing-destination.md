# ADR-0063: A transcoder is a routing destination, and a third kind of one

**Status:** Accepted
**Relates to:** [ADR-0022](ADR-0022-contention-belongs-to-the-timeslot.md),
[ADR-0061](ADR-0061-qsp-speaks-to-a-transcoder.md),
[ADR-0062](ADR-0062-as-much-as-possible-in-qsp.md),
[ADR-0018](ADR-0018-openbridge.md)

## Context

[ADR-0062](ADR-0062-as-much-as-possible-in-qsp.md) lists four things QSP
builds for the transcoder path. The first two are done and proved on hardware
(PROJECT_MEMORY §8q and §8r): `internal/ambe` speaks to an AMBEserver, brings
the chip to a known state, carries frames both ways and holds one call at a
time. Real DMR audio off the operator's own repeater has been decoded to
speech and listened to.

The third is **talkgroup to channel mapping**: deciding which DMR traffic
reaches the vocoder at all. Nothing today connects the client to the routing
core, and the question this record answers is where that connection belongs.

## Decision

**A vocoder channel is a routing endpoint**, named on a bridge like a peer or
a link, and `routing.Endpoint` gains a `Transcoder` field alongside `Peer` and
`Upstream`.

That is what a talkgroup-to-channel mapping *is*: a bridge joining a talkgroup
on a timeslot to a chip. Expressing it as an endpoint costs no new mechanism
and inherits three that already exist and are tested:

- **Scheduling.** A transcoded link that should only run during a net is a
  bridge whose `Enabled` flips, which is how ADR-0021's schedules and
  PTT triggers already work. No second mechanism.
- **Contention.** The routing core already refuses a destination that is
  busy, names what holds it, and counts the drop.
- **Explaining itself.** `Decision.Reason` already answers "why did this call
  go nowhere", which is the question an operator asks about a transcoder as
  much as about a bridge.

Everything above was available for free. The alternative — a subscription list
hanging off the transcoder, parallel to the routing table — would have
reimplemented all three, and ADR-0052's warning about subscription and
flooding becoming two code paths applies with equal force here: the path that
rots is the one fewer people use.

### It is a third kind, not a variety of the other two

**A transcoder contends like a peer and configures like a link**, and that
combination is why it needs its own field rather than reusing `Upstream`.

[ADR-0022](ADR-0022-contention-belongs-to-the-timeslot.md) drew the line: a
peer destination is reserved by `(peer, timeslot)` because a DMR timeslot is
one TDMA channel and can physically carry one call, while an upstream keeps
the talkgroup in its key because an OpenBridge link is an IP socket and
BrandMeister carries several talkgroups over one.

**One AMBE-3000 is one channel.** BLUEPRINT §7 says a club wanting four
simultaneous transcoded talkgroups needs four chips, and the chip itself
answers one packet at a time — `internal/ambe` serialises its exchanges
because a reply carries nothing identifying which request it answers. So a
transcoder is ADR-0022's *physical constraint* case, and its contention key
drops the talkgroup exactly as a peer's does.

Reusing `Upstream` would have given it an IP socket's key and delivered two
talkgroups to one chip — which is the bug ADR-0022 was written after, in a new
place. That is the whole argument for the third field, and it is a contention
argument rather than a taxonomy one.

**It drops the timeslot too**, which neither of the other kinds does. A
vocoder has no timeslots. The field is carried on a transcoder endpoint only
so that the talkgroup it serves keeps the slot it arrived on, and two
talkgroups reaching one chip on different slots are still two calls for one
channel.

### Configuration names it, and refuses a mapping that cannot work

`dmr.transcoders` is a list of name, address and rate index. A bridge endpoint
names one, and the configuration refuses both halves of the mismatch:

- a bridge naming a transcoder that is not configured or not enabled
- **a transcoder that is enabled and that no bridge routes anything to**

The second matters as much as the first, and the reason is on the record: a
link nothing routed to cost this project an afternoon, during which the socket
opened, the far end authenticated, the health report said no traffic had
arrived, and the advice sent an operator to check somebody else's address —
while no configuration on this side could ever have put a frame on it. A
vocoder that handshakes, reports ready and receives nothing is the identical
failure with a different subsystem name on it.

**The rate index is refused outside Table 115's range rather than clamped.**
The chip accepts an out-of-range index, then produces frames of a width
nothing expects, and the result presents as bad audio rather than as a
configuration error. PROJECT_MEMORY §8q records a run that went out at 2400
bps because a code path skipped the rate packet entirely, and the only
evidence was the width of the frames.

## Consequences we accept

**A mapping exists before anything acts on it.** This record decides where the
mapping lives and validates it; opening the client at startup and feeding it
frames is the next patch. **The health report says so explicitly** rather than
letting a configured transcoder look like a working one — §7's rule about no
stub that claims success, applied to a subsystem that is half wired.

**Identity is not solved here.** ADR-0062's fourth item — a transcoded
transmission appearing in Last heard as a station rather than an anonymous
burst, and a DMR talker's callsign reaching the far side — is a separate
decision with an open question in it: what a Zello username *is* in a system
keyed on DMR radio IDs. It gets its own record.

**The per-repeater permission is part of this decision after all**, added in
0366 before anything delivers. ADR-0062 requires that a repeater owner opt in
before transcoded audio appears on their machine, because a Zello user is not
necessarily licensed and their audio reaches RF.

It lives in the routing table rather than at the delivery point, for the same
reason the mapping does: the decision is configuration, this is the package
that owns configuration decisions, and a caller that had to remember to check
would eventually be a caller that forgot.

`Permission` denies when empty — the only list in this configuration that
does, because every access list governs a network the operator already runs
while this one governs whether somebody else's licence is put at risk. Three
consequences follow:

- **Zero is not a wildcard.** `AnyPeer` is 0 in this package and matches
  everything; carrying that convention here would mean a stray zero silently
  permitted every repeater to carry possibly unlicensed audio. Configuration
  refuses a zero entry rather than ignoring it, because 0 means "every peer"
  everywhere else and somebody will write it here meaning that.
- **Permitting everybody is a separate boolean**, so that it is a sentence
  somebody wrote on purpose.
- **An endpoint naming `AnyPeer` is withheld unless every peer is permitted.**
  Resolving "wherever it appears" needs the set of registered peers, which
  this package does not have and should not, so the unresolvable case takes
  the safe reading.

A refused destination goes in `Decision.Withheld` rather than simply being
absent from `Targets`, because a repeater that has not opted in looks exactly
like a repeater nobody bridged and those need different answers from an
operator — the requirement the COLLISIONS counter failed.

**One name is one chip, and a club with two needs two entries.** There is no
pooling, and nothing here pretends otherwise. If a second chip is added, the
second name contends separately, which is the behaviour BLUEPRINT §7 asks for
and the reason capacity was made a first-class concept rather than an
afterthought.

## Alternatives considered

**A subscription list on the transcoder.** Rejected: it reimplements
scheduling, contention and drop reporting, and it creates the second code path
ADR-0052 warns about.

**Reusing `Upstream` and treating the vocoder as a link.** Rejected on
contention grounds above. It would have been tidier by one field and wrong by
one bug.

**A setting rather than a bridge — "transcode talkgroup 2".** Rejected because
it cannot express the thing an operator actually wants, which is a talkgroup
on a particular slot at a particular time. A bridge already expresses all
three.
