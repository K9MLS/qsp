# ADR-0040: The air interface is specified, and IPSC is not

**Status:** Accepted
**Relates to:** [ADR-0029](ADR-0029-ipsc-from-capture.md),
[ADR-0036](ADR-0036-ipsc-voice-is-not-a-dmr-burst.md),
[ADR-0037](ADR-0037-dmr-fec-is-a-wrapper-not-a-codec.md)

## Context

[ADR-0029](ADR-0029-ipsc-from-capture.md) says nothing is implemented that a
capture does not demonstrate. That rule exists because IP Site Connect has no
published specification: every constant in `internal/protocol/ipsc` was measured
from traffic, and inventing one would be inventing protocol behaviour.

The two sides of this bridge are not alike in that respect. **The DMR air
interface is ETSI TS 102 361-1, a free download**, and the burst a hotspot
expects is specified down to the generator matrix. Applying the capture-only
rule to it would mean deriving, by search, something already written down —
which is not rigour, it is a longer route to the same answer with more
opportunities to be wrong.

That was demonstrated at cost. An attempt to recover the Link Control checksum
by searching thirty thousand candidate constructions against the captures
returned zero matches, and the parameters it was searching over were correct all
along: the field polynomial and the generator roots were both in the search
space, and the polynomial division applying them was wrong. **A wrong hypothesis
scores zero, and so does a right hypothesis evaluated by broken arithmetic.**
The two are indistinguishable from the score, which is a limit of the method
worth writing down beside the method itself.

## Decision

**Where a published standard specifies the DMR side, QSP implements the standard
and uses the captures as the test.** ADR-0029 continues to govern IPSC
unchanged.

The evidence standard does not weaken; it moves. Every value taken from the
standard is checked against real traffic before it is trusted:

| Piece | Source | Check |
|---|---|---|
| Reed-Solomon (12,9), B.3.7 | Table B.18 generator matrix | 28 of 28 bursts verify |
| Data type masks | **measured, not read** | one constant per type, 14 and 14 |
| Slot Type, Golay (20,8), B.3.1 | Table B.11 generator matrix | 28 of 28 rebuilt bit-exact |
| Data sync pattern, Table 9.2 | Table 9.2 | 28 of 28 rebuilt bit-exact |

The masks are the case worth noting. They are documented in later versions of
the standard, and QSP does not take them from there: computing the parity of
each captured burst and subtracting what it carries leaves `0x969696` on every
voice header and `0x999999` on every terminator. **A measurement that agrees
with a published constant is better evidence than the constant**, because it
also proves the construction that produced it.

The generator *matrix* is preferred to the generator *polynomial* wherever the
standard gives both. It is nine multiply-accumulates with no division, and
division is where the earlier attempt went wrong.

## Consequences

Voice headers and terminators can be built, which clause 5.1.2.2 makes
mandatory: a voice transmission **shall** be preceded by a voice LC header, so
what QSP has been emitting was not a valid transmission at all. §8e's assumption
that late entry would cover a missing header was wrong, and TS 102 361-2 says
why — late entry works from an embedded Link Control a receiver can match, and
recovers a talker's identity mid-stream rather than starting a transmission.

Reading a published standard is not reading another implementation. DMRlink and
HBlink3 remain unread and must stay that way; ADR-0008's reasoning is about
derivative works and is untouched by this.

**The rule generalises to P25**, which is also a published standard and is
reached the same way.
