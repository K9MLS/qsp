# ADR-0061: QSP speaks to a transcoder and stops there

**Status:** Accepted — the shape was settled by BLUEPRINT §7 and ADR-0034; this
records it as a decision with the Zello path in view
**Relates to:** [ADR-0034](ADR-0034-p25-is-native.md),
[ADR-0052](ADR-0052-qsp-is-federated.md),
[ADR-0060](ADR-0060-qsp-terminates-the-serial-tunnel.md)

## Context

A DVMEGA DVstick 30 arrives 2026-09-14 and the target is Zello. Research the
day before found the path is four processes: QSP, Analog_Bridge, the dongle,
and a Zello bridge speaking the Channels API. See `docs/ZELLO.md`.

**That looks like the arrangement [ADR-0060](ADR-0060-qsp-terminates-the-serial-tunnel.md)
just rejected**, and the difference matters enough to write down, because the
next person to read these two documents together will otherwise think the
project contradicted itself in two days.

ADR-0060 collapsed the Quantar chain because one of its links — P25Gateway —
spoke a protocol QSP already implemented, and the rest existed only to reach it.
Four processes were carrying a frame QSP could have received directly, and a
two-year-old talkgroup defect had survived in the gap between them.

Here the links are a **vocoder** and a **codec**. Collapsing them means shipping
one, and [ADR-0034](ADR-0034-p25-is-native.md) says QSP does not decode audio
and will not.

## Decision

**QSP speaks AMBE_AUDIO to a transcoder it does not own, and that is the end of
its involvement in the audio.** It never opens the dongle, never links a
vocoder, and never sees PCM or Opus.

What QSP is responsible for:

- **The link**: TLV frames over UDP to Analog_Bridge, carrying the talkgroup
  and the identity of the transmitting station.
- **Capacity**: one dongle is one channel. A second simultaneous transcoded
  call is **refused, with a reason an operator can read**, never interleaved.
- **Policy**: which talkgroup reaches which channel, and per-repeater refusal
  of transcoded audio, off by default and enforced by the routing engine
  (BLUEPRINT §7).
- **Identity**, both directions, so that a transcoded transmission is a
  station in Last heard rather than an anonymous burst.

What QSP is not responsible for: the codec, the dongle, the Zello account, the
Opus stream, or the WebSocket.

## Consequences

**The vocoder stays the operator's hardware, and the liability with it.** A
club that wants no transcoding installs nothing extra and QSP reports the
feature unavailable, which is what PROJECT_MEMORY already records for AllStar,
Zello and EchoLink.

**Capacity has to be visible before it is exceeded.** One channel means the
second caller is refused, and the counter for that must say *why* — which is
the lesson of the COLLISIONS defect of 2026-09-12, where a number rose for a
reason nobody could read. `docs/P25-NETWORK.md` §7 has the same problem for P25
contention; the two should share a vocabulary rather than invent one each.

**A Zello user is not necessarily a licensed operator.** BrandMeister requires
moderated channels for exactly this reason, and their audio reaches RF. The
per-repeater refusal is therefore not a nicety: it is the mechanism by which a
repeater owner consents. **Off by default, always.**

**The Zello Channels API is beta and subject to change**, by Zello's own
documentation. Keeping it four processes away means that churn lands on a
component QSP does not ship.

**And the AMBE_AUDIO frame is not yet known.** Ports and directions are
documented; the bytes are not. Under
[ADR-0029](ADR-0029-ipsc-from-capture.md)'s standing rule that is a capture
job, and the capture is available as soon as Analog_Bridge runs.

## Alternatives considered

**Embed a vocoder.** Refused by [ADR-0034](ADR-0034-p25-is-native.md), and the
reasoning is unchanged: tandem vocoding always sounds worse, licensing is not
QSP's to hold, and audio is king.

**Speak USRP instead of AMBE_AUDIO**, meeting the transcoder on the PCM side.
Refused: it puts QSP one step further from its own frames for no gain, and the
AMBE_AUDIO side is where the talkgroup and the talker identity still exist.
PCM has neither.

**Collapse the chain the way ADR-0060 did for the Quantar.** Refused, and the
distinction is the whole point of this record: there the links spoke a protocol
QSP already had, here they perform work QSP has decided never to perform.
