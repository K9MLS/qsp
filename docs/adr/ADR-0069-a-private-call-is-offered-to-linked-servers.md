# ADR-0069: A private call to a radio not heard here is offered to every linked server

**Status:** Accepted
**Date:** 2026-09-29
**Relates to:** [ADR-0021](ADR-0021-private-calls-and-data.md),
[ADR-0051](ADR-0051-a-qsp-link-is-a-peer.md),
[ADR-0046](ADR-0046-ipsc-private-calls.md),
[ADR-0067](ADR-0067-qsp-originates-a-text-message.md)

## Context

On 2026-09-29 the operator set up a second radio, 3132911, on the Motorola
repeater behind the test server. The test server is linked to production, and
the R7 (3132910) sits on a Pi-Star on production. Group calls and group texts
crossed the link in both directions. Private calls and private texts did not
cross in either direction.

The capture on production showed the R7's private call arrive from the Pi-Star
and go nowhere. That was routing working as it was written. ADR-0021 routes a
private call by asking which of this server's peers the called radio was last
heard through. For a radio on another server the honest answer is none. The
call then went only to the local Motorola repeaters (`NoHomebrewDestination`),
and there were none behind production.

A group call crossed only because ADR-0051 makes repeat offer every group call
to every QSP link. Nothing made the same offer for a private call.

**A linked server takes one of two shapes, and routing knew only one.** A link
this server dialled is an upstream, listed in `CoreOptions.QSPLinks`. A server
that dialled *this* one logs in as a peer. Routing could not tell that peer
from a hotspot. For a group call the difference never mattered, because repeat
reaches every peer. For a private call it is the whole difference.

## Decision

**A private call to a radio this server has not heard is offered to every
linked QSP server.** That means every dialled QSP link and every ready peer
whose package ID marks it as a QSP server (`MasterConfig.IsQSPLink`, the same
test that decides who gets the identity reply). The far server does what it
does with any private call: it looks the radio up among its own peers and
offers the call to its own Motorola repeaters. A server where the radio is not
has nobody to give it to.

- **Voice and data alike.** A private text is a CSBK preamble, a data header
  and Rate 3/4 blocks, each with its own stream ID. Every one of them takes
  this path.
- **The timeslot and the target cross unchanged**, as they do for a group call
  (ADR-0051).
- **Never back to where it came from.** A call from a linked-server peer is not
  offered to that peer. A call from a link is not sent back over that link:
  the existing loop rule applies, and it is `NotAJudgement`. The call does go
  on to the *other* linked servers, so a server reaches the network through
  one neighbour. Deduplication stops it at its second arrival, exactly as it
  stops a group call.
- **OpenBridge is not offered it.** Only QSP links are. A foreign network gets
  nothing it did not get before.
- **A located radio is not affected.** A radio heard here is delivered here
  and nowhere else. Once the far radio has spoken across the link, this server
  locates it behind the linked server's peer and the call goes there directly.
- **The local Motorola repeaters still hear it.** If no linked server carries
  the call — none ready, or a busy slot towards one — the result is what it
  was before: the "not heard recently" reason with `NoHomebrewDestination`.
  The radio may be on one of those repeaters, and nothing here judged the call.
  When a linked server does carry it, `Reason` is empty and the repeaters are
  offered it anyway.

### Two related corrections

**A private call is not subject to talkgroup attachment.** Its target is a
radio ID, and nothing attaches to a radio ID. With `dmr.subscription.enabled`,
every private call to a located radio was refused as "not attached", and
because that is a judgement the call was kept off the Motorola side as well.
Subscription is opt-in, so this was latent. It would have struck the moment a
radio was learned behind a linked server. A group call still needs its
attachment.

**A linked-server peer is sent every preamble.** The preamble gate of 0443/0445
exists for MMDVMHost, which turns each network preamble announcing data into
fifteen. Another QSP server applies its own gate to its own hotspots and passes
the full set to its Motorola repeaters, which expect what a Motorola radio
sends. The upstream direction of a link already carried all sixteen, so before
this the two directions of one link differed.

## Consequences

- **Both ends need it.** On production the test server is a peer, and on the
  test server production is a link. A private call from each side reaches the
  other only when that side routes it outward.
- **A private call to a radio nobody has heard is sent to every linked
  server.** On a small federation this is a handful of datagrams per burst. On
  a large mesh it is the same fan-out a group call already has, bounded by
  deduplication.
- **Confirmed private data needs its answer to come back.** A Motorola radio
  acknowledges a confirmed text, and the acknowledgement is itself a private
  data transmission to the sender. It takes this same path the other way, and
  by then the sender has been heard, so it is located and delivered directly.
- Composed private texts (ADR-0067 phase 3) are unchanged. They still need the
  acknowledgement path to be built.

## Verification

- `internal/routing/private_link_test.go` covers dialled links, inbound
  servers, both at once, hotspots not being offered the call, no links, a
  server that is not ready, a located radio, a radio learned behind a server,
  no echo to the origin server or link, and a busy link not taking the call
  from the repeaters. The data cases cover a voice header, voice, a CSBK, a
  data header and Rate 3/4 blocks, and the attachment exemption is covered
  both ways. Every deliberate break listed in the file was run and fails.
- `internal/peers/private_link_test.go` runs a private text and a private call
  through a real socket. The linked server receives all sixteen preambles, the
  header and the blocks, and a hotspot that has never carried the radio hears
  nothing.
- `internal/peers/linked_servers_test.go` drives the master's handshake. Only
  peers that registered with a QSP package ID are linked servers.
- On air: pending. Needs 0.1.288 on production *and* on the test server.
