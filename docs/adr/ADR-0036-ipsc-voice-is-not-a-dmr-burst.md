# ADR-0036: IPSC voice is not a DMR burst, and bridging is not a copy

**Status:** Accepted
**Relates to:** [ADR-0029](ADR-0029-ipsc-from-capture.md),
[ADR-0034](ADR-0034-p25-is-native.md)

## Context

QSP already carries DMR voice. HBP delivers a **33-byte burst** — vocoder data
wrapped in the forward error correction and sync pattern a radio put on the air —
and QSP relays it verbatim. That verbatim relay is why the audio on this network
is as good as the radio that produced it, and it is what *audio is king* has been
protecting.

The hope was that IPSC did the same, because if it did then bridging a Motorola
repeater to this network would be a copy: read one envelope, write another, leave
the bytes alone.

`testdata/ipsc/ipsc-probe-voice.pcap` settles it. Fifty-four voice frames from an
XPR8300, and every one carries **19 bytes** where DMR carries 33.

19 bytes is 152 bits. Three AMBE+2 frames at 49 bits each is 147. So IPSC almost
certainly carries the vocoder parameters *without* the FEC and sync that DMR
wraps around them — that last step is arithmetic rather than observation and is
recorded as inference. What is observed is that the payload is 19 bytes in every
frame, that it changes frame to frame the way speech does, and that it is nothing
like 33.

## Decision

**A bridge between IPSC and DMR reconstructs the burst; it does not copy it.**
QSP will have to apply the DMR FEC and sync when carrying IPSC voice toward HBP,
and strip them in the other direction.

**This is not transcoding and must never become transcoding.** The vocoder
parameters cross unchanged. AMBE+2 goes in and the same AMBE+2 comes out; only
the protective wrapper differs. ADR-0034 forbids routing one vocoder through
another and nothing here comes close to it — but the distinction has to be
written down, because "we have to transform the payload" is exactly the sentence
that ends with somebody decoding and re-encoding audio for convenience.

**If reconstruction cannot be made lossless, IPSC voice does not ship.** A
degraded bridge is worse than no bridge: a club that cannot use QSP knows it,
and a club whose audio is quietly worse than their old a commercial DMR server blames the radio.

## Consequences

Bridging costs real work rather than a memcpy, and it is work with a correct
answer — the FEC is specified by the air interface and either produces a burst a
radio decodes or it does not. That is testable against the HBP fixtures without
any equipment: reconstruct a burst from IPSC vocoder bytes, and compare against a
burst carrying the same audio.

Both directions need it. A Motorola repeater receiving DMR-originated audio needs
the wrapper removed, and nothing has yet been captured of a master *sending*
voice to a repeater — the probe never answered a voice frame and the repeater
never asked it to.

The 19-byte layout is not established. Three AMBE+2 frames at 49 bits fits, and
fitting is not knowing. Nothing should be built on the internal layout until a
capture or a reconstruction demonstrates it.

## What this does not decide

Whether QSP bridges IPSC to DMR at all. A club running a Motorola repeater and
nothing else needs no bridge — QSP would be an IPSC master among IPSC peers, and
the bursts never leave the protocol. That case is simpler and may well come
first.
