# IPSC fixtures

**There are none, and that is why there is no IPSC implementation.**

Motorola IPSC has no published specification. Every open implementation of it
was reverse-engineered from traffic, and QSP has decided to build from a capture
rather than from somebody else's implementation — see
[ADR-0029](../../docs/adr/ADR-0029-ipsc-from-capture.md).

That decision is not free. Deriving from DMRlink or HBlink3 is permitted by
their licence into a GPL-3.0 project and would make QSP's IPSC a derivative work
permanently, owing attribution from the moment the source is read. A capture
avoids that, and a club with a real Motorola repeater is the only thing that can
produce one.

## What is needed

[`IPSC-CAPTURE-REQUEST.md`](../IPSC-CAPTURE-REQUEST.md) says what to record and
why each part matters. In short: the registration handshake, which happens once
and needs the capture started first; keepalives over minutes; voice on both
timeslots with terminators; a peer list exchange, which cannot be seen with one
repeater; and a clean disconnect.

A capture placed here gets a sibling `.md` recording its provenance, structure
and sanitization, exactly as the HBP fixtures do. See
[`../README.md`](../README.md).
