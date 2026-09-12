# ADR-0058: The P25 fixed station interface is specified, and the Motorola framing above it is not

**Status:** Proposed — the decision is the operator's
**Relates to:** [ADR-0029](ADR-0029-ipsc-from-capture.md),
[ADR-0040](ADR-0040-the-air-interface-is-specified.md),
[ADR-0057](ADR-0057-p25-is-a-full-network.md)

## Context

[ADR-0057](ADR-0057-p25-is-a-full-network.md) commits QSP to carrying Motorola
P25 repeaters natively, and depends on this. It is separated because it is a
different kind of question — about where protocol knowledge may come from —
and because `docs/P25-PLANNING.md` has said since 2026-09-06 that it should be
an ADR **before** any P25 code is written rather than a comment discovered
afterwards.

Two documents already divide this ground:

- [ADR-0029](ADR-0029-ipsc-from-capture.md) governs IP Site Connect, which has
  no published specification. It forbids reading DMRlink, HBlink3 or any other
  implementation, because of the derivative-work consequence, and requires
  protocol knowledge to come from captures.
- [ADR-0040](ADR-0040-the-air-interface-is-specified.md) settled the other
  case. The DMR air interface is ETSI TS 102 361-1, a free download, and
  `internal/dmrfec` carries Golay, Reed-Solomon and BPTC(196,96) taken from it
  with a clause citation on each.

So the project already distinguishes a **specification** from somebody's
**code**. The P25 side needs the same line drawn, because the two halves of it
fall on opposite sides.

### The half that is specified

TIA-102.BAHA, *Project 25 Fixed Station Interface Messages and Procedures*
(June 2006, revised as BAHA-A), describes the Digital Fixed Station Interface.
It is the interface commercial converters and third-party consoles already
speak: a RIC-Mz converts Motorola V.24 into DFSI, and dispatch consoles from
several vendors consume it.

This is the exact analogue of TS 102 361-1 — a published industry standard, not
a capture and not an implementation.

### The half that is not

The framing Motorola runs above HDLC on a Quantar's V.24 link has no published
specification. What exists publicly is third-party reverse engineering, notably
the write-up at <https://wiki.w9cr.net/index.php/Quantar_V.24_Interface>, which
originated in work on the p25.ca forum and was done frame by frame with a
protocol analyser — the same method this project uses, a decade earlier.

**That is somebody else's capture, and it is not a standard.** It is also not
somebody's source code, so it is not what ADR-0029 forbids either. It falls in
between, which is why this document exists.

The GTR 8000's IP interface is in the same category: Motorola's manual describes
it as carrying digital voice and data to a Conventional Channel Gateway or a
GCM 8000 Comparator, and no specification for it is published. The clearest
evidence that it is proprietary is that a product exists to translate it into
DFSI.

## Decision, proposed

**Reading TIA-102.BAHA-A is permitted, on ADR-0040's terms.** A claim taken from
it carries a clause citation at the point of use, exactly as `internal/dmrfec`
does.

**Reading anyone's implementation stays forbidden**, unchanged from ADR-0029.
That includes Quantar_Bridge, pnx-mono, dvmhost and the DVSwitch suite, all of
which QSP may talk to and none of which it may read.

**A third-party capture may be read as a hypothesis and never as a fact.** The
w9cr material may be used to decide what to look for and where to point an
instrument. Nothing in it may be implemented until this project has seen the
same thing on its own wire, and a claim carrying only that provenance is marked
unverified in the code that holds it.

**And a claim from the standard is still verified against a capture before code
depends on it.** This is the part that keeps the permission narrow. ADR-0040
already works this way in practice, and §8a records why: a right hypothesis
evaluated by broken arithmetic scores zero, and thirty thousand Reed-Solomon
constructions were searched with the correct parameters inside the search space.
A document being authoritative does not make a reading of it correct.

## Consequences

**It unblocks ADR-0057's native interface** without opening the door ADR-0029
closed. QSP could speak DFSI from the standard and refuse to read a line of
anyone's DFSI code.

**It is the first time protocol knowledge would enter this project from a
document rather than a wire** — which was true of ADR-0040 as well, and is the
reason both deserve a record rather than a habit.

**The V.24 framing still needs a capture**, and no permission changes that.
There is no instrument for a synchronous serial link without converter
hardware, which is why the plan puts the Cisco WIC-1T before any V.24 code.

## Why this is Proposed rather than Accepted

The operator has decided the scope question ([ADR-0057](ADR-0057-p25-is-a-full-network.md)).
This one is about what the project is willing to read, which is his call and
not one to infer from a scope decision. The recommendation above is what the
planning document has carried since 2026-09-06, unchanged; it needs a yes.
