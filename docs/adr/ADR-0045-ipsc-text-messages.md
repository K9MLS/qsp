# ADR-0045: Text over IP Site Connect is DMR data in the voice envelope

**Status:** Accepted — one assumption unchecked, named below
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

**Accepted, and unchecked: a master may owe an acknowledgement.**

The captured transmissions repeat byte for byte apart from sequence and
timestamp — the group text twice, one private text **four times**, another twice,
at four to five second intervals. The obvious reading is that something is
waiting for an acknowledgement that never comes, because QSP parses neither type
and so answers nothing.

**If that reading is right, receiving text is not only decoding — QSP must
reply**, or every text will present as a failure to the sending radio even when
it was delivered. The 34-byte `0x84` frame with marker `0x13`, which carries no
payload and ends a group, is the candidate for what a reply looks like.

This is the one thing in this record that could be wrong, and it is wrong in the
expensive direction: a parser built without it would appear to work in the
journal while every operator's radio reported failure. **It is settled by one
question to the operator — did the radio report the texts as delivered or
failed — and by a capture of a real master relaying a text.**

**Accepted: the outbound shape is inferred, as voice was.** No capture of a
master sending text exists. ADR-0041 built the voice transmit path the same way
and it matched a real master on 22 of 24 bytes when one was finally captured, so
the precedent is good — but it is a precedent, not evidence.

**Gained: a silent failure becomes a visible one.** Whatever else changes, the
outbound path must stop returning nil without a word when handed a frame it
cannot encode. A transmission that vanishes while the journal says nothing is
the failure §7 exists to prevent, and it was in the code the whole time.
