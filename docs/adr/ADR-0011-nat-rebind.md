# ADR-0011: A source address change requires re-authentication

**Status:** Accepted. **Amended 2026-08-29 after field validation**, which is
what the last section of this record asked for.

## Context

Raised as R3 in the phase 0 architecture review.

HBP identifies a peer by its repeater ID, which travels in the clear in every
datagram. **Radio IDs are public information**; anyone can look up a callsign's
ID. The source address is the only thing distinguishing the station that
authenticated from anyone else who knows its number.

Peers behind NAT, on mobile networks, or on CGNAT rebind to a new source port or
address without warning, and the peer itself does not know it happened.

Three options:

1. **Trust the repeater ID alone.** Any datagram claiming an ID is accepted from
   anywhere. Rebinding is seamless — and so is impersonation. A single spoofed
   UDP packet moves a peer's session or injects traffic as that station.
2. **Trust the address after authentication, and accept a rebind if the peer
   proves itself again.** A rebound peer is refused until it re-runs the
   handshake, which MMDVMHost does automatically once its keepalives stop being
   answered.
3. **Never accept a rebind.** The peer is stuck until its registration expires.

## Decision

**Option 2.** Every message after authentication is checked against the address
the peer registered from. A mismatch is dropped with an explanation. To move,
the peer must complete `RPTL` → `RPTK` → `RPTC` again from the new address.

Three supporting rules follow from the same reasoning:

- **The digest is bound to the address the challenge was issued to.** The salt
  and digest both cross the wire in the clear, so an observer could otherwise
  replay a captured `RPTK` from anywhere.
- **A salt is spent once.** It is cleared on successful authentication, so a
  replayed `RPTK` finds no challenge to check against.
- **An unauthenticated login does not destroy an existing registration.** A
  stranger sending `RPTL` for a known ID gets a challenge, but the working
  peer's announced identity survives until someone actually authenticates.
  Otherwise one forged packet would be a denial of service.

## Amendment: a rebound peer is told, not left to work it out

*2026-08-29.* The field measurement this record asked for arrived, from an
ordinary hotspot on an ordinary home router rather than a mobile peer.

**It rebinds every eight to nine minutes, all day.** The router's NAT mapping
changes, the source port with it, and QSP dropped every keepalive in silence.
DMRGateway logged `Login to the master has failed, retrying login` on that cycle
from the moment it first connected, and nobody noticed because the reconnection
worked.

The decision below stands: a rebound peer must authenticate again, and the
security argument for it is unchanged. **What was wrong was the silence.** The
consequence section estimated the outage as "bounded by the peer's own retry
behaviour" and expected seconds. That retry behaviour turned out to be the
peer's own timeout, during which the network is dead for that member.

A mismatched keepalive is now answered with `MSTNAK`, which is exactly what an
*unregistered* keepalive already received and for the same reason: it tells the
peer to log in again rather than leaving it to discover this. The peer still
completes `RPTL`, `RPTK` and `RPTC` from the new address before passing any
traffic.

**The cost is a reflection vector, and it is small.** A spoofed ping provokes
an `MSTNAK` to the spoofed address. But `MSTNAK` is smaller than the ping that
provokes it, so there is no amplification, and QSP already answered
unregistered keepalives this way. A test asserts the answer stays smaller
than the request, so the trade cannot quietly become a worse one.

## Consequences

- **A legitimate rebind costs an outage**, now bounded by one keepalive interval
  rather than by the peer's own timeout, because the peer is told immediately.
- **Impersonation requires being on-path**, not merely knowing a public radio
  ID. That is a large difference in attacker capability for a small cost.
- Every rejection is logged with both addresses, so an operator seeing a peer
  flap can tell rebinding from an attack.

## What would change this

This was the decision most likely to be wrong in the field, and it was half
wrong: the rule was right and the handling of it was not. Rebinding turned out
to be far more common than expected — every nine minutes on a home router, not
an occasional mobile-network event — which makes the recovery path matter far
more than the rule.

**Validation needed:** run a hotspot on a mobile connection through a rebind and
measure the outage. If recovery is slow or rebinding is frequent, the middle
ground is to accept a rebind for a peer that has recently authenticated while
requiring a fresh handshake after longer gaps — but that is not worth building
without evidence that it is needed.

`TestNATRebindRequiresReauthentication` pins the current behaviour so that a
future change is deliberate.
