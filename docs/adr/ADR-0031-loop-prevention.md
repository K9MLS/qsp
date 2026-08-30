# ADR-0031: A transmission is recognised by who sent it, not by where it arrived

**Status:** Proposed — amended before implementation
**Relates to:** [ADR-0014](ADR-0014-contention.md),
[ADR-0018](ADR-0018-openbridge.md), [ADR-0024](ADR-0024-outbound-peer-mode.md)

## Amendment: the loop was already prevented, and this record overstated it

**The first version of this ADR argued that loops were an open hazard and that
no link should carry traffic until this was built. That was wrong, and it was
wrong because the code was not read far enough.**

`routing.Core.route` already refuses to send a frame that arrived on a link to
any link, and `RouteFromUpstream` documents the reasoning: a club exporting and
importing the same talkgroup would relay every frame from BrandMeister straight
back to BrandMeister, which is a broadcast storm on somebody else's network
produced by a configuration that looks entirely reasonable. It also already
rejects the hop count this record considered, on the grounds that it requires
every participant to cooperate and fails into the storm it was meant to prevent
when one does not.

That rule is stronger than what is decided below. It does not detect loops, it
makes them unformable: a QSP in the middle of A to B to C never relays at all,
so circulation cannot begin.

**What survives is duplicate suppression, which is a narrower and less urgent
problem.** Two links to one far network under different names, or a far end
that reaches this instance by two paths, deliver one transmission twice. Neither
copy arrived from a link this instance would send back to, so the existing rule
does not see them. The fingerprint below is what does.

The decision that follows is unchanged and still worth having. The claim that
peering must wait for it is withdrawn.

**And the shallowness this record recommends is already the implemented
behaviour rather than advice.** A QSP cannot relay between two other servers
even if an administrator configures it to. Club B cannot reach Club C through
Club A, and no setting makes it possible.

## Context

QSP can already link to another server two ways: OpenBridge, which bridges two
networks over an agreed passphrase, and outbound peer mode, which logs into
somebody else's master. ADR-0024 names another QSP among the things the second
one reaches. Neither has carried traffic against a real far end.

So today every instance is a leaf. Nothing loops because nothing connects.

That changes the first time two clubs peer, and it changes irreversibly. **A
protocol behaviour is a commitment to every instance that already speaks it.**
Once a dozen clubs are federated, a change to how frames are recognised has to
be adopted by all of them at once or the network partitions. Every other item
on QSP's list can be added later. This one is decided exactly once, and it has
to be decided before the first link carries audio rather than after.

### What a loop does to a DMR network

Club A peers with B, B with C, C with A. A transmission entering at A reaches B,
reaches C, returns to A, and goes round again. Nothing stops it.

The medium makes this worse than the equivalent in a store-and-forward network.
A duplicated message is a nuisance; a duplicated *frame* arrives on a timeslot
that can carry one call, so ADR-0022's contention refuses one of the two — and
which one it refuses depends on arrival order, which means a member hears their
own transmission fighting itself. A saturated slot stays saturated: the loop
feeds itself faster than the reservations expire.

**DMR gives us nothing to detect this with.** There is no TTL, no hop count and
no path vector anywhere in a frame. The homebrew protocol adds none. We are not
adding one either — see below.

### What survives a relay, and what does not

Two facts about the wire decide this ADR, and they point in opposite directions.

**`RepeaterID` does not survive.** `openbridge.Encode` assigns the local network
ID into `data.RepeaterID` on every frame it sends, because on OpenBridge that
field names the sending *server* rather than a repeater. A frame relayed through
three servers carries the third one's identity and no trace of the first.

**`StreamID` does survive.** `hbp.StreamID` is the unit of one keyup: every
frame of a transmission carries it and it changes when the operator unkeys. The
codec's own comment records the evidence — the voice fixture shows the same
StreamID on both the local and the master link as a gateway relays a
transmission. Real gateways preserve it. So does `SourceID`, the radio that
keyed up, which is globally unique because RadioID.net makes it so.

This is the whole problem in one sentence. **The field that identifies where a
frame came from is rewritten in transit; the field that identifies which
transmission it belongs to is not.**

### Why `sourceKey` cannot be reused

`routing.sourceKey` already identifies a transmission at its origin, as
`(peer, stream, slot, upstream)`, and it is correct for what it does. Its
`upstream` member exists precisely because two networks choose stream IDs
independently and a collision would look like a continuation of the same stream.

It is exactly wrong for this job, for the same reason it is right for that one.
`peer` is `RepeaterID`, which OpenBridge overwrites, and `upstream` is the link
a frame arrived on — the thing that *differs* between the original and the
looped copy. Keyed this way, a frame returning from a three-hop circuit looks
like a brand new transmission from a different sender, which is the one
conclusion that must not be drawn.

Two keys, two jobs. Contention asks "what is on this slot right now", and the
answer is local. Loop detection asks "have I already seen this keyup", and the
answer has to hold across servers that rewrite headers.

## Decision

**A transmission is fingerprinted as `(SourceID, StreamID)`, and the first
ingress path to present it owns it for the life of the stream.**

On ingress — from a peer, an OpenBridge link, or an outbound peer link — QSP
records the fingerprint against the path it arrived on. A frame bearing a
fingerprint already held by a *different* path is dropped before routing. A
frame from the same path continues normally.

The entry lapses on the stream timeout that already ages reservations, so a
fingerprint is remembered for the length of a keyup and a margin, not forever.

### Why this key

`SourceID` is a radio, and a radio transmits one stream at a time. Two
concurrent transmissions cannot share a fingerprint, because that would require
one operator keying two radios that hold the same ID. Reuse across time is
bounded by the timeout.

Neither half is enough alone. `StreamID` is 32 bits chosen by the originating
hotspot with no coordination, so two clubs collide eventually. `SourceID` alone
would drop a member's second transmission as a duplicate of their first.

### First path wins, and what that costs

The rule is deliberately not "shortest path" or "best path", because QSP cannot
measure either. Whichever copy arrives first is the real one.

**When the looped path is faster than the direct one, the wrong copy wins.**
The audio is identical, so a listener hears no difference, but the extra hop's
jitter is inherited for that transmission. This is a quality cost accepted in
exchange for a rule with no configuration and no measurement in it. A rule that
picked a path by latency would need timing state per peering, and would change
its mind mid-transmission.

### QSP's own traffic gets a fresh stream

Parrot replays bytes it never understood ([ADR-0028](ADR-0028-parrot.md)),
including the StreamID it recorded. A replay carrying the original fingerprint
would be dropped as a loop of the transmission it is replaying.

**Anything QSP originates or replays takes a new StreamID.** This is a
requirement on parrot and on any future announcement, not a special case in the
loop check: the check has no exceptions, and a component that would need one is
wrong.

## We are not adding a field to the wire

The obvious fix is a hop count, and it is available: the homebrew frame has
bytes QSP already preserves verbatim as `Trailing`.

**No.** QSP's value is that it speaks what MMDVMHost, BrandMeister, HBlink and
a commercial DMR server already speak. A frame carrying a field only QSP understands is a frame
that behaves differently depending on who relays it, and the failure appears at
the far end of somebody else's network where nobody can debug it. ADR-0010 keeps
codecs parsing what is there rather than interpreting it, and this is the same
discipline pointed outward.

The fingerprint uses fields that already exist and already mean this. Nothing
downstream has to change, and a QSP peering with a a commercial DMR server is protected by the
same rule as one peering with a QSP — because the rule lives entirely in the
receiver.

## Federation stays shallow, and this is why

This ADR makes a mesh survivable. It does not make one advisable.

Each hop adds a jitter buffer, and the arithmetic is unforgiving on a medium
with a 60 ms cadence. Two hops is defensible. Four is a network where people
talk over each other because nobody hears the pause.

**A link is agreed by two administrators, and traffic is not relayed onward by
default.** Club B peers with Club A to join A's net; B does not reach C through
A. That is a deliberate departure from how a fediverse works, and it comes from
the medium rather than from taste: an asynchronous network can afford transitive
delivery because nobody notices the delay, and a real-time one cannot.

Loop prevention is the backstop for the case where two clubs peer without
realising a third link closes the circle. It is not permission to build a mesh
and let the drop rule sort it out.

## Consequences

- **The check runs before routing and before access control**, because a looped
  frame is not a request to be evaluated. It is a frame this instance has
  already handled, and evaluating it twice means logging it twice and counting
  it twice.
- **A dropped duplicate is not an error and must not be logged as one.** On a
  correctly configured mesh it is the ordinary case, and a log line per frame at
  60 ms intervals would bury everything else. The console should show a count
  per link, which is the number that tells an administrator a circle exists.
  That number is also the only warning anybody gets, so it needs to be visible
  rather than merely recorded.
- **Fingerprint state is per instance and lives beside the peer registry**, for
  ADR-0002's reasons: written on every accepted frame, aged out on a timeout,
  owned by the single writer.
- **It costs a map entry per concurrent transmission**, not per frame. A busy
  club network carries a handful at once.
- **It cannot detect a loop that changes the source.** A gateway that rewrites
  `SourceID` — some cross-mode bridges do — produces a frame QSP has no way to
  recognise. This is a real limit and the reason federation is agreed between
  administrators rather than discovered: the topology has to be known to
  somebody, because QSP cannot always work it out from the traffic.
- **Testing this needs two instances.** A unit test can assert the fingerprint
  and the drop, and will. It cannot show that a real relay preserves StreamID
  across a real peering, and §8a of `PROJECT_MEMORY.md` records what this
  project thinks of conclusions the test suite reached on its own. **The first
  federated link is a scheduled point-to-point one between two administrators
  who can telephone each other**, and the duplicate counter is the thing to
  watch on it.
