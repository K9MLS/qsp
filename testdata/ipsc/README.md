# IPSC fixtures

**Two, both of one message type in one direction, and no master has ever
replied.**

| File | What it is |
|---|---|
| [`ipsc-phase1-a-id100.pcap`](ipsc-phase1-a-id100.md) | An XPR8300 registering against a host running nothing. 28 requests, ten seconds apart |
| [`ipsc-phase1-b-id3132910.pcap`](ipsc-phase1-b-id3132910.md) | The same, with the Radio ID changed and nothing else. This is what names the peer ID field |

Together they establish the `0x90` registration request, a big-endian uint32
peer ID at offset 1, a flat ten-second retry with no backoff, an asymmetric
source port, and that ICMP unreachable is ignored. They establish nothing about
a reply, keepalives, the peer list, voice, or disconnect, and nine of the
fourteen payload bytes remain unexplained.

`internal/protocol/ipsc` implements exactly that and refuses every other leading
byte with `ErrNotCaptured`.

Motorola IPSC has no published specification. Every open implementation of it
was reverse-engineered from traffic, and QSP has decided to build from a capture
rather than from somebody else's implementation — see
[ADR-0029](../../docs/adr/ADR-0029-ipsc-from-capture.md).

That decision is not free. Deriving from DMRlink or HBlink3 is permitted by
their licence into a GPL-3.0 project and would make QSP's IPSC a derivative work
permanently, owing attribution from the moment the source is read. A capture
avoids that, and a club with a real Motorola repeater is the only thing that can
produce one.

## What is still needed

[`IPSC-CAPTURE-REQUEST.md`](../IPSC-CAPTURE-REQUEST.md) says what to record and
why each part matters; [`CAPTURE-PLAN.md`](CAPTURE-PLAN.md) is the plan for the
equipment this project actually has. In short, everything that requires a master
to answer: the rest of the registration handshake, keepalives over minutes,
voice on both timeslots with terminators, a peer list exchange, which cannot be
seen with one repeater, and a clean disconnect.

**The differential technique is what made the two files above worth more than
one.** Capture the same event twice with exactly one setting changed and diff
them; the field that moved is the field that changed. Two captures differing in
one known way beat ten differing in unknown ways.

A capture placed here gets a sibling `.md` recording its provenance, structure
and sanitization, exactly as the HBP fixtures do. See
[`../README.md`](../README.md).
