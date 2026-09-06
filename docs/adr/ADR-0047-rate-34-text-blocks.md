# ADR-0047: A text message is Rate 3/4 blocks, and QSP carries them whole

**Status:** Accepted, with one step named as unmeasured
**Date:** 2026-09-06

## Context

Text over IP Site Connect has been built since ADR-0045 and has never carried a
message. The preamble crossed the bridge, the data header crossed, and every
content block was dropped in silence, in both directions. A radio at the far end
saw a header promising blocks that never arrived.

The cause was one sentence in `text.go`: *only the twelve-octet blocks can be
placed in a burst, and a Rate 3/4 burst carries twenty-two octets and does not
fit.* That was written when the text work was done, was correct about the
consequence and wrong about the number, and it went eighteen patches without
anybody connecting it to the symptom. It explains every report: KD9EJA's texts
arrive because his repeater's bursts are in a coding QSP accepts, K9MLS's never
do because the content is dropped, neither radio acknowledges because no
message is ever assembled to acknowledge, and group text on the local repeater
works because it never crosses the bridge.

ADR-0045 had the evidence in it and did not follow it. It records that byte 30
agrees with the low nibble of byte 51 in 153 of 162 frames and that *the nine
exceptions are the 60-byte Rate 3/4 frames, where byte 51 is not the Slot Type.*
Nine frames where a documented offset does not hold was a measurement of a
different layout, filed as an exception.

## What was measured

`testdata/ipsc/ipsc-text-rate34.pcap`, 30 Rate 3/4 datagrams cut from a whole
day's session capture, and the 12 already in `testdata/ipsc/ipsc-text.pcap`.
Forty-two blocks.

### The datagram is six bytes longer and everything after byte 37 moves

The block occupies 38 to 55, byte 56 is zero, byte 57 is the Slot Type reading
`0x48`, and 58 and 59 are the tail. That is the 54-byte layout with six more
octets of block in it, which is why byte 51 stopped being the Slot Type.

### Bytes 32 to 37 are not constant

They read `00 0d 80 0a 00 90` where a 54-byte frame reads `00 0a 80 0a 00 60`.
Byte 37 is 96 against 144, the bit counts of the two information blocks.
Copying the twelve-octet constants would have announced 96 bits of payload in a
datagram carrying 144.

### The block is data first, control last

Sixteen octets of user data, then a seven-bit block serial number and a
nine-bit CRC. Three independent lines of evidence:

- The user-data halves of one transmission's six blocks concatenate into an
  IPv4 datagram of total length 88, protocol 17, from `0c 2f cd ee` to
  `0c 30 25 ad` — Motorola's radio-IP encoding of the two radio IDs in the
  envelope — carrying UDP on port 4007 whose payload reads *"I can't talk right
  now..."*. No other offset produces any of that.
- The serial numbers run 0 to 5, and 0 to 3 in a four-block message.
- The CRC-9 verifies on 42 of 42.

**Clause 8.2.2.2 draws this the other way round**, with the control pair in
octets 0 and 1 and user data in 2 to 17. The two octets themselves are
identical either way; only their position differs.

### The CRC-9 is not the one clause B.3.11 describes

B.3.11 puts the serial first and adds an inversion polynomial. That arrangement
matches none of the 42 blocks under any nine-bit generator. A search over all
256 generators, seven message orderings and both inversions found exactly one
combination that matches every block: B.3.11's own generator,
`x⁹ + x⁶ + x⁴ + x³ + 1`, over the message in the order IP Site Connect presents
it, with no inversion. So the block as it arrives is a plain trailing-CRC
codeword — 135 bits protected by the nine that follow them.

Two hundred and fifty-six candidate readings were tried because the alternative
was arguing about what a 2005 first edition meant.

### The codec that existed was wrong in a way no test could see

`trellis.go` shipped with the constellation amplitudes mapped to dibits as
`+1 → 01, -1 → 00, +3 → 11, -3 → 10`. **Table 10.3 says `01 → +3, 00 → +1,
10 → -1, 11 → -3`**; all sixteen entries were wrong. The wrong mapping is a
permutation of the four dibit values, so encoding and decoding agreed with each
other perfectly and every test passed. Wiring it in as it stood would have put
well-formed bursts on air that no radio could read, with a symptom identical to
the one we started with.

Annex E.3 was read at the same time and confirms the burst layout this package
already had: Slot Type at bits 98 to 107 and 156 to 165, sync at 108, trellis
dibits in plain transmit order.

## Decision

**Carry the eighteen-octet block whole, code it with a trellis, and change
nothing about what a bridge understands.**

- **`internal/dmrfec/rate34.go` owns the block.** `CRC9`, `Rate34Serial`,
  `Rate34OrderOf`, `Rate34Rotate`, `BuildRate34Burst`, `DecodeRate34Burst`.
- **The block's length chooses the coding, not the data type.** Twelve octets
  is BPTC and eighteen is Rate 3/4. The frame's data type is checked against the
  Slot Type in the burst and a disagreement is a refusal, because two encodings
  of one fact disagreeing has cost this project time before.
- **A block of neither length is refused.** Half a message delivered is worse
  than none, because it looks like it worked.
- **A block whose CRC does not verify is still carried.** ADR-0037 settles that
  a bridge carries a payload it does not interpret, and an unconfirmed Rate 3/4
  block has eighteen octets of user data and no control pair at all. Verifying
  the CRC is a diagnostic, never a decision to drop.
- **No TMS parsing anywhere in the product.** The IPv4/UDP/UTF-16 reading above
  lives in a test, where its job is to prove an offset. Reassembling somebody
  else's application protocol on the path of every message is a feature nobody
  asked for.

### The one step that is not measured

**No capture anywhere contains a Rate 3/4 burst as it goes over the air.** The
IPSC captures carry the block already decoded, and every Homebrew frame in
`qsp-session.pcap00` is voice. So whether a hotspot expects the control pair at
the front of the block or the back is unknown, and it decides whether anything
QSP transmits can be read.

This is not settled by argument. It is:

- **named**: `dmrfec.Rate34AirOrder`, one constant, with a test pinned to it;
- **reported**: `DecodeRate34Burst` verifies the CRC-9 both ways round and says
  which arrangement it actually found, and the IPSC listener logs that once per
  transmission as `rate34_block`;
- **cheap to settle**: one text sent from a Pi-Star radio with `tcpdump`
  running makes the journal say the answer.

`Rate34AirOrder` is set to control-first because clause 8.2.2.2 is the clause
that describes the block's layout and MMDVMHost implements the standard. That
is a reason, not a measurement, and this record should be amended the day the
capture exists — as ADR-0041's inferred half was, and ADR-0046's.

## Consequences

**A test that asserted the old belief had to be deleted.**
`TestARateThreeQuarterBurstIsRefusedRatherThanTruncated` passed for eighteen
patches while the network could not send a text. It is replaced by
`TestARateThreeQuarterBurstCrossesTheBridge`, which asserts the burst
round-trips rather than merely that it was built, and by
`TestABlockOfNeitherSizeIsStillRefused`, which keeps the guard the old test was
actually providing.

**Round-trip tests cannot validate a codec against reality.** Encode and decode
share the tables, so a transcription error satisfies a round trip exactly as
well as a correct transcription. Every test in `rate34_test.go` that could pass
with wrong tables says so in its own comment. The tests that do carry weight are
the ones that end at a fact outside this repository: an IPv4 header, a CRC over
somebody else's bytes, a sentence the operator typed.

**ADR-0029 does not apply and never did.** It governs IP Site Connect, which has
no published specification, and forbids reading other people's *implementations*
because of the derivative-work consequence. The DMR air interface is the other
side of the bridge, ADR-0040 settled that it is specified rather than guessed,
and this package already carries Golay, Reed-Solomon and BPTC from the same
document. The tables here are cited by clause and not reproduced in prose.

**Two readings taken by eye were wrong again**, and a test caught both: the
Text Messaging Service header is ten octets rather than twelve, and its text is
UTF-16 little-endian rather than big. That is the tenth and eleventh time.
