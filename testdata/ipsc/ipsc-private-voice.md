# ipsc-private-voice.pcap

**The capture that named `0x81`** — a whole message type QSP had refused since
the IPSC listener was written, carrying every private call on the network.

SHA-256 `ef1f097db292132ac0266469533f85b0f23155d86237f182231ee2fff355285f`

## Provenance

| | |
|---|---|
| Captured by | K9MLS, Denton TX |
| Date | 2026-09-05, 12:54–12:56 UTC |
| Master | QSP 0.1.61 in production, `192.168.1.247:50000` |
| Peer A | XPR8300, radio ID 999999, `192.168.1.233`, K9MLS |
| Peer B | SLR5700, radio ID 315544, `203.0.113.60`, KD9EJA |
| Radios | K9MLS 3132910, KD9HDR 3155373 |
| Capture | `tcpdump -i any -s0 'host 203.0.113.60 or host 192.168.1.233'` on the VM |
| Packets | 1004 records, 998 UDP, over 120 seconds |

Both repeaters were registered to the production listener. QSP's own relayed
frames are in the file as well, sourced from `192.168.1.247` with sender ID
3132911, which is the master's ID.

| Leading byte | Datagrams |
|---|---|
| `0x80` | 720 |
| `0x81` | 236 |
| `0x83` | 3 |
| `0x85` | 5 |
| `0x96` / `0x97` | 17 / 17 |

## What was varied

**Two private calls in opposite directions, with group calls either side.** The
same two radios were both source and destination, so source and destination move
in opposite directions between the two private transmissions — one experiment
moving two fields in a way that cannot be confused.

| # | UTC | Sender | Kind | Source | Destination | Frames |
|---|---|---|---|---|---|---|
| 0 | 12:55:06 | 315544 | `0x80` | 3155373 | TG 2 | 136 |
| 1 | 12:55:24 | 315544 | **`0x81`** | 3155373 | **3132910** | 148 |
| 2 | 12:55:52 | 315544 | `0x80` | 3155373 | **TG 101** | 136 |
| 3 | 12:56:13 | 999999 | **`0x81`** | **3132910** | **3155373** | 88 |
| 4 | 12:56:27 | 999999 | `0x80` | 3132910 | TG 2 | 88 |

Transmission 2 was intended to be a private call to a second radio ID and was
keyed as a group call to talkgroup 101 instead. It is kept: it moves the
destination within `0x80`, which is the control the other four needed.

**Neither `0x81` transmission was relayed.** Every `0x80` transmission in the
file appears twice, once from its repeater and once from QSP; the two `0x81`
transmissions appear once each and go no further.

## The finding

**`0x81` is a private voice call. It is `0x80` with a radio ID where the
talkgroup goes.**

Diffing the header frame of transmission 3 against transmission 4 — same
repeater, fourteen seconds apart — the envelope differs at thirteen of its first
thirty-eight bytes, and every one of them is accounted for:

| Offset | What | Why it differs |
|---|---|---|
| 0 | Leading byte | `0x81` against `0x80` |
| 5 | Call counter | Increments per transmission |
| 9–11 | Destination | A radio ID against a talkgroup |
| 15–16 | Stream ID | Per transmission |
| 20–25 | — | Varies frame to frame within one transmission too |

The same diff on KD9EJA's repeater, transmission 1 against transmission 0,
differs at **exactly the same thirteen offsets**. Nothing structural changes.

Frame lengths, the `52 57 57 57 66 57` superframe cycle, the slot bit in byte
17, the flags at 18–19 running `0x80dd` / `0x805d` / `0x805e`, the frame marker
in byte 30 reading `0x01` on a header, `0x8a` on voice and `0x02` on a
terminator, byte 12 reading `0x02` for voice, and the constants block at 32–37
are all identical between the two kinds.

### Two encodings of the call type, agreeing

Bytes 38 onward of a header or terminator are the DMR **Full Link Control**, and
its first byte is the FLCO:

```
0x80   00 00 00 | 00 00 02 | 2f cd ee     FLCO 0x00, talkgroup 2, source
0x81   03 00 00 | 30 25 ad | 2f cd ee     FLCO 0x03, radio 3155373, source
```

`0x00` is Grp_V_Ch_Usr and `0x03` is UU_V_Ch_Usr in ETSI TS 102 361-2. **The
leading byte and the FLCO agree on all 32 header and terminator frames in the
file** — 24 group against `0x00`, 8 private against `0x03` — and they are
independent encodings, one Motorola's and one the air interface's.

**The destination is carried twice and agrees both times.** The envelope at 9–11
and the Link Control at 41–43 match on all 32 frames, as do the two copies of
the source at 6–8 and 44–46.

This is the same shape of evidence that settled the voice frame — byte 30 and
the low nibble of byte 51 agreeing — and the text bursts before it.

## What this capture does **not** establish

**Nothing about a master sending a private call.** Both `0x81` transmissions are
repeater to master. The outbound shape is inferred from `0x80`, exactly as
ADR-0041 inferred outbound voice, and that inference has a precedent rather than
a proof.

**Nothing about private call hang time, or what a repeater does when a private
call is answered.** No answering transmission was keyed.

**Nothing about `0x82`, `0x86`, or any other unseen byte.** The type space
plainly has more in it than the eight this project has captured.

**Nothing about whether an MMDVM radio will ring** on the far end. QSP delivering
a private call correctly and a hotspot presenting it are separate questions, and
the second one is open for private calls from Paul already.
