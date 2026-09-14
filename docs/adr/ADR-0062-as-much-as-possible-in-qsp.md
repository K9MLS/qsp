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
