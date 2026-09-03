# ipsc-master-voice.pcap

**A Motorola master sending voice.** The first capture in this project pointed
that way: every other IPSC fixture is a repeater talking to a master, and none
of them contains one byte of a master talking to a repeater.

SHA-256 `5b4011e05d19f591dae04f468c4f397d0811477f187fbb9d29311445a2c038a0`

## Why it exists

QSP's transmit path was built entirely from watching peers, which shows what a
master must *answer* and never what a master *initiates*. ADR-0041 named four
assumptions that could not be checked without a capture like this, and ADR-0042
corrected the frame shape using peer-side captures alone. This fixture is the
first evidence about the direction QSP actually sends in.

## Provenance

| | |
|---|---|
| Captured by | K9MLS, Denton TX |
| Date | 2026-09-03, 05:54 local |
| Master | XPR8300, firmware R02.30.20, radio ID **31329**, `192.168.1.233:50000`, colour code 4 |
| Peer | `cmd/ipsc-peer`, radio ID 3132911, `192.168.1.77:50004` |
| Source | The master's own RF, keyed on TG 2 timeslot 2, three transmissions |
| Capture | `tcpdump -i br0 -n -s 0 'host 192.168.1.233'` |
| Link type | Ethernet (1) |
| Packets | 347, of which 288 are voice |

The peer is a bench instrument, not QSP: under ADR-0043 QSP is never an IPSC
peer in production. The repeater was reconfigured into master role for this
capture and returns to being a peer afterwards.

## What it establishes

**The frame shape ADR-0042 derived from peer captures is what a master sends.**

- Headers and terminators are 54 bytes; voice frames are 52, 57 or 66.
- Byte 31 is `len - 32` on every voice frame, 276 frames, no violations.
- The cycle is `HHH (AfffLf)* T` in all three transmissions, without exception:
  three headers, superframes of sync-fragment-fragment-fragment-fragment-with-LC
  -fragment, one terminator.
- Byte 5 is a call counter, reading 1, 2, 3 across the three transmissions.
- Byte 51 is the DMR Slot Type: `0x41` on headers and `0x42` on terminators,
  which is colour code 4 with data type 1 and 2.

**22 of the 24 bytes from byte 30 onward are what QSP already builds**, including
the Reed-Solomon parity `90 b2 a0`, which QSP computes from the Link Control
rather than copying and which appears in no earlier capture.

## The two bytes that differ

Bytes 52 and 53. QSP writes zeros; this master writes `0x3b` and a value between
`0x23` and `0x41`.

**Byte 52 is constant within a session and varies between them.** It is `0x3b`
in all twelve headers and terminators here, `0x3c` and `0x1e` for the two hosts
in `ipsc-two-peers.pcap`. Two sessions with the same colour code give different
values, so it is not a function of the colour code.

**Byte 53 changes frame to frame**, 35 to 65 in this capture. No sum, XOR or
two's-complement checksum over any tried range of the datagram reproduces it,
and neither do seven CRC-16 constructions tested earlier over the peer captures.

One stable byte and one wandering byte, per device and per session, with no
relationship to the frame's contents, is the shape of a measurement rather than
of derived data — plausibly the received signal the master is relaying. **That
is a reading of the numbers, not a decoding**, and nothing in this project
depends on it: a repeater accepted QSP's zeros on air on 2026-09-02 and keyed.
