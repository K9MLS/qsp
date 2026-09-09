# ADR-0051: a link between two QSP servers is a peer, not a bridge

**Status:** accepted, 2026-09-08 — **confirmed on air the same day**

A QSP server logged into another QSP server as a peer at 16:15:07 UTC:
`peer connected peer_id=3132912 callsign=K9MLS from=192.168.1.27:58483`, four
milliseconds from starting to connected, with no port forward on the dialling
side and no bridge in the configuration.

A minute later a hotspot user in Denton was heard on a Motorola repeater
through it: `call started peer_id=3132910 talkgroup=2 timeslot=2`. **TS2, not
TS1.** Every run over OpenBridge had read TS1. The repeater keyed on the slot
the codeplug uses and the radio opened squelch — audio in both directions,
which had never worked.

Supersedes the QSP-to-QSP half of
[ADR-0018](ADR-0018-openbridge.md). Amends
[ADR-0031](ADR-0031-loop-prevention.md), whose blunt rule this replaces with
deduplication. Builds on [ADR-0019](ADR-0019-master-repeats.md), which is the
same mistake one layer in.

## Context

Two QSP servers linked over OpenBridge, both ends configured, both links
reporting healthy, and **no audio crossed in either direction for a day.** Then
a repeater keyed on network audio and the receiving radio heard nothing. Three
separate faults, one cause.

OpenBridge forces timeslot 1. `internal/protocol/openbridge/openbridge.go` says
so and has said so the whole time: proper OpenBridge passes all traffic on TS1
with the slot bit clear, because BrandMeister needs a way to take traffic from
many networks onto one slot. **That is correct for BrandMeister and wrong for
two instances of the same software.** Both ends know exactly what talkgroup and
timeslot a frame is on, and the pipe between them deliberately throws the slot
away. Everything downstream then has to guess what to put back.

The guessing had a home, and that is the second half of the cause. **The only
way to get traffic to a link was to write a bridge**, and a bridge joins
endpoints, and an endpoint is a talkgroup on a timeslot. So the accept form had
to ask an operator for a timeslot. The operator was answering the mechanism's
question rather than making a decision, there was no right answer to give, and
the default was 2 because a club talkgroup lives on TS2. Nothing arriving from
the link could ever match it.

That is [ADR-0019](ADR-0019-master-repeats.md) happening a second time, one
layer out. QSP spent two days building bridging before repeat existed because
bridging was mistaken for the routing model, caught when four hotspots on one
talkgroup could not hear each other. **Repeat was then built for peers and
never for links.** A link still gets traffic the way everything did before
0019: because somebody wrote a bridge. Bridging translates between talkgroups.
Two servers agreeing to carry each other's traffic is not translation. It is
repeat, across a wire.

The evidence was in the tree the whole time. `Upstream.Export` and
`Upstream.Import` are validated, stored, documented as what crosses in each
direction, and **read by no routing code**, which `config.go` states outright.
That was filed as a defect. It is not one. Nobody ever wrote the code to
consult them because there was never anything for them to decide.

### What other systems do about the other half of the problem

The symmetric-pipe design also costs a port forward at both ends, and the
amateur digital voice world has already learned this lesson twice.

XLX interlinking needs UDP 10002 forwarded, a fixed address with a published
DNS record, and mutual configuration — the remote XLX must list this XLX in its
own interlink file before the link comes up. Both admins edit files, both
forward ports, and neither side works until both are right. That is our
situation restated, and it is a known sore point rather than a model.

D-STAR reached the same conclusion earlier. DExtra requires port 30001 open
inbound, which requires port forwarding; the published guidance is that DPlus
is the universal option that works without additional router configuration.

The homebrew/MMDVM protocol needs no forwarding at all. Pi-Star's own
documentation says UDP 62030–62032 are outgoing ports and outgoing ports are
not forwarded. The BrandMeister specification makes the reason explicit: the
ping interval is 5 to 15 seconds, with 5 preferred for most firewalls. The peer
dials out, the router holds the mapping, and traffic flows both ways through
that one hole. **It is why a Pi-Star works behind a domestic router with
nothing configured.**

And when XLX links to XLX over DMR, it does not use the interlink protocol at
all. It uses a `DMR_Hosts.txt` entry — name, DMR ID, hostname, password, port
62030. A client login, to another server.

QSP already has both halves of that and has never pointed them at each other. A
master listens on 62031 for hotspots and three of them across the United States
use it daily. Outbound peer mode exists —
[ADR-0024](ADR-0024-outbound-peer-mode.md), listed in project memory as layer 5,
built and never having met a real far end.

## Decision

**A link between two QSP servers is one server logging into the other as a
peer.** Not OpenBridge, not a new protocol, not a new port.

Five consequences follow, and each is a decision in its own right.

### The timeslot and talkgroup cross unchanged

Nothing in the peer path forces a slot. TG 2 on TS 2 arrives as TG 2 on TS 2.
§6b already says a talkgroup number is never renumbered — 2 is 2 on both sides
of a hotspot — and **the timeslot now gets the identical rule**: TS 2 is TS 2 on
both sides of a link. The defect that opens this record cannot exist in a path
that never touches the field.

Two administrators still have to agree that TG 2 lives on TS 2, the way a
repeater pair agrees on a frequency. That is a human agreement and the software
does not enforce it. It does have to make a disagreement visible; see
consequences.

### Everything crosses, and the access lists are the only fence

A link carries no talkgroup list, no timeslot, and no bridge. A linked server
is a peer, and repeat already delivers every talkgroup to every ready peer.
Each side's own ingress check decides what it keeps.

So a server carrying TG 2 and TG 4 links to one carrying TG 2 and TG 6, and TG 2
works while TG 4 dies at the far end's ingress — not because the link filtered
it but because that network does not carry TG 4. **Nothing about that needs
configuring on the link.**

This is what an administrator already means when they say two servers are
linked. Open by default, with a fence each admin already owns and already edits.

`Upstream.Export` and `Upstream.Import` are removed, along with the validation
that implies they mean something. A field that describes routing and performs
none is the failure this project keeps recording.

### A frame is recognised, not restricted

[ADR-0031](ADR-0031-loop-prevention.md)'s rule — a frame from a link is never
sent to a link — exists to prevent a broadcast storm on somebody else's
network, and BrandMeister disconnects bridges caught re-bridging. It is
retained for OpenBridge, where the far end is a foreign network and bluntness is
the right trade.

It is **replaced for QSP-to-QSP links by deduplication on the transmission.**
Each server remembers the recent (source radio ID, stream ID) pairs it has
carried and drops a repeat. A frame arriving twice by two paths is carried once;
a frame coming back around a loop is dropped on arrival.

The reason this matters is scale. Ten servers all connected to each other is
forty-five peerings, nine per administrator, and an eleventh server means ten
new peerings coordinated with ten people. **That is not simple, it is the
opposite.** Relaying is what makes a large network simple, and deduplication is
what makes relaying safe. It also makes topology free: hub, chain, mesh, or an
accidental ring all work and self-heal, so nobody has to design the network.

Keyed on source radio ID *and* stream ID, within a short window. Stream IDs are
chosen independently by each network, so a collision on stream ID alone is a
matter of time rather than malice — the routing core already says so about its
own contention keys.

The state largely exists: routing tracks `sourceKey{peer, stream, slot}` for
contention. This extends it rather than inventing it.

### A server presents itself as a repeater

Duplex, both timeslots, full identity. **Not as a hotspot.** The homebrew
specification says a simplex or DMO client transmits with the slot bit unset,
which would reintroduce the defect that opens this record through a different
door.

Each server needs its own DMR ID, and the peering exchange refuses a collision
with a sentence naming both. This is not hypothetical: on 2026-09-08 both K9MLS
servers were announcing IPSC master ID 3132911 simultaneously, and a repeater
that meets a master announcing the repeater's own ID retries silently forever
with no indication of cause. Whether a server carries a registered ID of its own
or an operator ID with an ESSID suffix, as hotspots do, is deliberately left
open.

### A link retries forever, with capped backoff

5 s, 10 s, 20 s, 40 s, 80 s, then held at 120 s indefinitely, with jitter, reset
to 5 s on a successful login.

Not a hotspot's fixed five seconds forever: a server goes down for maintenance,
and hammering a dead host every five seconds for four hours is nine thousand
pointless packets and a log nobody can read. The fast start covers the common
case — a restart, a blip, a config reload — and nobody notices. The cap means a
four-hour outage costs 120 attempts and the network is whole within two minutes
of the far end returning. Jitter so that ten servers losing one hub do not all
return on the same tick.

**Never giving up is the point.** At ten servers, a link that stays down until a
human notices is a hole in the state that nobody owns.

## Consequences we accept

**The setup is asymmetric even though the traffic is not.** One side dials and
one side listens, decided by who offered — an offering server must be reachable,
and it already is, because it serves hotspots. So the far end reconnects on its
own and the listening side cannot force it. That is exactly how the three
existing hotspots behave and it has never been a problem, but it is real.

> **Amended 2026-09-08, the same evening.** This section said the asymmetry was
> invisible in use. It is not. The listening end cannot tell a linked network
> from a hotspot — same handshake, same port — so its Links page listed nothing
> while a link was carrying audio, and half of a linked pair looked unlinked to
> its own administrator. Found within four minutes of clicking through the
> console, and the claim was reasoned about the wire rather than about the
> person reading the page. See [ADR-0052](ADR-0052-qsp-is-federated.md), which
> makes a server announce what it is and shows a link from both ends.

**Two administrators both behind NAT with no forwarding cannot link.** Neither
can host, and no protocol fixes that without a relay. Serving that case is out
of scope here and would be a new decision, not an extension of this one.

**A slot mismatch is silent unless the console shows it.** If one side puts TG 2
on TS 1 and the other on TS 2, nothing errors: the traffic dies at the ingress
check and the link looks dead. That is the failure this record opens with,
wearing a different label. The answer is not a configuration knob but a mirror —
one line per link reporting what actually arrived against what this server
carries, so the mismatch is legible in seconds rather than a day.

**A link must open and close without a restart.** `applyPending` does not
reconcile upstreams today, which is tolerable when a link is a line in a file
and is not when a link is a live connection that drops and returns on its own.
Telling a club administrator that adding a link means dropping every station on
their network is the commercial answer. This is where that debt comes due.

**OpenBridge stays, narrowed.** BrandMeister and DMR+ will never speak anything
else and forcing TS1 is correct there. It stops being how QSP servers reach each
other, which was the mistake. Existing QSP-to-QSP links are torn down and
re-peered once.

**Ten servers is not a bandwidth problem.** A DMR voice stream is about a
kilobyte per second per slot; nine copies is nine kilobytes per second. The real
cost is latency, and QSP forwards frame by frame with no jitter buffer, so a hop
is a UDP relay plus a round trip — on the order of ten milliseconds across a
state, well inside one 60 ms frame. Relaying is cheap enough to prefer over a
mesh, and deduplication makes a mesh harmless when somebody builds one anyway.

## What this does not decide

Whether a QSP server carries a registered DMR ID of its own or an operator ID
with an ESSID suffix. Whether the peering exchange should negotiate anything
beyond identity — version, carried talkgroups, agreed stream-ID ranges — now
that both ends are known to be QSP. Whether a hop count or a network-ID trail
should eventually bound relaying beyond what deduplication gives. Each is worth
a record of its own once this one is built and running.
