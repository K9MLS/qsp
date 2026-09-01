# ADR-0037: The DMR FEC is a wrapper, and QSP may add or remove it

**Status:** Accepted
**Relates to:** [ADR-0034](ADR-0034-p25-is-native.md),
[ADR-0036](ADR-0036-ipsc-voice-is-not-a-dmr-burst.md),
[ADR-0029](ADR-0029-ipsc-from-capture.md)

## Context

[ADR-0036](ADR-0036-ipsc-voice-is-not-a-dmr-burst.md) established that Motorola's
IP Site Connect carries nineteen bytes where the Homebrew protocol carries a
thirty-three byte burst, and concluded that bridging the two means reconstructing
rather than copying. It left open whether reconstruction could be made lossless,
and said that if it could not, IPSC voice would not ship.

It can.

The two protocols differ by exactly one layer. ETSI TS 102 361-1 defines the
burst as 264 bits carrying three 72-bit vocoder frames *including FEC* plus a
48-bit synchronisation field placed in the middle of the burst. The 49-to-72
encoding is the P25 half-rate vocoder specification: a [24,12] extended Golay
code and a [23,12] Golay code protect the twenty-four most sensitive parameter
bits, twenty-five bits are left unprotected because they tolerate errors, the
[23,12] codeword is masked by a pseudo-random sequence keyed on the first twelve
bits, and the result is interleaved.

Homebrew forwards the burst a radio put on the air. IPSC forwards the parameters
alone, because the receiving repeater regenerates the protection before it
transmits. Neither is wrong; they are two answers to what a network should carry.

## Decision

**QSP may add and remove the DMR vocoder FEC, and this is not transcoding.**

The forty-nine parameter bits are never inspected, never decoded and never
re-encoded. They are copied. Only the protective wrapper around them changes, so
the audio a radio reproduces from a bridged transmission is bit-for-bit the audio
the originating radio encoded. ADR-0034 forbids routing one vocoder through
another and nothing here approaches it.

The distinction is recorded rather than assumed, because *"we have to transform
the payload"* is the sentence that ends with somebody decoding audio for
convenience. **A change to `internal/dmrfec` that inspects a parameter bit is
out of scope for this ADR and needs a new one.**

## Why this is not derived work

The burst layout is ETSI TS 102 361-1 and the vocoder FEC is the P25 half-rate
vocoder specification. Both are published standards that anyone may implement.
[ADR-0029](ADR-0029-ipsc-from-capture.md) forbids reading another IPSC
implementation and that stands: none was read for this, and one that surfaced
during research was deliberately not opened.

Nothing here implements a vocoder. AMBE+2 is patented and QSP does not encode,
decode or transcode speech — it moves parameter bits between two wrappers.

## Evidence

Published or not, every claim is checked against captured traffic.
`internal/dmrfec` is tested over the 916 real bursts in
`testdata/hbp/hbp-voice-session.pcap`:

- **2,660 of 2,664 vocoder frames decode with a zero Golay syndrome**, 99%. The
  four that do not carry genuine over-the-air bit errors, and twelve such bits
  were corrected across the capture — the FEC doing its job.
- **884 uncorrupted bursts were stripped to parameters, rebuilt, and compared.
  Zero changed.** The round trip is bit-exact.
- The synchronisation pattern appears in one burst in six, which is what a
  360 ms superframe of six 60 ms frames requires and what confirms the payload
  is two halves around a middle rather than one continuous run.

**The interleave geometry was determined by experiment, not read off a page.**
Three candidate readings were run against 2,748 real frames: one scores 99% and
the others score zero. That asymmetry is the evidence, and it is stronger than a
citation would have been.

## Consequences

The bridge described in ADR-0036 is buildable and provably lossless, which
removes the condition that would have stopped IPSC voice shipping.

It also has a use beyond IPSC. Any future protocol that carries parameters
rather than bursts meets the same wall, and this package is where that is
handled once.

The FEC corrects errors, so a bridged burst is not always identical to the one
captured — it is identical to the one the radio *sent*. That is an improvement
and it means round-trip equality can only be asserted over frames that arrived
undamaged. The test says so rather than quietly skipping.
