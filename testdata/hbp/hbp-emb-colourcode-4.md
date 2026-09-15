# hbp-emb-colourcode-4.pcap

**The capture that turned the EMB's colour-code axis from a prediction into an
observation.**

| | |
|---|---|
| Taken | 2026-09-15, on the production server |
| Command | `sudo tcpdump -i ens192 -n -s0 -w … host 192.168.1.247 and udp port 62031` |
| Link type | Ethernet (`-i ens192`) |
| md5 | `ad1d8ce57d3fb1835dc19a3d131ebd4b` |
| Size | 193 452 bytes, 1 744 packets |
| Contents | 1 688 DMRD frames, 28 RPTP, 28 MSTP |

## What is in it

Seven transmissions, all radio **3132910** on **talkgroup 2, timeslot 2**, from
two different peers:

| Peer | Address | Colour code | Transmissions | Voice bursts |
|---|---|---|---|---|
| 3132910, the Pi-Star | 192.168.1.155 | **11** | 5 | 240 |
| 3132913 | 192.168.1.1 | **4** | 2 | 168 |

**The second colour code is the point.** Every capture this project held before
this one carried colour code 11 and nothing had ever moved those four bits.

## What it settled: the EMB colour-code axis

`internal/dmrfec`'s EMB generator was recovered by searching all 256
degree-eight generators under a fifteen-bit-plus-parity model, and exactly one
reproduced every captured EMB. But every one of those captures was colour code
11, so **the colour-code axis was predicted by the model rather than
demonstrated** — as `EMB-CAPTURE-REQUEST.md` said at some length.

This capture contains four EMB values at a colour code the model had never
seen, and `EMBFor` reproduces all four:

| Colour code | LCSS 0 | LCSS 1 | LCSS 2 | LCSS 3 |
|---|---|---|---|---|
| 11 | `b01a` | `b269` | `b4ff` | `b68c` |
| **4** | **`411e`** | **`436d`** | **`45fb`** | **`4788`** |

Eight for eight. Two colour codes whose binary forms are `1011` and `0100`
still do not span four bits, so this is not every value — but a generator that
predicted four unseen EMBs correctly on the first attempt is a generator worth
believing.

## What it also settled: the embedded Link Control

The capture's 68 complete embedded Link Control groups — LCSS 1, 3, 3, 2 across
four bursts of a superframe — **decode with `DecodeEmbeddedLC` and verify their
checksums, every one.** Both radios, both colour codes.

Every group carries the same Link Control, and QSP builds it identically:

```
LinkControlFor(2, 3132910, false)  →  00 00 00 00 00 02 2f cd ee
the radio's own LC                 →  00 00 00 00 00 02 2f cd ee
EncodeEmbeddedLC's fragments       →  05060606 0f050303 0f05360a 003a3c39
the radio's own fragments          →  05060606 0f050303 0f05360a 003a3c39
```

FLCO `0x00`, group voice channel user; group address `0x000002`, talkgroup 2;
source `0x2FCDEE`, radio 3132910 — which is exactly what the DMRD headers say.

**So annex B is now a recording rather than a reading.** The interleave, the
eight-by-sixteen matrix, table B.16's Hamming generator and the modulo-31
checksum all reproduce a real radio's bytes.

## Reading EMBs out of a capture: skip burst A

Two values in this file are not EMBs at all, and anything reading the middle 48
bits without filtering will invent colour codes from them:

- **`75f7`**, which decodes as colour code 7, LCSS 2. It appears **exactly one
  burst in six** — 9 of 54, 5 of 30, 15 of 90, 12 of 72, 16 of 96 — because it
  is the synchronisation pattern on burst A of each superframe, which carries
  no embedded signalling.
- **`df5d`**, colour code 13 with the pre-emption bit set. Twice per
  transmission: the voice LC header and the terminator, which also carry sync.

The first pass at this capture reported three colour codes per transmission
because of exactly that, and the sixth-of-all-bursts arithmetic is what gave it
away.

## What is still missing

**No Talker Alias.** All 68 embedded Link Controls are FLCO `0x00`. Both
sources are hotspots, and **MMDVM hotspots do not send Talker Alias** — so this
capture could never have contained one. It needs a MOTOTRBO with Inband Caller
Alias enabled in CPS, keying up directly.

**Colour codes 1, 2 and 8**, which would span the four bits and complete the
observation. The differential in `EMB-CAPTURE-REQUEST.md` still applies: set
the Pi-Star to each, key up, note which.
