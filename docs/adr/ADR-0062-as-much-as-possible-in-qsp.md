# ADR-0062: As much as possible in QSP

**Status:** Accepted
**Relates to:** [ADR-0034](ADR-0034-p25-is-native.md),
[ADR-0061](ADR-0061-qsp-speaks-to-a-transcoder.md),
[ADR-0060](ADR-0060-qsp-terminates-the-serial-tunnel.md)

## Context

The documented way to bridge DMR to Zello is four processes: MMDVM_Bridge
logging into the DMR network over Homebrew, Analog_Bridge, AMBEserver, and a
Zello client. Installing that alongside QSP was proposed on 2026-09-14 and the
operator refused it:

> I'd rather build our own path into QSP and not have some pieced together
> crap.

And, having seen what the codec requires:

> I understand that there are going to be add-ons to get Zello to work, I just
> want as much as possible built into QSP.

**That is a rule rather than a preference, and it is the one this record
adopts.** A thing leaves QSP only when keeping it would break a property QSP
has decided to hold. Not because a package already exists that does it, and not
because assembling is faster than building.

## Decision

**As much as possible is built into QSP. Two things are not, and each has a
reason that has been tested rather than assumed.**

### What QSP builds

- **The AMBE_AUDIO link.** A UDP client to an AMBEserver, speaking the
  AMBE-3000 packet format. See `cmd/ambe-probe`.
- **Talkgroup to channel mapping**, and the per-repeater permission to accept
  transcoded audio, off by default (BLUEPRINT §7).
- **Capacity.** One dongle is one channel; the second simultaneous call is
  refused with a reason an operator can read.
- **Identity, both directions**, so a transcoded transmission is a station in
  Last heard rather than an anonymous burst.
- **Everything an operator sees**: the console, the counters, the health
  report.

### What is outside, and why

**The vocoder.** [ADR-0034](ADR-0034-p25-is-native.md): QSP ships no codec.
Tandem vocoding always sounds worse, the licence is not QSP's to hold, and one
process may own a serial port. AMBEserver already does that job and serves the
chip on a socket, which is what keeps the operator's hardware available to the
operator.

**Opus, and this one was checked.** Zello's Channels API says the codec field
is required and **must be `opus`** — there is no PCM option. The parameters are
fixed at 16 kHz mono with 60 ms frames; `gD4BPA==` in their own example decodes
to exactly that.

The Go reference client, `jcmurray/monitor`, states the cost plainly: it uses a
Go wrapper around the OPUS libraries, which must be installed on the machine —
`pkg-config libopus-dev libopusfile-dev`. A wrapper around a C library is cgo,
and cgo costs three things QSP holds deliberately:

- `CGO_ENABLED=0`, and with it the single static binary
- cross-compilation to ARM with `GOARCH=arm64 go build` and no C toolchain
- an install with no development headers as a prerequisite

**Trading all three for one optional feature is the wrong trade**, and it is the
same reasoning as ADR-0034 applied to a different codec. If a pure-Go Opus
encoder good enough for voice appears, this part of the decision should be
revisited: nothing else about it is load-bearing.

### Revisited 2026-09-15: the premise expired and the decision stands

**"A wrapper around a C library is cgo" is no longer the whole picture.**
Pure-Go Opus implementations now exist — `tphakala/go-opus`,
`selawe/go-opus-codec`, `kazzmir/opus-go`, `darui3018823/opus` — several
published in mid-2026, all claiming `CGO_ENABLED=0`. The sentence above
invited this revisit and it was carried out by running them rather than
reading about them.

**The decoder is real.** `selawe/go-opus-codec` has no dependencies at all,
builds with `CGO_ENABLED=0`, and publishes a decoder conformance matrix
passing all twelve RFC 8251 vectors at every rate and channel count. Decoding
a libopus-encoded file through it returned peak 6417 and mean |sample| 2217
against an original of 6005 and 2237 — correct.

**The encoder is not.** The same library's encoder, driven at 8 kHz mono VoIP
with the bitrate set to 16 kbps, produced packets its own decoder rendered as
−32768 on every sample. Its own command-line tool refuses anything but 48 kHz,
so the test was repeated there and the output handed to **libopus itself**:
input peak 6149 and mean 2240 came back as peak 32761 and mean 23132. Not an
API misuse and not a sample-rate problem — the reference implementation
decodes those packets as full-scale noise.

None of the others is a candidate either: `pion/opus` is decoder-only by its
own README, and `tphakala/go-opus` and `darui3018823/opus` both describe their
encoders as CELT-only, which is the music mode rather than the speech one.

**So the decision holds, and its reason is now better than the one it was
written with.** It is not that no pure-Go binding exists; it is that **no
pure-Go Opus encoder yet produces packets a standard decoder can use**, and
QSP's Zello direction needs encoding. A premise that was true on 2026-09-14
had expired within a day, and the conclusion survived the test that expiry
prompted.

**What would change it:** a pure-Go encoder whose output libopus decodes at
the right level. The test above is four commands and should be repeated rather
than argued about. The decoder half could already be pure Go today — but
splitting one codec across two processes to save cgo in one direction is worse
than keeping it whole, so both stay outside together.

## Consequences

**Two processes beside QSP, both optional.** A club running plain DMR installs
QSP and nothing else, and always will. The two that remain are the two that
could not come in, rather than the two nobody got round to absorbing.

**The four-process chain is refused.** MMDVM_Bridge exists to turn a DMR
network connection into AMBE frames and QSP *is* the DMR network; Analog_Bridge
is plumbing around a socket QSP can open itself. Both were pure overhead and
both are gone.

**The Zello endpoint needs an operational owner.** Its auth token expires after
about a month, so renewal is a running concern rather than a setup step.
Whatever QSP reports about the link has to distinguish "the transcoder is
down" from "the token expired", because those need different actions from an
operator.

**And the licensing constraint is unchanged.** A Zello user is not necessarily
licensed and their audio reaches RF, which is why BrandMeister requires
moderated channels. The per-repeater refusal stays off by default: a repeater
owner opts in.

## Alternatives considered

**Link libopus and do everything in QSP.** Refused for the three properties
above. It remains the operator's call to make differently, and the reasoning is
recorded here so that reversing it is a decision rather than a drift.

**Install the documented four-process chain.** Refused by the operator, and the
refusal was right: two of the four are overhead that exists only because the
chain was assembled from parts that did not know about each other.

**Keep QSP out of it entirely and ship a bridge alongside.** Refused by the
rule this record adopts. The mapping, the permission, the capacity and the
identity are all things QSP already knows and nothing else does.
