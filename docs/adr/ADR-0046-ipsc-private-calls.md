# ADR-0046: A private call over IP Site Connect is `0x81`

**Status:** Accepted
**Date:** 2026-09-05

## Context

QSP had refused the leading byte `0x81` since the IPSC listener was written. It
arrived from both repeaters on the network, in runs lasting as long as somebody
holds a key down, and every datagram was counted as unparsed and thrown away.
**No private call from a Motorola repeater had ever crossed the bridge.**

It hid the way the timeslot defect hid: this network's traffic is group calls on
TG 2, and a member who tries a private call and hears nothing assumes the other
station is not there.

It was found by reading the journal of a running system — the fourth time in two
days — and not by any test, review or capture taken for another purpose.

## What was measured

`testdata/ipsc/ipsc-private-voice.pcap`, captured 2026-09-05: two private calls
in opposite directions between the same two radios, with group calls either
side, through two repeater models. **Source and destination move in opposite
directions between the two private transmissions**, so neither can be confused
for the other, for a constant, or for the envelope's sender ID.

### `0x81` is `0x80` with a radio ID where the talkgroup goes

Diffing a private header against a group header from the same repeater fourteen
seconds apart, the envelope differs at thirteen of its first thirty-eight bytes:
the leading byte, the call counter, the three destination bytes, the stream ID,
and six bytes that vary frame to frame within a single transmission anyway. **The
same diff on the other repeater model differs at exactly the same offsets.**

Frame lengths, the `52 57 57 57 66 57` superframe cycle, the slot bit in byte 17,
the flags, the frame marker in byte 30 and the constants block at 32–37 are
identical between the two kinds.

### Two encodings of the call type, agreeing

Byte 38 of a header or terminator is the DMR Full Link Control opcode:

```
0x80   00 00 00 | 00 00 02 | 2f cd ee     FLCO 0x00, talkgroup
0x81   03 00 00 | 30 25 ad | 2f cd ee     FLCO 0x03, radio
```

`0x00` is Grp_V_Ch_Usr and `0x03` is UU_V_Ch_Usr in ETSI TS 102 361-2. The
leading byte and the FLCO agree on **all 32 header and terminator frames** in the
capture, and they are independent encodings — one Motorola's, one the air
interface's. The destination is carried twice, in the envelope and in the Link
Control, and agrees on all 32, as does the source.

This also settles `Voice.Destination`, which had been marked unverified since it
was written because no capture had ever moved it. This one moves it three ways
in two minutes.

## Decision

**Accept `0x81` as voice, carry the call type with the destination, and add no
configuration.**

- **One kind predicate.** `Kind.IsVoice()` covers both, and every reader of a
  voice frame — header, payload, colour code, slot bit — compares with it. A
  reader left comparing against `KindVoice` alone would refuse half the traffic
  while the rest of the path worked, which is exactly how a whole timeslot of
  audio went missing for a fortnight.
- **The call type is a parameter of the Link Control, not a default.**
  `dmrfec.LinkControlFor` took two addresses and always wrote FLCO 0. A private
  call carrying a group Link Control would tell every receiving radio that a
  conversation between two members is a talkgroup it may join. Making the caller
  say which it is means a new call path cannot inherit the wrong answer by
  saying nothing.
- **No new settings.** A network that carries voice carries private voice.
  Routing already resolves a private destination to a subscriber; that is how
  Homebrew private calls have worked all along, and this reaches it as an
  ordinary `hbp.CallPrivate` frame.
- **Access control applies unchanged**, on the same terms as ADR-0044: a ban is
  a ban on a radio, and a private call is a transmission.

### Why not a separate private-call path

The same reason ADR-0045 gave for text: the envelope is identical, the routing
decision is identical, the access checks are identical, and the only genuinely
new part is one byte of call type. A second path would duplicate five things to
avoid duplicating one.

## Consequences

**The outbound half is inferred, and marked as such.** The capture holds private
calls from a repeater to a master and none the other way, so what a master sends
for one is reasoned from what it sends for a group call together with the three
things this capture shows differ. ADR-0041 built the entire outbound voice path
that way and it matched a real master on 22 of 24 bytes when one was finally
captured — a precedent, not a proof.

The alternative is worse than an inference. Sending a private call as `0x80`
would put a conversation between two members onto a talkgroup, so refusing to
guess is not the safe option here; it is the harmful one.

**Every IPSC call was reported to the console as a group call**, because
`CallViews` hardcoded it while no other kind could reach the code. It now reads
the call type, and a private call gets the badge a private call gets.

**A private call reaching an MMDVM radio is a separate open question.** Private
calls from a hotspot member have been delivered correctly by QSP and never
logged arriving by MMDVMHost, and that is unresolved. This record establishes
what crosses the bridge, not what a radio on the far end does with it.

**Nothing here says anything about private call hang time**, about what a
repeater does when a private call is answered, or about the unseen bytes in the
type space. The capture contains no answering transmission.
