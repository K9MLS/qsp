# ADR-0060: QSP terminates the serial tunnel itself, and there is no bridge host

**Status:** Accepted — decided by the operator, 2026-09-12
**Relates to:** [ADR-0029](ADR-0029-ipsc-from-capture.md),
[ADR-0034](ADR-0034-p25-is-native.md),
[ADR-0043](ADR-0043-qsp-is-the-master.md),
[ADR-0057](ADR-0057-p25-is-a-full-network.md),
[ADR-0058](ADR-0058-the-p25-fixed-station-interface-is-specified.md)

## Context

[ADR-0057](ADR-0057-p25-is-a-full-network.md) settled that QSP carries Motorola
P25 repeaters natively and left one question open: which process opens the V.24
serial port. It also kept the DVSwitch chain — Quantar_Bridge, MMDVM_Bridge,
P25Gateway — as scaffolding, to prove the audio path and to act as a reference
to measure against.

Research on 2026-09-12 into the physical build changed both of those.

### The serial port is not QSP's problem on this path

A Quantar's V.24 interface is synchronous RS-232 with bit-stuffing HDLC above
it. The published amateur builds do not convert it: a Cisco router with a
serial card takes the raw frames and carries them over a TCP session using
STUN, Cisco's serial tunnel, which exists because it was built for IBM SDLC and
HDLC descends from it. In `basic` mode everything arriving on one side is
carried to the other, on TCP 1994.

So on the STUN path **nothing opens a serial device.** The router does the
physical layer in hardware and presents a TCP stream. The objection recorded
against QSP owning the repeater interface — that it would put a serial
dependency into a binary whose selling point is having none — does not apply
here at all. A STUN listener is the same shape as everything else QSP already
is.

### And the bridge host was only ever an instrument

The scaffold's remaining justification was measurement: something known-good to
diff against. But `stun route all tcp` accepts any address. Point it at a QSP
server, accept the connection, and the frames arrive — so the capture needs no
Pi, no second virtual machine, and none of the four processes, two of which
cannot be instrumented by this project anyway.

The operator's objection was sharper than the architecture note: relying on
another machine to run software that speaks a protocol QSP already speaks, in
order to reach a repeater QSP is supposed to be the master of, is the wrong
shape. [ADR-0043](ADR-0043-qsp-is-the-master.md) says a club runs one server.

## Decision

**QSP is the master of a Motorola P25 repeater directly.** It terminates the
serial tunnel, reads the Motorola framing, and carries the audio. No bridge
host, no relay chain, and nothing between the router and QSP.

### Built in four phases, each with its own instrument

The order is the order the evidence arrives in, and it is the order the P25
reflector and the IPSC listener were both built in.

**1. The keepalive, captured.** The router's tunnel points at a QSP host and
the bytes are caught with `tcpdump` and a socket that accepts and records.
Instrument: the wireline card's LED, which flashes while the link is down.
Produces the STUN framing and the Motorola keepalive, as a fixture under
`testdata/`.

**2. QSP answers the keepalive**, and the LED goes steady. This is the P25
milestone arriving a second time on a different transport — *the poll is the
registration* — and it is the first moment a Quantar believes it has a link
partner with nothing else in the path.

**3. Voice, captured and parsed.** A handheld into a dummy load. The payload
above the framing is a header, LDU1, LDU2 and a terminator carrying IMBE — the
same logical data units `internal/protocol/p25` already reads from the
reflector side. **QSP is learning a wrapper for frames it already understands**,
which is why this is smaller than it looks.

**4. Relay.** A Quantar transmission reaching a hotspot through the reflector
side, and back.

### Consequences

**No code until there are bytes.** There is no published specification for the
Motorola framing above HDLC, so this is [ADR-0029](ADR-0029-ipsc-from-capture.md)
territory exactly: knowledge comes from captures taken here, never from anyone's
implementation. Nothing in phases 1–4 can begin before a V.24 daughtercard and
a serial card are on the bench. **Estimating it honestly: comparable to the
IPSC listener or the P25 reflector, several sessions rather than one.**

**The scaffold is demoted, not removed from the plan.** It is no longer the
first step. It keeps a repeater on the air during the build, and it is a
known-good far end if phase 3 turns out to need one. If phases 1 and 2 go
cleanly it is never stood up, and ADR-0057's removal date becomes moot.

**Audio is still king.** Nothing decodes an IMBE payload on this path either,
so a Motorola-asserted MFID in a Link Control Word stays a byte QSP copies.
[ADR-0034](ADR-0034-p25-is-native.md) paying for itself a third time.

**A second physical option stays open and is not this.** A USB V.24 converter
feeding a serial device is the other way to reach a Quantar, and it *would* put
a serial dependency in the binary and only work at the repeater site. It is not
excluded — it is simply not what this decision builds, and the STUN path is
preferred because it works for a remote site over a network and needs no new
device handling.

## Alternatives considered

**Keep the bridge host permanently.** Refused. Four processes to reach a
repeater, of which one speaks a protocol QSP already implements, and a defect
could live in any of them while QSP owns one. This project's method is to log
the same fact at two layers and read the gap; four of those layers would not be
ours to instrument.

**Speak DFSI to a commercial converter instead.** Still worth doing, and it is
[ADR-0058](ADR-0058-the-p25-fixed-station-interface-is-specified.md)'s subject
— it reaches a GTR 8000 without learning a proprietary protocol, and it is what
third-party consoles consume. It is a different interface for a different box,
not a substitute for being a Quantar's master.

**Wait for hardware and decide then.** Refused as a false economy in the other
direction: the phases and their instruments can be written now, so that an
afternoon with the hardware produces phase 1 and 2 instead of producing a
question about what to capture.
