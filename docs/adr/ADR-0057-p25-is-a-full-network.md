# ADR-0057: The P25 side is a full network, and it carries Motorola repeaters natively

**Status:** Accepted — decided by the operator, 2026-09-12
**Relates to:** [ADR-0029](ADR-0029-ipsc-from-capture.md),
[ADR-0034](ADR-0034-p25-is-native.md),
[ADR-0040](ADR-0040-the-air-interface-is-specified.md),
[ADR-0043](ADR-0043-qsp-is-the-master.md),
[ADR-0052](ADR-0052-qsp-is-federated.md)

## Context

QSP became a P25 reflector on 2026-09-11, built from three captures taken off
the operator's own Pi-Star. Hotspots reach it through G4KLX's P25Gateway and
nothing in the path decodes audio, exactly as [ADR-0034](ADR-0034-p25-is-native.md)
requires.

What that does not yet do is carry a **Motorola P25 repeater**. The operator
owns a Quantar and a GTR 8000, and a club with either of those cannot use
QSP's P25 side today — which is the same refusal IP Site Connect existed to
end on the DMR side. [ADR-0043](ADR-0043-qsp-is-the-master.md) says QSP is the
master and a club runs no second one; a P25 network that needs somebody else's
software in the path to reach the repeaters in it is not that.

Research on 2026-09-12 found a route that works today and needs no QSP code:
Quantar_Bridge connects a Quantar to the MMDVM reflector protocol through
P25Gateway, and P25Gateway is the far end QSP was already captured against. The
first write-up of that research recommended it and argued *against* QSP owning
the repeater interface itself. **The operator rejected that framing, and was
right to.** Two questions had been collapsed into one:

1. Does QSP carry Motorola P25 repeaters as first-class peers? — a question
   about what QSP is.
2. Which process opens the V.24 serial port? — a question about where a few
   hundred lines live.

An argument about the second was written as an answer to the first.

## Decision

**The P25 side of QSP is a full network, not a bridge to somebody else's.** A
Motorola P25 repeater is a peer of a QSP server in the same sense a Motorola
DMR repeater already is, and the interface it speaks to QSP is QSP's own.

Three consequences follow, and they are separable:

**1. The DVSwitch chain is scaffolding, and it is named as such.** Running
Quantar_Bridge into P25Gateway into QSP is a legitimate first step and is worth
doing: it proves the audio path, it keeps a repeater on the air while the native
interface is built, and it is available before any hardware arrives. It is not
the destination, and the plan says when it comes out. A scaffold with no removal
date becomes the building.

**2. QSP speaks a P25 fixed-station interface of its own.** The published
standard is TIA-102.BAHA-A, the Digital Fixed Station Interface. Whether QSP may
implement from that document is [ADR-0058](ADR-0058-the-p25-fixed-station-interface-is-specified.md),
which this decision depends on and does not pre-empt.

**3. Where the V.24 code runs is deferred to a capture, and nothing is ruled
out.** A site-side element speaking IP back to QSP and QSP opening the serial
device itself are both live options. The first suits a multi-site network and
keeps a UDP daemon free of serial dependencies; the second is fewer moving parts
for a club with one repeater in the same rack. **Both are QSP's own code either
way**, which is the part that matters to this decision and the part the earlier
write-up confused with the choice between them. The question is answered after
the first V.24 capture exists, per the method in §8a: a plausible reading is
tested rather than argued for.

## Consequences

**The Motorola half is now scope, not aspiration.** `docs/P25-PLANNING.md`
records the route; the health report's `p25` entry stops meaning "hotspots
only" once the repeater interface exists.

**Audio is still king and still decides.** Nothing in a P25 voice payload is
inspected on any of these paths, so a Motorola-asserted MFID in a Link Control
Word is a byte QSP copies rather than a byte it has to understand. That is
[ADR-0034](ADR-0034-p25-is-native.md) paying for itself a second time.

**Somebody else's software in the path is a measurement problem, not only an
architectural one.** While the scaffold is up, a defect can live in five
processes and QSP owns one of them. That is tolerable for proving a path and
intolerable as a standing arrangement, because this project's whole diagnostic
method is to log the same fact at two layers and read the gap — and four of
those layers would not be ours to instrument.

**ADR-0029 is unchanged.** Reading anyone's implementation stays forbidden.
Using Quantar_Bridge, P25Gateway or dvmhost as a *peer* is testing against real
software, which QSP has done with Pi-Star and MMDVMHost from the beginning, and
is not reading it.

## Alternatives considered

**Bridge to an existing P25 network and stop there.** Cheapest, and it makes
QSP a client of the thing it is meant to be an alternative to. Refused:
[ADR-0052](ADR-0052-qsp-is-federated.md) makes QSP a federation of sovereign
servers, and a server that cannot carry its own members' repeaters is not
sovereign.

**Transcode P25 to DMR and reuse the DMR core for everything.** Refused by
[ADR-0034](ADR-0034-p25-is-native.md), and the reasoning there is unchanged:
tandem vocoding always sounds worse, and audio is king.

**Wait for the native interface and carry no Motorola P25 traffic meanwhile.**
Refused as a false economy. The scaffold is available now, costs nothing but
configuration, and produces the thing this project finds defects with — a
running system with traffic on it.
