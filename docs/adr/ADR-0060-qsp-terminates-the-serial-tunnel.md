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

### What the phases found

**Phase 1, 2026-10-04.** `testdata/quantar/stun-link-request.bin`. The tunnel's
header is seven bytes — `08 31`, a two-byte type, a two-byte length, and one
byte that was 1 with the interface in `stun group 1` — and the router opens
with one thirty-byte message of type 2 that needs no answer. The station does
not send a keepalive: it sends `FD 3F`, an HDLC request to open the link, every
0.51 seconds, and nothing else until it is accepted. **So phase 2 is not
"answer the keepalive" but "accept the link"**, and there is no voice to capture
before it.

**Phase 2, built in 0.1.303.** `internal/v24link` accepts with `FD 73`: the
station's address, and the standard's acceptance with the poll bit carried
back. The control values are ISO/IEC 13239's, a published standard, which is
what lets the reply be written without a capture of one. Everything else is
recorded and not answered. The station's reaction is not yet known.

**Phase 2, finished in 0.1.304.** The station took the acceptance and, 23
milliseconds later, introduced itself: `FD BF 01 03 C2 00 00 00 00 FF` — message
type 1, twice its site number plus one, and `C2` for a Quantar, which is the
published account of this frame confirmed to the byte. Unanswered, it repeated
that three times and started again from the link request, every 1.55 seconds,
97 times (`testdata/quantar/stun-introduction-unanswered.bin`). QSP now
answers with its own introduction in the same shape, as a Quantar at another
site, because the station is set for a repeater on the far end; and then sends
Receive Ready every two seconds. **The reply and the interval are both
reasoned, not captured**: the published reply is from a console interface and
uses another address and type, and the published limit is five seconds with no
interval given. Whether the station accepts either is the next thing to read.

**0.1.304 was ignored, and 0.1.305 opens the link from both ends.** The
station repeated its introduction as though QSP's had not arrived, 65 times
(`testdata/quantar/stun-introduction-ignored.bin`). The published account says
a station whose request is accepted "sends a single UA frame back"; this
station never has, and a UA answers only a request — so the far end in that
account was asking too. QSP now sends its own link request, in the station's
form, until the station accepts it, and introduces itself only once both ends
are open. **This is an inference from one sentence and one silence.** The
alternative it leaves is that the introduction's form was wrong, so the other
form on record — a console interface: address `0B`, type `00`, site 13 — is a
setting, `quantar.present_as`, and not a build.

**It worked, on the first try of that form.** 0.1.305, 2026-10-04 16:34:59 UTC:
the repeater accepted QSP's request 30 milliseconds after QSP accepted its own,
both introduced themselves, and its first keepalive came five seconds later.
The link then stayed open. It sends Receive Ready every 5.01 seconds; QSP's
two-second interval was never tested by a drop. The console form was not
needed for this repeater and stays as a setting.

**Phase 3, built in 0.1.306 from the same capture**
(`testdata/quantar/stun-voice-three-calls.bin`). A transmission is information
frames, control `03`, from address `07`: a start marker `00 02 02 0C 0B …`, a
header in two records `60` and `61`, then voice, then an end marker
`00 02 02 25 0B …` sent twice. **From the record's first byte the voice is the
frame `internal/protocol/p25` already parses** — types `62` to `73` at the same
lengths — so the prediction above held exactly, and the reader is a wrapper
around that package.

One thing the reflector side had not shown: the link control alternates. Every
other voice unit carries the standard word, which names the talkgroup and the
radio, and the ones between carry a Motorola word, manufacturer `90`, whose
bytes in the same places are neither. Frame `64` says which, so it is read
before `65` and `66` are believed.

**Renamed in 0.1.306.** The interface is V.24 and a GTR 8000 has it too, so the
package is `internal/v24link`, the setting `p25_repeaters`, and the console
says Motorola P25 repeaters. Only a Quantar has been on the far end. The
fixtures stay under `testdata/quantar` because a Quantar is what they are of.

**Phase 4, first half, built in 0.1.307: a repeater's calls go out.** To the
gateways, the voice records from their type byte on — which are that
protocol's frames already — and the terminator it expects when the call ends.
To other repeaters, every record as received. Nothing is converted. **One call
at a time**: `p25link.Floor` is shared by the two listeners, the first talker
keeps it until its call ends or it has been silent a second, and anybody else
is counted and not carried. Gateways are one holder among themselves, so what
two gateways do together is unchanged. **Repeater to repeater is built and has
never run**: there is one repeater here.

**Phase 4, second half, built in 0.1.308: a gateway's call goes to the
repeaters.** A gateway's frame is the repeater's voice record already, so it is
given the address and control byte a repeater's own carried, `07 03`, and sent
between the captured start marker and the captured end marker, twice. A call
that stops without a terminator is ended at the repeaters after a second, so
none is left keyed. **This is the direction nobody has captured**: what a
repeater accepts is inferred from what it sends.

**The header is the open question.** A repeater's own call carries one, in two
records, and the second encodes the talkgroup with error correction QSP does
not compute. By default none is sent, on the reasoning that every voice unit
repeats the talkgroup. `send_header` sends the captured one, which says
talkgroup 1, for a repeater that will not transmit without it. If that proves
necessary, computing a true header is the work that follows.

**Timing is not handled.** Frames are sent to a repeater as they arrive. On a
LAN that is every twenty milliseconds; across the internet it will not be, and
a buffer to pace them is expected to be needed and should be built from a
measurement.

**Not in Last heard at 0.1.306, deliberately**: that was ADR-0059's question
and the operator's to decide. He decided it the same day, and a repeater's
calls have been in Last heard since 0.1.309.

**On air with 0.1.308, 2026-10-04.** Quantar to hotspot: clean. Hotspot to
Quantar: the repeater transmitted the call **with no header sent**, which
answers the open question below in the cheap direction. The audio was
incomplete, and the cause was outside QSP: the hotspot shared the Quantar's
frequency, so the repeater heard the same radio twice, once through QSP and
once, badly, through its own receiver.

**Audio is king, and nothing here touches it.** Voice frames are read for who
is talking and answered with nothing. When they are carried, the IMBE inside
them is copied, never decoded (ADR-0034).

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
