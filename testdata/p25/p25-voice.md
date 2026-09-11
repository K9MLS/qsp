# P25 voice capture, 2026-09-11

`p25-voice.pcap` — 3938 packets, md5 `8865b5353ee898d0748399b272e9ad95`, taken on
a Pi-Star with `tcpdump -i any -w … udp` while transmitting P25 through a linked
reflector.

**This is the capture `CAPTURE-REQUEST.md` asked for, and it answers most of
it.** It is the fixture behind every assertion in `internal/protocol/p25`.

## What is in it

**Seven P25 transmissions**, from 0.18 to 3.42 seconds, on the loopback path
between P25Gateway and MMDVMHost. One real external flow to a reflector at
`198.51.100.176:41000`, so the P25 side was genuinely linked rather than idle.

**Voice arrives in logical data units of nine frames each**, alternating:

| Frames | Unit | Lengths |
|---|---|---|
| `0x62`–`0x6A` | LDU1 | 22, 14, 17, 17, 17, 17, 17, 17, 16 |
| `0x6B`–`0x73` | LDU2 | 22, 14, 17, 17, 17, 17, 17, 17, 16 |
| `0x80` | terminator | 17, and every byte after the type is zero |
| `0xF0` | keepalive | 11 — the type and a callsign padded to ten characters |

**Every type appears exactly 30 or 32 times.** Nothing was dropped, which is
what makes the lengths above exact rather than typical.

## What it cannot answer, and why

**Which bytes carry the talkgroup and the source radio.** Frames `0x66` through
`0x69` each hold three bytes that are **byte-identical across all seven
transmissions** — that is where the Link Control lives. But every transmission
was one radio on one talkgroup, so a field that never changed cannot be told
apart from framing that never changes.

This is the blocker for routing. QSP can recognise and relay P25 today; it
cannot decide *where a call goes*, because that needs the talkgroup.

**The registration handshake.** The capture begins with the gateway already
running, so it holds steady state and nothing about how that state was reached.
`CAPTURE-REQUEST.md` asked for the capture to start first, and this one did not.

**Anything about malformed input.** A capture shows what a correct
implementation sends. It says nothing about what a P25 radio does with a frame
that is wrong, which is where the IPSC work found its real defects — and that
needed a repeater, not a capture.

## What was built from it

`internal/protocol/p25` identifies a frame, checks its length exactly, and
carries the payload **verbatim**. ADR-0034 says a P25 call crosses QSP without a
vocoder, and that has a consequence: QSP has no reason to understand the inside
of a voice frame, so it does not. Every byte it would rebuild is a byte it could
get wrong.

All 565 frames on the inbound path round-trip byte for byte. The test refuses to
run if the fixture holds substantially fewer, so a wrong flow or a truncated file
fails loudly rather than proving nothing.
