# hbp-text-preambles.pcap

**One text message from a hotspot, whole**, and the capture that explained why
one message put two rows in the console.

SHA-256 `88ef548d1ac6a7eb2f324f85b8a8ade70a33c4d0eaeca5e9a6039353e0f69c74`

## Provenance

| | |
|---|---|
| Captured by | K9MLS, Denton TX |
| Date | 2026-09-07, 12:2x UTC |
| Hotspot | Pi-Star / MMDVMHost at `192.168.1.155:42602` |
| Master | QSP 0.1.91 at `192.168.1.247` |
| Sending radio | 3132910, to TG 2 on timeslot 2 |
| Link type | Linux cooked v2 (276) |
| Packets | 82, of which 68 UDP |

The operator sent one group text reading **`K9MLS`**.

## What one text is

| From the hotspot | Data type | Count | Stream IDs | Timing |
|---|---|---|---|---|
| preamble CSBKs | `0x3` | **16** | **sixteen, one each** | 111–122 ms apart, over 1.87 s |
| data header | `0x6` | 1 | one, shared with the blocks | |
| Rate 1/2 blocks | `0x7` | 5 | the same one | 142 ms for all six |

QSP relayed 22 datagrams of kind `0x83` to the repeater.

## What it establishes

**Sixteen preamble CSBKs, each in a stream of its own, carrying opcode 61 and
feature ID 0 — all sixteen, with no other opcode present.** ETSI figure 7.8
puts the CSBKO in the low six bits of the block's first octet and the Feature ID
in the second. TS 102 361-2 holds the table that would name 61; this project
does not have it, so `dmrfec.CSBKPreamble` records what a preamble carries
rather than what the number is called.

**That is why one text was two rows.** The preambles share no stream with each
other or with the message, so the call tracker grouped them into one run and the
message into another — correctly. The longer, more prominent row (1.86 s,
fifteen frames) carried no message at all.

**Naming a preamble by its opcode rather than by its shape in the stream is
what lets everything else keep its row.** A radio check, a call alert and a
**remote monitor** are also single CSBKs alone in a stream, and the last of
those makes somebody's radio transmit without its operator knowing — the one
thing an administrator most needs to see.

**A short message travels in Rate 1/2 blocks.** `K9MLS` is five characters and
fits the twelve-octet blocks BPTC carries, so both codings are in use on this
network: this and ADR-0047's Rate 3/4.

The blocks reassemble into an IPv4 datagram from `0c 2f cd ee` — radio
3132910 — on UDP port 4007, whose payload after the ten-octet Text Messaging
Service header reads `4b 00 39 00 4d 00 4c 00 53 00`: UTF-16 little-endian
`K9MLS`.

## What it does not contain

**No radio check, call alert or remote monitor.** No capture in this repository
holds one, so nothing here demonstrates that those keep their rows — only that
the rule which suppresses preambles does not match them. If one is ever
captured it belongs beside this file.
