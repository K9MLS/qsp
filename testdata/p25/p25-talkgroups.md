# P25 talkgroup capture, 2026-09-11

`p25-talkgroups.pcap` — 5173 packets, md5 `a0fb6bef8e5d10215df618d9249136fd`,
none dropped. **This is the capture that unblocked routing.**

## Why it was needed

`p25-voice.pcap` had seven transmissions and could not answer where the
talkgroup lived, because every one of them was **one radio on one talkgroup**. A
field that never changes is indistinguishable from framing that never changes,
and any claim about which byte meant what would have been a guess — in a routing
field, where a guess sends a call to the wrong place.

## What it contains

**Fourteen transmissions across four talkgroups**, and three reflector hosts,
because in a P25 gateway a talkgroup is a reflector.

| Frame 0x65, bytes 1–3 | Talkgroup | Transmissions |
|---|---|---|
| `0x00039D` | 925 | 1–4 |
| `0x00270F` | 9999 | 5, 11 |
| `0x002A88` | 10888 | 6–10 |
| `0x007BB8` | 31672 | 12–14 |

**Talkgroup 9999 is returned to at transmission 11**, after 10888 had been used.
That is the control the whole capture rests on: without it, a byte drifting with
time would look exactly like a talkgroup.

**Frame 0x66, bytes 1–3 is `0x2FCDEE` in every one of the fourteen** — 3132910,
the transmitting radio's own identifier.

The operator confirmed 925, 9999, 10888 and 31672 are the talkgroups programmed
into the radio, so this is a confirmed decode rather than an observed
difference.

## What is still unknown

**Frames 0x67, 0x68 and 0x69 each hold three constant bytes** whose values are
neither the radio ID nor any talkgroup — `0xF09D6A`, `0x19D426`, `0xE0EB7B`. P25
protects its Link Control with Reed–Solomon and that is the obvious
explanation, but it is a guess and nothing depends on it. QSP carries those
bytes untouched either way.

**The registration handshake.** The capture was started before the gateway, but
a timer restarted the service underneath, so the first exchange may not be in
it. Still outstanding.

**A second radio.** Every transmission is the same one, so the source field is
confirmed as *a* 24-bit identifier matching this radio and not yet proven to
follow a different one.

## What was built from it

`Frame.Talkgroup` and `Frame.SourceID` in `internal/protocol/p25`, read only
from the frames that carry them — a caller asking every frame for the talkgroup
would otherwise get whatever those bytes happen to be in a voice frame, which is
audio.

The sixteen-bit identifier is returned separately from the byte above it, which
was zero throughout. A capture showing it non-zero produces a visible surprise
rather than a talkgroup number sixty-five thousand too large.
