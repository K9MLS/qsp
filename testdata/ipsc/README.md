# IPSC fixtures

**Six captures, seven message types, and two Motorola repeaters that registered
to each other over the internet on 2026-09-01.**

| File | What it is |
|---|---|
| [`ipsc-phase1-a-id100.pcap`](ipsc-phase1-a-id100.md) | An XPR8300 registering against a host running nothing. 28 requests, ten seconds apart |
| [`ipsc-phase1-b-id3132910.pcap`](ipsc-phase1-b-id3132910.md) | The same with the Radio ID changed and nothing else. This is what named the sender ID field |
| [`ipsc-rehearsal-two-peers.pcap`](ipsc-rehearsal-two-peers.md) | Registration requests from **two different repeaters**, whose bodies differ. This is why nothing past the sender ID is enforced |
| [`ipsc-phase2-master-not-bound.pcap`](ipsc-phase2-master-not-bound.md) | A failure kept on purpose: a master serving 50001 while advertising 50000 |
| [`ipsc-phase2-registration.pcap`](ipsc-phase2-registration.md) | **Two repeaters registering over the internet.** Six-packet exchange, seven message types, the reply to `0x90` |
| [`ipsc-phase2-established.pcap`](ipsc-phase2-established.md) | Twenty-four minutes of a settled link doing nothing |

Together they establish seven message types, an envelope of one type byte and a
big-endian sender ID, a ten-second retry when unregistered and a fifteen-second
keepalive when registered, an asymmetric source port, and that ICMP unreachable
is ignored. They establish nothing about voice, private calls, text or
disconnect, and thirty-nine of `0xf1`'s forty-four bytes remain unexplained.

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
