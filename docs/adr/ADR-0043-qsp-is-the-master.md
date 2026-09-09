# ADR-0043: QSP is the master, and a club runs no second one

**Status:** Accepted
**Date:** 2026-09-02

## Context

QSP now carries audio both ways on two protocols: Homebrew hotspots and
Motorola repeaters on IP Site Connect, with a conversation held between one of
each on 2026-09-02. That makes a question urgent that was theoretical while the
bridge was one-way.

**Where does authority sit?**

IP Site Connect has a master and it has peers. Nothing in the protocol requires
that the master be QSP. A club could run a Motorola repeater in the master role,
point its other repeaters at that, and hang QSP off the side as a peer — which
is roughly how a commercial server is often deployed, and it is the shape a club
with an
existing IPSC system would reach for first.

Until now QSP had no position on this. It happened to be a master because that
was the half of the protocol the captures showed, and `cmd/ipsc-probe` exists to
answer a repeater rather than to ask a master anything.

## Decision

**QSP is the master. A club that deploys QSP runs no other master, and QSP is
never a peer in production.**

Every Pi-Star and every Motorola repeater points at QSP. There is no Motorola
master repeater alongside it, nothing commercial, and no second thing to
configure and
keep alive.

### Why authority belongs here

**QSP is the only component that can see the whole network.** A Motorola master
sees IPSC peers. A Homebrew master sees hotspots. Neither sees the other, and a
bridge hanging off the side of one of them is a translator that has to ask
permission. Routing, last-heard, subscription, access control and the call
record are all whole-network facts, and every one of them is wrong or partial in
a deployment where something else holds the centre.

This extends [ADR-0019](ADR-0019-master-repeats.md), which established that
repeating as a master is QSP's primary function and bridging a layer on top of
it. That record settled *what a master does*; it did not say that QSP must be
the one. On the Homebrew side the answer was never in doubt, because a hotspot
has no master role to offer. IPSC does offer one, and this record declines it —
so the two protocols now agree rather than leaving IPSC as the one place the
answer was accidental.

### What this rules out, deliberately

**Augmenting an existing IPSC system.** A club whose Motorola master they cannot
or will not reconfigure cannot adopt QSP incrementally. It is replace, not
augment, and somebody will ask.

That is a real cost and it is accepted on purpose. Augment-mode means QSP must
be correct as a peer *and* as a master, forever, on a protocol reconstructed
from captures — and it means every deployment has two things that can fail and
two places to look. The project's answer to "what does a club run" should be one
binary.

## Consequences

**Gained: QSP never has to implement the IPSC peer role.** That is half the
protocol it never has to get right, defend, or keep working. Registration as a
client, the ten-second retry, keepalive as a peer, and the behaviour of a peer
whose master vanishes are all simply absent, and their absence is now a decision
rather than a gap.

**Gained: the master role is the thing that must be right**, and it is the thing
QSP is. Effort concentrates rather than splitting.

**Accepted: QSP's behaviour as a master is still partly inferred.** Everything it
knows about being a master, it learned from watching peers talk to one. That
shows what a master must *answer*. It shows nothing about what a master
*initiates* — a message a real master sends unprompted is one QSP has never
seen, does not send, and cannot miss by studying its own captures. ADR-0041
covers the transmit path built this way and ADR-0042 corrected its shape; this
record does not close that gap, it names it.

**Bench instruments may play a peer. Nothing shipped may.** Settling the above
means observing a real master, which means something registering to one as a
peer and writing down what arrives. Such a tool is a diagnostic in the same
category as `testdata/` and `cmd/ipsc-probe` — it never lives in `cmd/qsp`, never
runs at a club, and its existence is not a route back to peer support. The
operator reconfiguring a repeater into master role on a bench for an afternoon
is not a club running a second master.

## The limit of "QSP controls all audio"

Worth stating plainly, because the sentence is easy to over-read.

**QSP has authority over delivery, not over transmission.** It decides what
reaches a repeater. It does not decide what the repeater puts on the air: a
Motorola repeater receives everything QSP sends and filters by its own codeplug,
naming the talkgroups and timeslots it carries. QSP cannot learn that codeplug —
an IPSC peer announces no subscriptions, unlike a Homebrew peer that attaches to
talkgroups explicitly — so filtering at the master would mean guessing at
somebody else's programming.

So the network's routing is QSP's and is complete. The last hop is the
repeater's, and that is the protocol rather than a shortfall. An operator asking
"why did that talkgroup not come out of my repeater" is asking a codeplug
question, and the console should not imply otherwise; see ADR-0042 and the
peer table's "all, filtered at the repeater".
