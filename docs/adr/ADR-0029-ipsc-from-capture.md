# ADR-0029: IPSC is built from a capture, and the capture is the hard part

**Status:** Proposed

## Context

IPSC is what the repeaters clubs actually own speak — XPR8300, XPR8400,
SLR7500, MTR3000 — and a club with a real repeater cannot use QSP without it.
It is the single largest gap by reach after outbound peer mode, and unlike that
one it cannot be closed by reading a document.

[ADR-0008](ADR-0008-protocol-licensing.md) settled the permission question on
2026-08-27: implement from captured traffic, with reading DMRlink or HBlink3
permitted where captures fall short, because GPL-3.0 into GPL-3.0 is the
arrangement that licence exists to allow.

This record is about the consequence of choosing between those two routes, and
about what a capture has to contain.

## The two routes are not equivalent, and the difference is permanent

**From a capture**, QSP's IPSC implementation is QSP's. It carries the
provenance discipline `internal/protocol/hbp` already uses — each message type
recorded as fixture-derived — and owes nobody anything.

**From DMRlink or HBlink3**, it is a derivative work. That is permitted, and it
is also irreversible: the attribution and the GPL notice are owed from the
moment the source is read, and cannot be undone later by rewriting the code from
memory. A contributor who reads that source has bound the project whether or not
a line is copied.

**So the order matters.** Capture first, read second, and only for what the
capture does not contain. Doing it the other way costs an option that cannot be
recovered.

There is also a community position worth recording, though it does not bind
anyone: the BrandMeister wiki states that IPSC is proprietary and not to be
shared among amateurs, with only Motorola Application Partners entitled to the
information. Several open implementations exist regardless, so the position is
not observed in practice. It is noted here so that a decision to proceed is a
decision rather than an oversight.

## What a capture must contain

A capture is not "some traffic". IPSC has a registration handshake, a peer-list
exchange, keepalives and voice, and a capture missing any of them leaves that
part unimplementable from it.

**The registration sequence**, from a repeater's first packet to it being in
service. This is the part with the most structure and the least chance of being
guessed, and it happens once — so the capture has to start before the repeater
does.

**A peer list exchange.** IPSC peers learn about each other through the master,
which is the mechanism that makes it a site-connect protocol rather than a
point-to-point one.

**Keepalives in both directions**, over at least a few minutes. The intervals
matter as much as the format, and an implementation that guesses them looks
correct until a repeater drops.

**A voice transmission on each timeslot**, ideally two, with the terminator.
`internal/protocol/hbp` was validated by the invariant that a stream begins and
ends with a sync frame; IPSC needs its own equivalent, and one can only be found
in a recording of a real one.

**A private call and a text message**, because those are the paths that failed
on this network for want of testing rather than for want of code.

**A disconnect**, clean if the equipment offers one. `RPTCL` went unimplemented
in HBP for exactly as long as no capture contained one.

### What makes a capture usable

Recorded with `-s0`, so packets are whole rather than truncated. Recorded on the
master's side, so both directions appear. Accompanied by a note of the
equipment, its firmware, and what was done during the recording — a capture
whose contents nobody can explain is a set of bytes rather than evidence.

`testdata/README.md` states the rules a contributed capture must meet, and an
IPSC capture meets the same ones.

## Decision

1. **No IPSC wire-format code is written before a capture exists.** Not a
   parser, not a constant, not a message type. The temptation is to write the
   obvious parts first and fill in the rest later, and the obvious parts are
   exactly the ones that would come from somebody else's implementation.
2. **The capture comes from the club's own repeater**, which is the
   circumstance that makes this project able to take the better route at all.
   Most projects cannot.
3. **Reading DMRlink or HBlink3 is a deliberate, recorded act**, taken only for
   message types no capture contains, noted in the code at the point of use, and
   accompanied by the attribution a derivative work owes.
4. **IPSC is a second protocol, not a variant of the first.** It gets its own
   package beside `hbp` and `homebrew`, and feeds the same routing core through
   the same interfaces an upstream link already uses. Nothing in
   `internal/routing` should learn that IPSC exists.

## Consequences

- **IPSC is blocked on hardware access, and that is the right place for it to be
  blocked.** The alternative unblocks it immediately at the cost of an option
  the project cannot get back.
- A club with a Motorola repeater cannot use QSP until this is done, and should
  be told so plainly rather than left to discover it.
- The capture, once taken, is worth more than the implementation it enables: it
  is the only artefact that lets somebody else verify QSP's IPSC is right, and
  the only one that survives a rewrite.
