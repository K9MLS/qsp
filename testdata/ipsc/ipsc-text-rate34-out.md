# ipsc-text-rate34-out.pcap

**QSP relaying a text message with its content in it.** The counterpart to
`ipsc-text-outbound.pcap`, which is the same path recorded eight hours earlier
and carries thirty-three preambles, three data headers and nothing else.

SHA-256 `e960dd389dbfc9250270d89bc56be4cc36281b89ad8a6c88a0bd6ff36fbfc2d5`

## Provenance

| | |
|---|---|
| Captured by | K9MLS, Denton TX |
| Date | 2026-09-06, 22:47 UTC |
| QSP version | **0.1.85**, the first build with ADR-0047's work in it |
| Repeater | XPR8300, radio ID 999999, `192.168.1.233`, colour code 11 |
| Hotspot | Pi-Star / MMDVMHost at `192.168.1.155` |
| Link type | Linux cooked v2 (276) |
| Packets | 374 |

## What it contains

| Direction | Kind | Length | Count |
|---|---|---|---|
| hotspot → QSP | DMRD `0x8` Rate 3/4 | 55 | 12 |
| QSP → repeater | IPSC text, byte 30 `0x08` | **60** | **12** |
| QSP → repeater | IPSC text, byte 30 `0x03`/`0x06` | 54 | 150 |
| QSP → hotspot | DMRD `0x3`, `0x6`, `0x7` | 53 | 30 |

**Twelve in, twelve out.** Before 0243 the second row was zero.

## What it establishes

Every one of the twelve outbound datagrams reads back through
`ipsc.Message.AsText` as an eighteen-octet block. Byte 30 is `0x08`, bytes 32
to 37 are `00 0d 80 0a 00 90`, byte 56 is zero, and byte 57 is `0xb8` — colour
code 11, Rate 3/4 — which is the layout measured from a repeater's own
datagrams in `ipsc-text-rate34.pcap`, now written by QSP rather than read from
Motorola.

Serial numbers run 0, 1, 2, 3 and then the final block eight more times. A
master owes no acknowledgement, so the sending end repeats its last block until
it gives up; that is a working network, not a defect.

**One of the twelve fails its CRC-9 and was relayed anyway.** ADR-0047 decided
that a block QSP cannot verify is carried rather than dropped, because an
unconfirmed block has no CRC at all and discarding a member's message over one
bad bit is the worse failure. This is that decision meeting real traffic.

## What it does not establish

**Nobody has yet confirmed a radio displayed the message.** Everything here is
about bytes leaving QSP correctly. The last hop is a handheld screen and only
an operator can report it.
