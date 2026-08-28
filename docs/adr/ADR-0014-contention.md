# ADR-0014: A transmission occupies its origin as well as its destinations

**Status:** Accepted — the reservation key is amended by [ADR-0022](ADR-0022-timeslot-contention.md)

## Context

Two people key up at the same moment on different repeaters bridged to the same
talkgroup. Both transmissions are valid. Both cannot be relayed: interleaving
two streams onto one destination produces audio nobody can understand.

The obvious model reserves each **destination** while a transmission is
delivering to it, and refuses a second transmission's frames at a reserved
destination.

That model has a hole, and a test found it. Consider peers A, B and C, all
bridged on one talkgroup. A keys up: B and C are reserved. B then keys up. B's
frames are refused at C, correctly — but **A's own endpoint was never reserved**,
because A is the source, so B's audio is delivered to A.

The result is worse than either alternative. B's transmission is neither relayed
nor refused; it is fragmented across the network, reaching some listeners and
not others.

## Decision

**A transmission reserves its origin endpoint as well as its destinations.**

A collision is then refused everywhere or nowhere. Exactly one of two people
doubling gets relayed, which is what a master is for.

Two supporting rules:

- **A terminator releases every endpoint the transmission held**, so the next
  person can key up immediately. Waiting out the timeout after a clean unkey
  would make every exchange feel broken.
- **A reservation expires after `StreamTimeout`.** A peer that loses power
  mid-transmission would otherwise hold its destinations until QSP restarted —
  the failure that welds a talkgroup open.

## The subtlety that caused the bug

DMR marks both the voice **header** and the voice **terminator** as sync frames.
Nothing in the frame distinguishes them; only position within the stream does.

The first implementation released reservations on any sync frame, so a
transmission's opening frame immediately freed the destinations it had just
taken, and contention never triggered at all. The same subtlety is handled in
`internal/calls`, and it was still missed here.

`Core.Route` now tracks whether a transmission already holds anything before
deciding whether a sync frame opens or closes it.

## Consequences

- Colliding transmissions are refused whole, and every refusal produces a `Drop`
  carrying an operator-facing reason. Constitution §18 forbids silent drops.
- `BusyCount` includes origins, so a one-destination bridge shows two
  reservations during a transmission. That surprised a test; the count is
  correct and the test was wrong.
- **Not addressed:** priority. Whoever keys up first wins, and there is no way
  for Net Control to pre-empt. That is a reasonable feature and deliberately out
  of scope until somebody asks for it.
