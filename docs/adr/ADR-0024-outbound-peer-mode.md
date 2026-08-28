# ADR-0024: Outbound peer mode, and the network it must not be used against

**Status:** Proposed

## Context

QSP accepts connections. It cannot make one. That single gap is layer 5 in
ADR-0019's model and the last structural one: every other layer now exists.

It matters because a whole class of systems expects to be dialled rather than to
dial. XLX reflectors, DMR+ servers, IPSC2, and another QSP all sit behind a
master that a peer logs into. QSP can be that master and cannot be that peer, so
none of them are reachable.

The protocol work is small. QSP already implements the master side of the
homebrew handshake against real hardware; the peer side is the same conversation
from the other end — `RPTL`, `RPTK` with the digest, `RPTC` with the station's
configuration, then `RPTPING` for as long as the link lasts. Nothing new has to
be decoded.

*Built 2026-08-28.* The state machine is `internal/protocol/homebrew`, pure and
clock-injected like `peers.Master`, so reconnection and every timeout are
testable without a network. `upstream.PeerLink` drives it over a connected UDP
socket and owns its concurrency: the state machine is deliberately
single-writer, and the reader, the ticker and whichever goroutine is routing a
frame outward all want it. `upstream.Connection` lets one `Set` hold both link
kinds, since OpenBridge and a homebrew peer differ entirely in how they reach
the far end and not at all in what a caller wants from them.

Not yet wired into `cmd/qsp`, so an enabled homebrew upstream still refuses
startup.

**This record exists mostly for two things that are not the protocol**: the
configuration shape, which the admin interface has to be built around, and a
constraint on where the capability may be pointed.

## The constraint, first

[ADR-0018](ADR-0018-openbridge.md) decided that QSP links to BrandMeister over
OpenBridge and **does not log into a BrandMeister master as a homebrew peer**.
BrandMeister's operators define peer bridging as prohibited and ask specifically
that nobody build software without an onboard radio that impersonates those
protocols. QSP is exactly the software they are describing.

Building outbound peer mode does not change that. **A capability existing is not
permission to use it where its use has been refused.** The BrandMeister link
still goes over OpenBridge and still waits on a bridge being granted; this does
not shorten that path and must not be described as if it does.

**QSP does not enforce this.** Detecting a BrandMeister address would mean
carrying one network's hostnames in the codebase, which is the same thing §0
refused for talkgroup lists and for the same reasons: it is one network's data,
it goes stale, and it makes QSP quietly wrong for everyone else. The constraint
is documented where an operator configuring a link will read it, and the
operator is responsible for honouring it, which is the ordinary arrangement
for every other network's terms of service.

## Decision

**Outbound peer mode is a protocol option on the existing `upstreams` block,
rather than a new one.**

An upstream is already "a link to another network". Whether that link is
OpenBridge or a homebrew peer login is how the link is carried, not what it is
for. Both have a name, an enabled flag, an address, credentials in a file, and
lists of talkgroups exported and imported. Splitting them into two blocks would
duplicate all of that and give the admin interface two concepts where one will
do.

```json
"upstreams": [{
  "name": "xlx950",
  "protocol": "homebrew",
  "enabled": false,
  "address": "xlx950.example.org:62030",
  "repeater_id": 3132910,
  "password_file": "/var/lib/qsp/xlx950.pass",
  "identity": {
    "callsign": "K9MLS",
    "rx_frequency": 444625000,
    "tx_frequency": 449625000,
    "colour_code": 11,
    "latitude": 33.2148,
    "longitude": -97.1331,
    "height": 10,
    "location": "Denton, TX",
    "description": "QSP",
    "url": "https://example.org",
    "timeslots": 2
  },
  "export": [{"talkgroup": 9, "timeslot": 2}],
  "import": [{"talkgroup": 9, "timeslot": 2}]
}]
```

`protocol` defaults to `openbridge`, so every existing document keeps working
without an edit and means what it already meant.

### The identity block is not optional decoration

A master a peer logs into expects `RPTC`, and what is in it is what the far end
shows its users. A blank callsign or a missing location makes QSP appear on
somebody else's dashboard as an unidentified station, which is discourteous at
best and, on a network that requires identification, grounds for being removed.

So `callsign` is required when the protocol is `homebrew`. The rest have
defaults, because a reflector does not care about transmit power and an operator
should not have to invent one.

**The repeater ID must not collide with a peer registered locally.** QSP would
then hold one ID meaning two different stations, and a private call to it would
be routable to two places. Validation refuses the overlap rather than leaving it
to be discovered as intermittent misrouting.

### Reconnection is the feature, not an afterthought

A master has peers time out and reconnect on their own. A peer has to do the
reconnecting, and an outbound link that stays down after one network blip is
worth less than no link, because an operator will believe it is working.

The link retries with a bounded backoff, logs the transition in both directions,
and reports its state through the health registry. **A link that has never
connected and a link that has dropped are different things** and must not read
the same: the first is usually a wrong password or address, the second is
usually the far end or the network in between.

## What this does not change

- **Loop prevention already covers it.** `Core.RouteFromUpstream` refuses to
  send a frame arriving from a link back out to any link. That rule was written
  for OpenBridge and applies unchanged, which is what a blunt rule buys.
- **Access control already covers it.** Talkgroups arriving from a link cross
  the same ingress test as anything else, which is the case ADR-0020's double
  check was written for.
- **Contention already covers it.** ADR-0022 keeps a link contending per
  talkgroup rather than per timeslot, because a link is an IP socket rather than
  a radio channel. That holds however the link is carried.

## Consequences

- QSP becomes able to reach XLX, DMR+, IPSC2 and another QSP, which is the
  largest single gap by reach in the parity document.
- **QSP-to-QSP is the interesting case.** Two clubs each running QSP can link
  directly without either asking a third party for anything, which is a thing
  none of the existing options offer them.
- The admin interface can be built against a schema that will not move, which is
  why this record comes before it rather than after.
- Outbound peer mode is not IPSC. IPSC is a different protocol for Motorola
  repeaters and is unaffected by this, though the two are natural companions and
  IPSC2 is reachable through this once it exists.
