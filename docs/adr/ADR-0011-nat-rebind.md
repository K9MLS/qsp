# ADR-0011: A source address change requires re-authentication

**Status:** Accepted — needs field validation

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

## Consequences

- **A legitimate rebind costs an outage**, bounded by the peer's own retry
  behaviour rather than by `PeerTimeout`: MMDVMHost stops receiving `MSTPONG`
  and re-logs in on its own. Expected to be seconds, **not yet measured against
  a real mobile peer.**
- **Impersonation requires being on-path**, not merely knowing a public radio
  ID. That is a large difference in attacker capability for a small cost.
- Every rejection is logged with both addresses, so an operator seeing a peer
  flap can tell rebinding from an attack.

## What would change this

This is the decision most likely to be wrong in the field, because the trade
depends on how often real peers rebind and how quickly they recover.

**Validation needed:** run a hotspot on a mobile connection through a rebind and
measure the outage. If recovery is slow or rebinding is frequent, the middle
ground is to accept a rebind for a peer that has recently authenticated while
requiring a fresh handshake after longer gaps — but that is not worth building
without evidence that it is needed.

`TestNATRebindRequiresReauthentication` pins the current behaviour so that a
future change is deliberate.
