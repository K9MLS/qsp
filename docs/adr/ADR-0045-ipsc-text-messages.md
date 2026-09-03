# ADR-0045: Text over IP Site Connect is DMR data in the voice envelope

**Status:** Accepted — the one open assumption was checked and disproved; see the amendment
**Date:** 2026-09-03

## Context

Text works between Homebrew peers and has since 0.1.12, with real work behind it:
contention handling so a text is not dropped, one Last-heard entry per message
rather than thirty, and the fix so a text does not present as fifty failed
transmissions.

Over IPSC it does nothing, in either direction, and the operator found this by
testing rather than by reading the code.

**Nothing was broken. It was never in scope.** The IPSC listener handles three
message types — registration, keepalive and voice — and the `ipsc` package
defines seven constants, all of them registration, keepalive or voice. ADR-0041
and ADR-0042 are explicitly about voice frames.

The two directions failed differently, and one of them failed silently:

- **Inbound**, a text arrived as a type the switch does not name, was logged as
  an unrecognised datagram, and incremented the `unparsed` counter.
- **Outbound**, a text reached `SendVoice` like anything else. `Encode` starts by
  extracting a vocoder core, a data burst has none, so it returned nil. No
  frames, no error, no log line. **The transmission evaporated.**

## What was measured

`testdata/ipsc/ipsc-text.pcap`, captured 2026-09-03 with the operator's XPR8300
registered to QSP as peer 999999. One group text and several private texts, sent
from a radio through the repeater. 163 data bursts.

### Two message types, and they are the call type

| Type | Header destination | Payload destination | Meaning |
|---|---|---|---|
| `0x83` | `00 00 02` | `00 00 02` | group text, to a talkgroup |
| `0x84` | `30 25 ad` | `30 25 ad` | private text, to a radio ID |

Both carry the same source. Voice is `0x80`, so data sits immediately beside it
in the type space.

### The envelope is the voice envelope

Type byte, 32-bit sender ID, call counter, source, destination, stream ID, slot
bit, flags, sequence and timestamp are all in the same places, and **bytes 32 to
37 carry `00 0a 80 0a 00 60`** — the same constants block a voice header carries.

Byte 12 reads `01` on every data frame where voice reads `02`.

### Byte 30 is the DMR data type, and two encodings agree

The markers observed are `0x3`, `0x6`, `0x7` and `0x8`: **CSBK, Data Header,
Rate 1/2 Data and Rate 3/4 Data**, which are the ETSI data types for exactly this.

**Byte 30 equals the low nibble of byte 51 in 153 of 162 frames**, and byte 51 is
the DMR Slot Type established by the voice work — colour code high, data type
low. Two independent encodings of one fact, agreeing, which is the same pattern
that validated the voice frame shape. The nine exceptions are the 60-byte
Rate 3/4 frames and one 34-byte frame, where byte 51 is not the Slot Type.

Colour code reads 4 in 150 of 162, matching `ipsc-master-voice.pcap`.

**A text is real DMR data bursts inside the IPSC envelope.** Not a Motorola
invention: the air interface's own data types, wrapped the way voice is.

### Byte 41 is blocks remaining

It counts down — 21, 20, 19 … 6 on the group text; 16, 15 … 2, 1, 0 on a private
one — so a receiver knows how many bursts are still coming.

### Byte 52 is `0x3b`, for the third time

150 of 162 frames, identical to `ipsc-master-voice.pcap`. A third independent
session finding it constant per device strengthens ADR-0042's reading that it is
a measurement rather than derived data, and that writing zero is harmless.

## Decision

**Build text over IPSC in both directions, reusing the DMR data handling that
already exists, and add no configuration.**

- **Two new message kinds**, `0x83` and `0x84`, carrying group and private text.
  The distinction is the call type and belongs in the parsed message, not in a
  separate code path.
- **The existing envelope decoder is reused.** Everything before byte 30 is
  already understood and already implemented; only the payload is new.
- **Access control applies unchanged.** ADR-0044 settled that a subscriber ban is
  a ban on a radio; a text is a transmission and is refused on the same terms as
  voice.
- **No new settings.** A network that carries text carries text.

### Why not a separate data subsystem

The temptation is a parallel path, because data is not audio. It would be wrong
here: the envelope is identical, the routing decision is identical, the access
checks are identical, and the only genuinely new part is the payload. A second
path would duplicate five things to avoid duplicating one.

## Consequences

**A master owes no acknowledgement.** *(Amended 2026-09-03, hours after this
record was written.)*

This section originally read that something was waiting for an acknowledgement
QSP never sent, because the captured transmissions repeat at four to five second
intervals and QSP answers nothing. **Both halves of that reading were wrong**,
and the operator disproved them in two sentences.

The repeats were **the operator pressing send four times**, not a radio retrying.
The four-to-five second spacing was how fast a person works a keypad, and it was
read as a protocol timer.

And **the sending radio reported success** — while QSP dropped all 119 bursts, so
the intended recipient cannot have received anything. The acknowledgement
therefore came from the repeater, on RF, one hop from the radio. **The master is
not part of it.**

So receiving text is decoding, and sending it is encoding. There is no protocol
conversation to hold, and nothing in the outbound path has to wait for a reply.

**The lesson is about the evidence, not the protocol.** A repeating pattern in a
capture looks exactly like a retry timer whether it is a machine or a person, and
this project's whole method is that a reading taken by eye is wrong. Two
questions to the operator were cheaper than any amount of staring at timestamps,
and they should have been asked before this section was written rather than
after.

**Accepted: the outbound shape is inferred, as voice was.** No capture of a
master sending text exists. ADR-0041 built the voice transmit path the same way
and it matched a real master on 22 of 24 bytes when one was finally captured, so
the precedent is good — but it is a precedent, not evidence.

**Gained: a silent failure becomes a visible one.** Whatever else changes, the
outbound path must stop returning nil without a word when handed a frame it
cannot encode. A transmission that vanishes while the journal says nothing is
the failure §7 exists to prevent, and it was in the code the whole time.
