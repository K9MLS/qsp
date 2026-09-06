# hbp-text-rate34.pcap

**The only capture anywhere of a Rate 3/4 burst as a radio produces it**, and
the one that ended a question two sessions could not settle by reasoning.

Every other Rate 3/4 fixture here holds blocks a Motorola repeater had already
decoded, which exercises the block layout and says nothing whatever about the
trellis code. This one holds 54 coded bursts from MMDVMHost.

SHA-256 `825ae129d9db96ce6215ecf09f1072399ca5502f9f109035782f7a94eb474f07`

## Provenance

| | |
|---|---|
| Captured by | K9MLS, Denton TX |
| Date | 2026-09-06, 22:39 UTC |
| Hotspot | Pi-Star / MMDVMHost at `192.168.1.155:52771` |
| Master | QSP at `192.168.1.247:62031` |
| Sending radio | 3132910, into the hotspot |
| Addressed to | 3155373, private |
| Link type | Linux cooked v2 (276) |
| Packets | 366, of which 348 UDP |

The operator typed **`Hi`** into a handheld and sent it three times.

## What it contains

| Direction | Frame | Data type | Count |
|---|---|---|---|
| hotspot → QSP | DMRD | `0x3` CSBK | 224 |
| hotspot → QSP | DMRD | `0x6` Data Header | 18 |
| hotspot → QSP | DMRD | `0x8` **Rate 3/4** | **54** |
| QSP → hotspot | DMRD | `0x6`, `0x7` | 26 |
| both | MSTPONG / RPTPING | — | 26 |

All timeslot 2, all private calls.

## What it establishes

**The trellis tables are right, and the differential is total.** Decoding the
54 bursts with the constellation mapping of ETSI table 10.3 succeeds on 54.
Decoding them with the mapping this repository shipped in patch 0242 succeeds
on **none**. That mapping is a permutation of the four dibit values, so encode
and decode agreed with each other perfectly and every test passed; only a radio
could tell, and this is the radio.

**A hotspot puts the block serial number and CRC at the front.** 49 of the 54
verify their CRC-9 read control-first and **none** verify control-last, which
settles `dmrfec.Rate34AirOrder` by measurement rather than by argument between
figure 8.8 and what IP Site Connect delivers.

**QSP's encoder reproduces a hotspot's bursts exactly.** Re-coding each decoded
block gives back the same 33 bytes MMDVMHost sent, for all 49. This is the only
check in the repository that a wrong table cannot pass, because the bytes on
the other side came out of somebody else's encoder.

**The message decodes.** The three blocks reassemble into an IPv4 datagram of
total length 42, protocol 17, from `0c 2f cd ee` to `0c 30 25 ad`, carrying UDP
on port 4007 whose payload after a ten-octet header is UTF-16 little-endian
`Hi`.

## The five that verify neither way

Serial 2 arrives three ways — `…7b3b29fd`, `…533b29fd`, `…7b3b79fd` — differing
by single bits. Errors the hotspot passed through. They are carried rather than
dropped, which is ADR-0047's decision meeting real traffic on its first day.

## Expected parser behaviour

`dmrfec.DecodeRate34Burst` returns a block for all 54 and reports
`control-first` for 49 and `unknown` for 5. `dmrfec.BuildRate34Burst` rebuilds
the original burst byte-for-byte from any of the 49.
