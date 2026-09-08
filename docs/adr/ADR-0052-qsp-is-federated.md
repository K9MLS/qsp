# ADR-0052: QSP is a federated network

**Status:** accepted, 2026-09-08

Frames [ADR-0051](ADR-0051-a-qsp-link-is-a-peer.md), which decided how two
servers link and was written as though two were the interesting case. This
record is the constraint that decision sits inside, and it corrects three
things 0051 got right for a club and wrong for a network.

## Context

QSP is being built for the amateur radio community, and the DMR networks it is
an alternative to are worldwide. A club server in Denton, ten servers across a
state, and a thousand servers across the world are not three sizes of the same
thing: the third has properties the first does not, and a design that only
serves the first is a design that has to be replaced.

**Federated names the property.** Each server is sovereign — it decides what it
carries, who it links to, and what it accepts — and servers cooperate through a
shared protocol rather than through anybody's central configuration. There is no
QSP headquarters, no registry a club must join, no operator whose server is more
authoritative than another's. That is not an aspiration; it is what makes QSP a
community alternative to a a commercial DMR server rather than another thing to be admitted to.

Three decisions taken on 2026-09-08 read differently in that light, and the
error in each is the same: **the sender was made responsible for what the
receiver gets.**

- *"Everything crosses; each side's access lists are the fence."* Federated at
  two servers. Centralist at a thousand, because it makes every server carry
  every talkgroup in the world and discard almost all of it at ingress.
- *"The administrator names the link."* Correct, and locally-chosen names
  collide. Two servers called `production` meeting at the third hop is a
  namespace with no coordinator and no rule.
- *"A link is one server logging into another."* Sound on the wire, and it left
  the listening end unable to tell a linked network from a hotspot — so half of
  a linked pair looked unlinked to its own administrator. 0051 called that
  asymmetry invisible in use. It was visible within four minutes of an
  administrator clicking through the console.

Each was reasoned about the mechanism and not about the person or the scale.

## Decision

**QSP is a federation of sovereign servers.** Four rules follow, and the rest of
this record is what each one costs.

### 1. A server decides what it accepts; nobody decides for it

Sovereignty is the first rule because everything else is a consequence. A server
carries what its administrator says it carries. No link, no neighbour, and no
protocol message may oblige it to carry anything else, and no server may be
required to register anywhere to take part.

Flooding — every talkgroup crossing every link — stays the **default**, because
it is what makes two clubs linking take one configuration block and no
negotiation, and that simplicity was hard won. It stops being the only option:
a link may say what it wants, and a server that says nothing gets everything as
today. Subscription is how a large network stops carrying the world, and it is
the receiver's decision because that is what sovereign means.

### 2. Identity is unique without a coordinator, and travels

Servers must be able to name each other without anyone issuing the names, so a
server has a stable identifier and a display name, and both travel with it.

The identifier is what the network uses; the display name is what an
administrator reads. **Both consoles of a linked pair show the same link with
the same name**, because two administrators discussing a link that each side
names differently is a support conversation nobody can have.

**Amended 2026-09-08, after the console satisfied that sentence and got it
wrong.** There are three names here, not two, and the rule above collapsed the
second and third. A **local label** is what an administrator calls a
configuration block on their own server; a **display name** is what a server
announces about itself; an **identifier** is what the network uses. Production's
Links page headed its link `production` — the local label the *test server* had
chosen for its own config block, carried faithfully across and rendered on the
server that label refers to, so an administrator on .247 read a link to the test
server under their own server's name. The far end's display name,
`QSP Test Server`, was two columns along.

So, precisely: **both consoles head a link with the far end's announced display
name.** A local label names a configuration block on the machine that holds one
and appears nowhere else. Only the dialling side has a config block, which is
why a local label can never be the shared name — half a linked pair does not
have one.

**Decided in [ADR-0053](ADR-0053-three-names-for-a-server.md):** a server has
three names — a self-generated opaque identifier the software compares, a
changeable display name a human reads, and the DMR ID, which stays the per-link
login it already is. The paragraph below is the question as it stood.

**What the identifier should be is deliberately left open**, and it is the one
choice here that cannot be changed once servers are running, because it is what
everybody calls everybody else. A DMR ID is unique, already issued, and every
operator has one — and it is issued to an *operator*, while a server outlives
the person who registered it and a club running three instances needs three from
a pool sized for members. BrandMeister gave networks their own numbering for
that reason. Today a `qsp` link presents a DMR ID in `repeater_id` and that is
what is wired; it is a starting point, not a conclusion.

### 3. A server says what it is

A QSP server registering with another announces that it is a QSP server, its
identifier, its display name, its network's name, and its version. The homebrew
identity packet already carries callsign, description, location and URL, so this
needs no new field on the wire to begin.

Without it a linked network is indistinguishable from a hotspot, which is the
defect this record was written after. With it the console can show a link from
both ends, and every later capability — the network view, subscription,
version-dependent behaviour — has something to build on. **Nothing else here is
possible until servers can identify themselves**, which is why it is first in
the order of work.

**Amended 2026-09-08: only the server that dials says what it is.** Identity
rides on registration, and registration goes one way. Production's Links page
shows Direction and Network for the test server; the test server's page has
neither column, because nothing comes back. So the listening end knows its
neighbour and the dialling end knows only an address — which is the same
asymmetry 0051 called invisible and this record was written after, one layer up
and pointing the other way.

A server must therefore **answer with its own identity when another registers
with it**. Until it does, rule 2's "both consoles show the same name" is not
merely unimplemented but unimplementable on the dialling side, which has nothing
to display. This is part of item 1 in the order of work below, not a separate
one: *a server says what it is* is not satisfied by saying it in one direction.

### 4. What a server knows about the network is its own view

There is no map. Each server knows its neighbours directly and learns of others
through them, so what any server holds is partial, possibly stale, and entirely
its own — a partition leaves two halves with different pictures and neither is
wrong.

This is a real protocol with real failure modes: entries that outlive the server
they describe, a rejoin reconciling two divergent views, a server that lies.
Worth building, because at a hundred servers an administrator wants to know who
is out there and through whom. **Not worth building casually**, and it gets its
own record.

## Consequences we accept

**Two servers stay as simple as they are today.** One configuration block on the
dialling side, nothing on the listening side, everything crosses. Every
mechanism here is inert until a network is large enough to need it, and that is
a design requirement rather than an accident: a club of two must not pay for a
federation of a thousand.

**Relaying needs a bound as well as deduplication.** Deduplication stops a loop
building into a storm, and it stops it at the first repetition rather than
before the frame has travelled. A hop count does not replace it — it bounds the
work a mesh does before dedup notices.

**Deduplication's table grows with the network.** Keyed on radio and stream with
entries expiring on a stream timeout, it holds one entry per transmission in
flight — a handful on a club network, and something worth measuring rather than
assuming on a large one.

**Subscription and flooding must not become two code paths.** A link that says
nothing gets everything; a link that names talkgroups gets those. If those grow
apart, the common case will be the one that rots, because almost every network
will be small.

**A federation admits servers we did not write.** Anything announced can be
false: a name, a version, a claim about the wider network. Nothing a neighbour
says may be allowed to make this server carry what its administrator has not
permitted — rule 1 is the answer, and it has to hold against a neighbour that is
misconfigured or hostile rather than only against one that is friendly.

**And this record supersedes 0051's claim that the dialling asymmetry is
invisible.** It is not, and the Links page shows a link from both ends
regardless of which side dialled.

## Order of work

1. **A server says what it is, in both directions**, and the Links page shows
   inbound links beside outbound ones, each headed with the far end's display
   name. The inbound half is built; the reply to a registration is not, so the
   dialling side still knows only an address. This is the defect an
   administrator hit tonight and the foundation for everything else.
2. **A hop count**, bounding relay before deduplication catches it.
3. **The link's own state on the page** — retrying, last attempt, last
   connected — since reconnection is silent to the console today.
4. **Subscription**, when a link asks for it. Its own record.
5. **The network view**, propagated hop by hop. Its own record.

## What this does not decide

*(The identifier is decided in ADR-0053.)* Whether subscription is expressed as
talkgroups, as a pattern, or as something else. How a partition reconciles.
Whether a server may refuse to relay for a neighbour, and what it owes one that
depends on it. Each is a decision this frame makes askable rather than one it
answers.
