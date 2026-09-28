# ADR-0067: QSP originates a text message, and that is a capability it does not have

**Status:** Accepted — phase 1 complete for both block formats, packet CRC included, 2026-09-27; phase 2 is next
**Date:** 2026-09-27
**Relates to:** [ADR-0029](ADR-0029-ipsc-from-capture.md),
[ADR-0045](ADR-0045-ipsc-text-messages.md),
[ADR-0047](ADR-0047-rate-34-text-blocks.md),
[ADR-0034](ADR-0034-p25-is-native.md)

## Context

The operator asked what to build after APRS, and answered his own question with
three things: a talkgroup that replies with the time and the local temperature,
weather alerts sent as text to subscribers, and bulletins from net controllers
and administrators. They are one mechanism, not three features, and that
mechanism rests on something QSP cannot currently do.

### Text works, and every bit of it is relay

Text over IP Site Connect has been built since [ADR-0045](ADR-0045-ipsc-text-messages.md),
corrected by [ADR-0047](ADR-0047-rate-34-text-blocks.md), and is confirmed on
air. But look at what the code actually does:

- `ConvertText`, in `internal/ipscbridge`, takes an `ipsc.Message` and produces
  `hbp.Data`. It **translates a message somebody else composed.**
- `internal/parrot` has a text test whose entire subject is that the parrot must
  **not swallow** a text addressed to its number. Its correct behaviour is to
  leave the message alone.
- `internal/dmrfec/talkeralias.go` encodes UTF-16BE, which is the nearest thing
  in the tree to composing a payload, and a Talker Alias is a different
  container carried inside a voice superframe.

**Nothing takes a Go string and produces a DMR text message.** Grepping for an
encoder finds none. So the honest position is that QSP has never put a sentence
of its own on a radio's screen, and three proposed features all assume it can.

This is the pattern §8a names as "build the complete thing": the named half
exists — carrying text — and the called half does not.

### Why an acknowledgement will not tell us it worked

`internal/parrot/text_test.go` records something that matters here more than it
did there: *a radio still reported success, because the repeater acknowledges on
RF one hop away and a master is not part of that.* A sender's radio saying
"delivered" is evidence about one RF hop, not about QSP, and not about the
recipient.

So there is no acknowledgement path QSP can trust, which decides the instrument:
**the only proof that origination works is a message visible on a radio's
display.** That is the same shape as every other milestone in this project —
the wireline LED, the hotspot hearing a Motorola repeater, MMDVMHost decoding a
Talker Alias.

## Decision

**QSP originates text messages, and it is built in phases, each with its own
instrument. No feature that depends on origination ships before phase 3.**

**1. Composed against a capture.** Build a message and diff it byte for byte
against a real one. `testdata/ipsc/ipsc-text-rate34.pcap` and
`testdata/ipsc/ipsc-text.pcap` already hold forty-two real blocks from real
radios, which ADR-0047 measured in detail — preamble, data header, Rate 3/4
content blocks, and the 60-byte layout where everything after byte 37 moves.
Instrument: a differential against bytes this project already owns. Produces the
encoder, and nothing is transmitted.

**2. On a hotspot.** Send to a Pi-Star and read the screen of a radio on it.
Instrument: a display. This is the first moment anyone knows the encoder is
right, and it is cheap — no Motorola hardware involved.

**3. On a Motorola repeater, group and private.** Both, because ADR-0045's
defect was that the private case behaved differently from the group case and
lost messages silently. Instrument: a display again, and KD9EJA's SLR5700 as the
second model.

**4. Limits measured, not assumed.** The usable length, the character set and
the behaviour on overflow, per protocol and per radio model. §8s says it
plainly: test with what the far side renders, not with what QSP encodes. The
Opus decoder sized for QSP's own 60 ms packets failed on every real Zello
packet; a formatter sized for what the specification implies will fail the same
way.

## What phase 1 found, 2026-09-27

**Phase 1 is done for both block formats, and the second one was a surprise.**

The private captures are Rate 3/4 **confirmed** blocks: sixteen octets of user
data, a seven-bit serial and a CRC-9 each. A calibration capture taken to feed
a CRC search turned up the other half of the story — **a group text goes out as
Rate 1/2 unconfirmed blocks**, twelve octets of plain user data with no serial
and no CRC-9, and three further differences nothing here knew about:

| | private | group |
|---|---|---|
| blocks | Rate 3/4, confirmed | Rate 1/2, unconfirmed |
| destination address | `0x0c` + 24-bit radio ID | **`0xe1` + 24-bit talkgroup** — 225.0.0.2 |
| TMS header octet 2 | `0xe0` | **`0xa0`** |
| IP time to live | 64 | 1 |
| data header octet 9 | send sequence, fragment-last | `0x00` |

`internal/tms` as first written would have **refused every group message on
this network**, and nothing noticed because no fixture held one. That is the
"build the complete thing" failure caught by a capture rather than by a user,
which is the cheap way round.

Both formats now round-trip octet for octet against their captures, which is
what closes phase 1.

**A correction, recorded rather than edited away.** The first write-up of this
capture said the radio had autocapitalised "AAAA" into "Aaaa" and concluded that
text entry on a radio is not literal. The operator typed "Aaaa". The radio
transmitted what it was given, the inference was invented to explain a
difference that was not there, and the conclusion would have shaped ADR-0068's
command grammar around imaginary behaviour. The leading space on one message
stays recorded as observed and unexplained.

**And the packet CRC is provably a CRC.** The calibration messages were sent
one character apart so that same-length pairs would exist, because for equal
lengths a CRC's initial value and output mask cancel:
`crc(A) ⊕ crc(B) = R(A ⊕ B)`. Three pairs came out of it, and the third's
target equalled the first two XORed **exactly** — so the trailer is
GF(2)-linear in the message and cannot be a keyed hash, an additive checksum
or anything that carries. With two of the five unknowns eliminated rather than
guessed, twelve polynomials against both input reflections, both output
reflections, both stored byte orders and fourteen regions still match none of
the pairs. The parameters are unknown; the shape is not.

**That proof of linearity proved nothing, and is withdrawn.** The three
same-length pairs came from three messages of one length — "Aaa ", "Aaab" and
"Baaa" — so they were A–B, B–C and A–C, and the third target equals the first
two XORed for *any* function of the message: f(A)⊕f(B) ⊕ f(B)⊕f(C) is
f(A)⊕f(C) by arithmetic alone. The conclusion happened to be right. The
evidence for it is the solve below, not that test. It is recorded here and in
the changelog rather than edited out, for the reason the "Aaaa" correction
above gives: a wrong proof left standing is trusted by the next person to read
it. A linearity test needs four messages of one length, so that some pair
shares no member with the others.

## What solved the packet CRC, 2026-09-27

**The CRC is CRC-32 with the polynomial the ETSI clause names, and the octets
are taken in swapped pairs.** In full: polynomial `0x04C11DB7`, not reflected,
initial value zero, no output mask, over the whole user-data stream less the
CRC itself — the datagram and its pad — with octets fed in the order 1, 0, 3,
2, … and the result carried least-significant octet first.

The first search had that polynomial and that region in its list — "the whole
user-data stream less the CRC itself" is its eighth region. **What neither
search tried was the octet order**, and CRC RevEng does not try
it either, so the handover's next step would have found nothing and pointed at
the region, which was right all along.

**It was solved rather than searched.** The earlier searches listed polynomials
and tested each one. This one used the property the calibration capture was
designed around: with the initial value and mask cancelled, the polynomial must
divide D(x)·x³² + R(x) for every same-length pair, so the greatest common
divisor over the pairs *is* the polynomial if a CRC-32 exists for that
arrangement of the bytes. The search was then over arrangements alone — region
start, four octet orders, both reflections, four stored byte orders — and
exactly one gave a degree-32 divisor. The pairs came from treating the whole
user-data stream as the region, which gives four messages of one length and
two of another: seven same-length pairs, four of them independent. Of the four
usual pairs of initial value and mask, only zero and zero then reproduced all
six samples. `scripts/crc-solve.py` repeats all of it from the fixture.

**It was checked against captures it was not found from.** Ten Rate 3/4
private transmissions in `ipsc-text.pcap`, `ipsc-text-rate34.pcap` and
`ipsc-text-rate34-out.pcap` — a different block format, different radios,
different days — reproduce to the octet. And the one block in the outbound
capture whose CRC-9 fails, put back in its message, fails the packet CRC too.
`TestThePacketCRCHoldsForEveryCapturedTransmission` and
`TestThePacketCRCRefusesTheCorruptBurst` in `internal/tms` hold both.

**Phase 1 is now complete to the last octet.** `dmrfec.JoinPacket` computes the
CRC instead of carrying the captured one across, and both round-trip tests
compare every octet. For Rate 1/2 the packet CRC is also the only integrity
check the format has, and `dmrfec.VerifyPacket` is what a receiver would call.
Phase 2 — a composed message read off a radio's screen through a hotspot — is
unblocked.

## Consequences

**The formatter's limits become a measured fact with a fixture, not a constant
somebody chose.** Whatever number phase 4 produces goes in a test, because the
next person to change the formatter will otherwise re-derive it by guessing.

**Audio is king still applies, and it argues for rate limits as a requirement.**
A text costs a data burst on a timeslot that people are trying to talk on. A
service that answers a hundred commands during a net is competing with the net.
Every feature built on this gets a visible, configurable cap —
[ADR-0034](ADR-0034-p25-is-native.md)'s rule that audio outranks features, with
§6c's rule that the limit must be visible rather than silent.

**This is [ADR-0029](ADR-0029-ipsc-from-capture.md) territory and the captures
are already here.** The encoder is built from `testdata/`, never from reading
another implementation. That the fixtures exist is why phase 1 costs a session
rather than a week.

**Both protocols, together.** Homebrew and IPSC, in the same phase, because the
recurring defect in this project is shipping the half that was named and
forgetting the half that gets called.

## Alternatives considered

**Round-trip a template through the relay path.** Take a captured message,
substitute the payload, and let the existing conversion carry it. Refused as a
fake under §7's "no fake anything": it would work for messages the same length
as the template and mislead about everything else, and the first real weather
alert would be the test.

**Answer only where a message arrived — an echo, like parrot.** That is genuinely
simpler and it is half the requirement. It cannot push a weather alert to a
subscriber, cannot send a bulletin, and cannot report anything on a schedule. It
also does not avoid the work, since an echo of QSP's own words is still
origination.

**Say it with audio instead.** A generated voice announcement avoids the encoder
entirely. Refused: it burns the vocoder, keys every repeater on the talkgroup,
and spends the audio path on automation rather than on people talking. Automated
content on an amateur network stays textual.
