# ipsc-text-rate34.pcap

**The capture that settled what a text message is made of.** Every other IPSC
text fixture in this repository holds preambles, data headers and blocks short
enough for BPTC; this one holds the Rate 3/4 content blocks, which is where the
message itself lives and which QSP dropped from the day the text path was
written.

SHA-256 `72c6748d06046258197e0cfd814e4abce3c71ad51e4fe19b35332f4e8ce3d204`

## Provenance

| | |
|---|---|
| Captured by | K9MLS, Denton TX |
| Date | 2026-09-06, 17:52 to 17:58 UTC |
| Repeater | XPR8300, radio ID **999999**, `192.168.1.233`, colour code 4, peer of QSP |
| Master | QSP 0.1.84 at `192.168.1.247:50000` |
| Sending radio | 3132910 (`0x2fcdee`) |
| Receiving radio | 3155373 (`0x3025ad`) |
| Link type | Linux cooked v2 (276) |
| Packets | 166 |

**This is a filtered capture, not a whole one.** It was taken from a session
capture of the whole server, `qsp-session.pcap00`, md5
`8d6a258b13348e5f382d81c8cdcb58a8`, 21 635 UDP datagrams. Kept here are the
datagrams between `192.168.1.233` and `192.168.1.247` whose IPSC type byte is
`0x83` or `0x84`. Nothing was altered: each packet record is the original
bytes, and the pcap header is the original header. The parent capture is not
committed because it is 2,5 MB of mostly voice and hotspot keepalives.

## What it contains

| Direction | Type | Byte 30 | Length | Count |
|---|---|---|---|---|
| repeater → QSP | `0x84` | `0x03` CSBK | 54 | 110 |
| repeater → QSP | `0x84` | `0x06` Data Header | 54 | 6 |
| repeater → QSP | `0x84` | `0x08` **Rate 3/4** | **60** | **30** |
| repeater → QSP | `0x84` | `0x02` Terminator | 54 | 1 |
| repeater → QSP | `0x84` | — | 34 | 7 |
| QSP → repeater | `0x84` | `0x03` CSBK | 54 | 12 |

**QSP relayed no Rate 3/4 datagram at all**, which is the defect this fixture
was cut to demonstrate: 30 arrived and 0 left. The 12 outbound datagrams are
preambles.

The 30 blocks belong to six transmissions of two messages, each sent three
times because the operator pressed send three times. The repeats are
byte-identical apart from the call counter, stream ID, sequence and timestamp,
which makes them free replication of every measurement below.

## What it establishes

- **A Rate 3/4 datagram is 60 bytes and everything from the block onward sits
  six bytes later than in a 54-byte one.** The block occupies 38 to 55, byte 56
  is zero where byte 50 is zero in a 54-byte frame, byte 57 is the DMR Slot
  Type where byte 51 is, and 58 and 59 are the tail. Byte 57 reads `0x48` in
  all 30: colour code 4, data type 8.

- **Bytes 32 to 37 are not the constants a voice header carries.** They read
  `00 0d 80 0a 00 90` where a 54-byte frame reads `00 0a 80 0a 00 60`. Byte 37
  is `0x60` against `0x90`, which are 96 and 144 — the bit counts of the two
  information blocks. Byte 33 goes `0x0a` to `0x0d`; two readings fit two data
  points and neither is needed.

- **The block is sixteen octets of user data, then a seven-bit serial number
  and a nine-bit CRC.** Concatenating the first sixteen octets of the six
  blocks of one transmission gives an IPv4 datagram of total length 88,
  protocol 17, from `0c 2f cd ee` to `0c 30 25 ad` — Motorola's radio-IP
  encoding of the two radio IDs in the envelope — carrying UDP on port 4007
  whose payload is UTF-16 little-endian after a ten-octet header, reading
  *"I can't talk right now..."*. A second message reads *"Give"*. No other
  offset produces any of that.

- **The serial numbers run 0 to 5 and 0 to 3.**

- **`dmrfec.CRC9` reproduces the check value of all 30 blocks here**, and of
  all 12 in `ipsc-text.pcap`, for 42 of 42.

## What it does not contain

**No Rate 3/4 burst as it goes over the air.** IP Site Connect carries the
block already decoded, so nothing here exercises the trellis code, and the
parent session capture's Homebrew traffic is voice from end to end. The
arrangement of the two control octets in a burst is still unmeasured; see
`dmrfec.Rate34AirOrder`.

## Expected parser behaviour

`ipsc.Message.AsText` reads all 154 inbound bursts. Blocks are 12 octets for
byte 30 in {`0x02`, `0x03`, `0x06`} and 18 for `0x08`. The 34-byte frames whose
marker is `0x13` are refused: that marker is not a DMR data type and naming it
would be a claim this project cannot demonstrate.
