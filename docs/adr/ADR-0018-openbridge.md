# ADR-0018: OpenBridge for linking to other networks

**Status:** Proposed
**Date:** 2026-08-27

## Context

A club network that only talks to itself is a smaller thing than a club wants.
BLUEPRINT-v1 §4 makes linking outward a phase in its own right, and BrandMeister
is the network most clubs would link to first.

There are two ways QSP could reach a BrandMeister master, and only one of them
is permitted.

## Decision

**QSP links to BrandMeister using OpenBridge. It does not log into a
BrandMeister master as a homebrew peer.**

This is not a preference between two workable options. BrandMeister's own
documentation requires the OpenBridge protocol for interconnecting another
amateur radio network, defines "peer bridging" — using the MMDVM or Homebrew
protocol with a hotspot or other software to bridge reflectors or servers — as
prohibited, and asks specifically that nobody build software or appliances
without an onboard radio module that impersonate those protocols.

QSP is exactly the software they are describing. Registering as a homebrew peer
would work today, would be indistinguishable from a hotspot to their master, and
would be doing the thing the operators of that network have asked people not to
do. It would also break without warning on any version bump, unsupported and
deservedly so.

### What OpenBridge is

A simple protocol based on MMDVM that carries `DMRD` packets only, with **no
connection establishment and no keep-alive**. Two networks agree a passphrase and
a network ID out of band; frames are then authenticated and exchanged over UDP.

This is far less work than the homebrew master QSP already has. There is no
handshake, no salt, no peer lifecycle, no registry. `internal/protocol/hbp`
already parses and round-trips `DMRD` against frames captured from a real radio.

**Approval is the hard part, not the code.** Bridges are granted at each
master's discretion, and a request has to show benefit to that network's
community. QSP can be ready; a club still has to ask.

### Provenance, under ADR-0008

OpenBridge's format is documented on the BrandMeister wiki, which carries the
same CC BY-NC-SA licence [ADR-0008](ADR-0008-protocol-licensing.md) is about.
This is the same limb already exercised for the Homebrew specification, under
the same interim rules: **wire format only** — field names, offsets, lengths —
with no implementation source read and no text, table or code copied.

Provenance is recorded per message type in the code, as for HBP. This ADR does
not resolve ADR-0008; it applies the interim rules to one more case and records
that it did.

## Design

### Configuration

Upstreams are **named blocks**, plural, because a club may link to BrandMeister
*and* to a neighbouring QSP instance:

```json
"upstreams": [{
  "name": "brandmeister",
  "enabled": false,
  "address": "3102.master.brandmeister.network:62035",
  "network_id": 3132910,
  "passphrase_file": "/var/lib/qsp/bm.pass",
  "export": [{ "talkgroup": 3148, "timeslot": 1 }],
  "import": [{ "talkgroup": 3148, "timeslot": 1 }]
}]
```

Every value here is administrator configuration, per BLUEPRINT-v1 §0. A club in
Toulouse points at `2081`; a club in Texas at `3102`. QSP has no opinion.

**`export` and `import` are separate lists.** A club may wish to send its local
net upstream while accepting a nationwide talkgroup down, and those are not the
same set. Collapsing them into one list makes the asymmetric case unexpressible
and the symmetric case look safer than it is.

**`enabled` defaults to false.** Enabling an upstream puts a club's audio onto
somebody else's network. That should take a deliberate edit rather than arriving
switched on because a configuration was copied.

**The passphrase lives in a file**, not in `qsp.json`, matching `peer.pass` and
subject to the same `0600` check. Configuration gets pasted into forum posts and
support requests; secrets should not travel with it.

### Loop prevention

**A frame that arrived from an upstream is never sent to an upstream.**

If QSP exports TG 3148 and imports TG 3148 — which is the ordinary case, not an
exotic one — then without a rule a frame from BrandMeister is relayed straight
back to BrandMeister, and the second copy is indistinguishable from a new
transmission. That is a broadcast storm on somebody else's network, caused by a
configuration that looks entirely reasonable.

The rule is deliberately blunt rather than clever. Hop counts and origin tags
would allow QSP-to-QSP-to-BrandMeister chains, but they require every
participant to cooperate, and the failure mode when one does not is the storm
this rule exists to prevent.

**Consequence, stated plainly:** two QSP instances cannot relay for each other
through a third. A club needing that links directly.

### Timeslot: OpenBridge is TS1

Proper OpenBridge passes all traffic on TS1, with the slot bit clear in the
`DMRD` header. HBlink extends this to both slots for unit calls only, and marks
that as an extension rather than the protocol.

**This is not optional and it has a consequence for every club.** Hotspot
talkgroups are conventionally on TS2. Anything QSP exports must be moved to TS1
on the way out, and anything imported must be moved to the configured local
timeslot on the way in.

QSP already translates timeslots — `forward_test.go` covers TS1↔TS2 across a
bridge — so the mechanism exists. What matters is that the `export` and `import`
lists name the **local** talkgroup and timeslot, and the TS1 rule is applied by
QSP rather than left for an administrator to remember.

### Routing

**Upstreams route through the existing core**, as endpoints in `routing.Table`,
rather than beside it.

Contention (ADR-0014), talkgroup and timeslot translation, and the delivery path
measured at a hundred peers all apply unchanged. The alternative — a parallel
path for upstream traffic — means two implementations of routing, and the second
one would be the one without 297 tests around it.

The cost is that `routing.Endpoint` gains a notion of a destination that is not
a peer ID. That is a real change to well-tested code and should be made
deliberately, with the existing tests as the check.

### Reporting a link that is not working

OpenBridge has no keep-alive, so QSP **cannot distinguish "no traffic" from
"the far end is gone"**. A quiet talkgroup and a dead link look identical.

That sounds like a minor reporting question. The research says otherwise, and
three findings turn this from a judgement call into the most valuable thing this
feature can offer.

**It is the documented top failure.** The BrandMeister wiki's OpenBridge FAQ
leads with "not seeing any traffic from BrandMeister on your OpenBridge
connection", and the answer is to check UDP forwarding and verify the IP address
is still the one supplied at setup. A link therefore breaks *silently* on any
address change — and the wiki notes BrandMeister logs "connection address of
OpenBridge system changed" on their side, not ours. The DVSwitch mailing list
carries operators spending days on exactly this, with no diagnostic to work
from.

**Monitoring is explicitly the operator's job.** BrandMeister's bridging policy
asks bridge operators to confirm they understand it is not the BrandMeister
team's responsibility to alert them to issues or down connections, and that
monitoring their own servers is theirs.

**And silence has a consequence.** The same policy states that bridges showing
no traffic for more than 60 days, or that are not connected, may be disconnected
or removed without notice. A link that quietly died is a link that will quietly
be taken away, and re-requesting it means going back through approval.

So the decision:

1. Report an enabled upstream as healthy. Honest about what is known, useless to
   an operator, and leaves them in the position the mailing list describes.
2. Track the time of the last frame received and report the link as stale beyond
   a threshold.
3. Report frame counts only, and let a human interpret them.

**Option 2.** Constitution §3 requires an absent capability to say so, and
silence that might be either is exactly the case §3 exists for. The health
summary must state what it actually means and claim no more than QSP knows:

> no traffic received for 4 hours; this may be a quiet talkgroup or a broken
> link

The console shows the last-heard time unconditionally, whatever the threshold
says, because that is the number an operator actually reasons with.

**The threshold is configuration and any default is a guess.** It should be long
enough not to cry wolf on a club talkgroup that is genuinely quiet overnight,
and far short of the 60 days at which a bridge is at risk of removal. A default
in hours rather than minutes or days.

## Consequences

- A club can link to BrandMeister without QSP impersonating a hotspot.
- `routing.Endpoint` changes, and the existing routing tests become the
  regression check for that change.
- QSP-to-QSP-to-elsewhere relaying is not possible. Direct links only.
- A stale-link warning is a guess dressed as a measurement unless its wording is
  careful. It is worded carefully, and the last-heard time is always shown.
- Every exported talkgroup is translated to TS1 and back. An administrator
  configures local talkgroups and timeslots; the TS1 rule is QSP's to apply.
- **No re-bridging.** BrandMeister prohibits re-bridging talk groups provided to
  a bridge, and states that connections found doing so are disconnected without
  notice. The loop-prevention rule above happens to enforce this, but it is
  worth recording as a policy obligation rather than a side effect: a club that
  bridges a BrandMeister talkgroup onward to a third network is breaking the
  terms its bridge was granted under.
- ADR-0008 remains open, with one more case recorded against its interim rules.

## Alternatives considered

**Homebrew peer.** Rejected: prohibited by the network being connected to, and
fragile besides.

**Nothing — clubs stay isolated.** Rejected. A network that cannot reach the
wider DMR world is a worse product than a commercial DMR server for the one thing a commercial DMR server is
bought for.
