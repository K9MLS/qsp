# ADR-0042: The outbound frame shape is measured, and the colour code belongs to the repeater

**Status:** Accepted
**Date:** 2026-09-02
**Amends:** [ADR-0041](ADR-0041-ipsc-transmit-from-inference.md), which stands

## Context

ADR-0041 built the transmit path from inference because no capture of a master
sending voice existed. It named four assumptions to check if the path proved
silent on air. It did prove silent: 494 frames reached a member's repeater on
2026-09-02 and were ignored.

None of the four assumptions was the reason, and none of them could have been,
because **QSP's own output did not have the shape of an IPSC frame at all**:

| | QSP sent | A repeater sends |
|---|---|---|
| Header / terminator | 33 bytes | 54 bytes |
| Voice frames | 66 bytes, all of them | 52, 57, 57, 57, 66, 57 cycling |

That is measurable without equipment. `ipsc-two-peers.pcap` holds 326 voice
frames from two repeater models transmitting simultaneously, and
`ipsc-probe-voice.pcap` and `ipsc-slot-tg.pcap` hold more. The shape can be
required back from a fixture to the same standard as the BPTC and Slot Type
work, before anybody keys a radio.

## What was measured

Across 326 voice frames and 93 headers and terminators, from two models:

- **Byte 30 is the frame marker**: `0x01` header, `0x02` terminator, `0x8a`
  voice. Headers and terminators are 54 bytes without exception.
- **On a voice frame, byte 31 is `len(datagram) - 32`.** No violations in 302
  frames.
- **The payload class at byte 32 decides the trailer, and so the length**:
  `0x40` sync with no trailer (52 bytes), `0x06` fragment with five (57),
  `0x16` fragment-with-Link-Control with fourteen (66).
- **The cycle is `A f f f L f`** — sync, fragment, fragment, fragment,
  fragment-with-LC, fragment — in every superframe of every transmission from
  both models.
- **The last byte of a trailer is the EMB payload**: colour code in the high
  nibble, LCSS in bits 1 and 2. It decodes to first, continuation,
  continuation, last, single across the cycle, which is independently the order
  `dmrfec.LCSSForPosition` derived from the Homebrew captures. Two protocols,
  two captures, one answer.
- **Bytes 38 to 49 of a header are the twelve-octet Link Control block**: nine
  octets of Link Control and three of Reed-Solomon parity, masked `0x969696`
  for a header and `0x999999` for a terminator. Five headers and terminators
  from two models on two talkgroups reproduce bit-exact from their Link Control
  alone. This is the same block that BPTC(196,96) carries on the air, so
  ADR-0040 applies and `internal/dmrfec` already built it.
- **Byte 51 is the DMR Slot Type**: colour code in the high nibble, data type in
  the low — `0x1` voice LC header, `0x2` terminator with LC.
- **Bit `0x80` of byte 31 is the timeslot**, agreeing with the slot bit in byte
  17 in all 93 frames.

### Two things the handover said that the fixture does not support

**`body[20]` is not a marker.** It was recorded as reading `0x67` on headers and
`0x07`/`0xe7`/`0x87` on the three voice shapes. It is the low byte of the 32-bit
timestamp at bytes 22 to 25, equal to `timestamp & 0xff` in all 326 frames and
taking 48 distinct values. The timestamp advances by exactly 480 per frame, so
within one superframe the low byte falls by `0x20` each time and the first
superframe after the headers really does read those four values against those
shapes. The next superframe does not. **A reading taken across one superframe
looked like a field and was an artefact of the sampling window**, which is the
ninth time a reading taken by eye has been wrong.

**The destination field has moved.** §8f recorded that every captured
transmission read 455, so nothing had ever moved bytes 9 to 11.
`ipsc-two-peers.pcap` contains both `0x000002` and `0x0001c7`, and the Link
Control destination in the same frame moves with it.

## Decision

**Build every outbound frame to the measured shape, take the superframe position
from the burst rather than from a counter, and sign each frame with the colour
code of the repeater it is addressed to.**

### The position comes from the burst

A voice frame's trailer carries a Link Control fragment and an LCSS describing
that fragment. Reading both out of the same incoming burst makes them agree by
construction. A counter running alongside would drift after a lost frame and
produce a frame whose LCSS says "first fragment" over bytes that are a
continuation — two individually correct things that together are a lie, which
§8a names as this project's most common defect shape.

### The colour code is per repeater

A colour code is the DMR air interface's co-channel discriminator. QSP does not
filter on it and does not care what it is — but QSP has to *write* one into
every frame it builds, because the Slot Type of a header and the EMB of a voice
burst both carry it, and there is no value meaning "none".

One colour code for the whole network is therefore the one choice that cannot be
right. `ipsc-two-peers.pcap` has two repeaters transmitting at the same moment on
colour codes 1 and 4. Signing both with either number is wrong for one of them.

So the listener learns each peer's colour code from the frames that peer sends —
it is in every voice frame twice over, in the Slot Type and in the EMB — and
mirrors it back. A peer that has never transmitted keeps `ipsc.colour_code` as
its default. **This is what "all colour codes, from all devices" requires**: not
a filter to remove, but the right number in the field.

### The slot bit's polarity comes from configuration, in both directions

The encoder hardcoded one polarity while `Converter` read
`ipsc.slot_bit_is_timeslot2`. An instance configured the other way would receive
audio on one timeslot and send it back out marked as the other, and both halves
were individually correct. The encoder now reads the same setting.

## Consequences

**Accepted: two bytes are written as zero and are not understood.** Bytes 52 and
53 of a header take 87 distinct values across 93 frames. Seven CRC-16
constructions over eight byte ranges in both byte orders match none; sum-8,
XOR-8 and sum-16 over five ranges match at most two. Byte 52 drifts slowly
within a transmission and holds constant for one remote repeater, which reads
like a measurement taken at the sending repeater rather than anything computed
from the frame. A master relaying somebody else's audio has no such measurement,
so QSP writes zero.

**This is a fifth ADR-0041 assumption and it is the weakest of the five**,
because unlike the other four it cannot be settled by reasoning about the
captures held. If a repeater validates those two bytes, this is the reason it
stays silent, and a capture of a real master is what settles it. The test
asserts the 52 bytes that are derived and deliberately does not assert these two.

**Accepted: the `0x40` bit of byte 31 is set unconditionally.** It is set in 87
of 93 frames. The six exceptions are the second and third repeat headers of an
SLR5700; an XPR8300 sets it on all three and both models set it on every
terminator. Setting it always reproduces both models' first header and every
terminator, and the model that disagrees does so only on frames it has already
sent once.

**Gained: the transmit path can now be wrong in a way a test can see.** Until
this ADR the suite was green while QSP sent a shape no repeater has ever sent.
The four defects deliberately reintroduced to check the new tests — a 14-byte
trailer on every frame, a header with no payload, one wrong Link Control byte,
and inverted slot polarity — are each rejected by name.

**Unchanged: ADR-0029 still governs.** Nothing here was read from another
implementation. DMRlink and HBlink3 remain unread. The DMR air interface pieces
come from ETSI TS 102 361-1 under ADR-0040; the IPSC envelope comes from
captures.
