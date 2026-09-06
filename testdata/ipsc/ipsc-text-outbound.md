# ipsc-text-outbound.pcap

**QSP relaying a text to a remote IPSC peer, taken while it was broken.** It is
kept as the witness for a defect rather than as evidence for a reading: it is
what the far end of the bridge received on 2026-09-06, and it is a text message
with no text in it.

SHA-256 `22b6efa6484cdd503d2551e16b2667adf30e3f69951390d40dd8fe5163260ae3`

## Provenance

| | |
|---|---|
| Captured by | K9MLS, Denton TX |
| Date | 2026-09-06, 17:40 to 17:41 UTC |
| Master | QSP 0.1.84 at `192.168.1.247:50000` |
| Remote peer | `198.51.100.2:50004` |
| Link type | Linux cooked v2 (276) |
| Packets | 42 |

## What it contains

| Direction | Type | Byte 30 | Length | Count |
|---|---|---|---|---|
| QSP → peer | `0x84` private text | `0x03` CSBK | 54 | 33 |
| QSP → peer | `0x84` private text | `0x06` Data Header | 54 | 3 |
| QSP → peer | `0x97` | — | 14 | 3 |
| peer → QSP | `0x96` | — | 14 | 3 |

## What it establishes

**Thirty-three preambles, three data headers, and not one content block.** The
receiving repeater is told a message is coming and how many blocks it has, and
then the transmission ends. A radio at the far end has nothing to display and
nothing to acknowledge, which is exactly what every member reported.

There is no 60-byte datagram anywhere in this capture. That is the whole
defect, visible in one table.

## What it is not

It is not evidence about the shape of a correct outbound Rate 3/4 datagram —
QSP had never built one when this was taken. `ipsc-text-rate34.pcap` carries a
repeater's own, which is what the encoder is measured against.

A capture of this same path taken after the fix should show 60-byte datagrams
with byte 30 reading `0x08`, and would be worth committing beside this one.
