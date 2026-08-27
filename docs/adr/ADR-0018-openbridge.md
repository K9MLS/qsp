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

Three options were considered:

1. Report an enabled upstream as healthy. Honest about what is known, useless to
   an operator.
2. Track the time of the last frame received and report the link as stale beyond
   a threshold.
3. Report frame counts only, and let a human interpret them.

**Option 2**, with the threshold configurable and the health summary stating
what it actually means: *"no traffic received for 47 minutes; this may be a
quiet talkgroup or a broken link"*. Constitution §3 requires an absent
capability to say so, and silence that might be either is exactly the case §3
exists for. The wording must not claim more than QSP knows.

Any default threshold is a guess. It is configuration, defaulting to something
long enough not to cry wolf on a quiet club network.

## Consequences

- A club can link to BrandMeister without QSP impersonating a hotspot.
- `routing.Endpoint` changes, and the existing routing tests become the
  regression check for that change.
- QSP-to-QSP-to-elsewhere relaying is not possible. Direct links only.
- A stale-link warning is a guess dressed as a measurement unless its wording is
  careful. It is worded carefully.
- ADR-0008 remains open, with one more case recorded against its interim rules.

## Alternatives considered

**Homebrew peer.** Rejected: prohibited by the network being connected to, and
fragile besides.

**Nothing — clubs stay isolated.** Rejected. A network that cannot reach the
wider DMR world is a worse product than a commercial DMR server for the one thing a commercial DMR server is
bought for.
