# hbp-voice-live.pcap

**The first capture of a live DMR transmission reaching QSP.**

Captured 2026-08-25 on the WPSD hotspot, with QSP running as the master. This
is the fixture that closes the Phase 1 gate: until this file existed, every line
of the HBP codec rested on two captures of BrandMeister traffic and one person's
reading of them.

```
sudo tcpdump -i any -w qsp-voice-20260825.pcap 'udp port 62031'
```

SHA-256 `01053f62acaaa373bc9a49d9e84471bdb6a8d424cf76ecc216d853d3e640dcb6`

---

## What is in it

1152 packets over 94.7 s, LINUX_SLL2, 0 dropped by the kernel.

`-i any` recorded both legs, which is useful rather than noise:

| Flow | Packets | What it is |
|---|---|---|
| `192.168.1.155:45582 → 192.168.1.135:62031` | 566 | hotspot → QSP, the master link |
| `192.168.1.135:62031 → 192.168.1.155:45582` | 10 | QSP → hotspot, MSTPONG |
| `127.0.0.1:62032 ↔ 127.0.0.1:62031` | 576 | MMDVMHost ↔ DMRGateway, the local dialect |

Both dialects run concurrently on one host, so the file also documents the
difference between the master link's 11-byte `MSTPONG` and the gateway link's
4-byte `DMRP`.

## The transmissions

Five voice streams, all group calls, source and repeater both 3132910, TS2,
arriving as TG 9 after DMRGateway rewrote TG 11 per `TGRewrite0=2,11,2,9,1`.

| Stream ID | Frames | Duration | Rate |
|---|---|---|---|
| `0x503609e8` | 74 | 4.50 s | 16.46/s |
| `0x8478a6a9` | 62 | 3.77 s | 16.44/s |
| `0xb186fbc4` | 62 | 3.77 s | 16.44/s |
| `0x12bc1491` | 116 | 7.02 s | 16.52/s |
| `0x6cd7506b` | 242 | 14.58 s | 16.59/s |

DMR's frame rate is one per 60 ms, or 16.67/s. Every stream lands within 1.5 %
of that across durations from under four seconds to nearly fifteen. The counts
match what the console reported independently, and QSP recorded zero drops and
zero collisions across all 556 frames.

## Round-trip

Every one of the 576 LAN payloads parses and re-marshals byte-for-byte:

```
LAN payloads:       576
round-trip exact:   576
round-trip differs:   0
parse failures:       0

hbp.Data  556
hbp.Ping   10
hbp.Pong   10
```

## Reading this file: clip to the UDP length

Frames shorter than the 60-byte Ethernet minimum are zero-padded, and the
padding is captured. An 11-byte `MSTPONG` produces a 39-byte IP datagram, which
is padded by 7 bytes.

Slicing from the end of the UDP header to the end of the record therefore yields
an 18-byte "MSTPONG" that no parser will accept, and it looks exactly like a
protocol defect. Clip to the UDP length field instead —
`internal/peers/pcap_test.go` does this correctly and is the reference.

## Credentials

**No login handshake.** The capture began roughly ten minutes into an
established session, so there is no `RPTL`/`RPTK` exchange and no
`SHA-256(salt ‖ password)` in this file. Nothing required redaction.

The password in use was `qsptest123`, a throwaway chosen for this test.

## What this still does not cover

| Gap | What closes it |
|---|---|
| `RPTCL` on a clean disconnect | A capture running while the custom network is disabled |
| `MSTNAK` on a bad login | A capture of a deliberately wrong password |
| The `description`/`slots` field split | A capture from a single-timeslot hotspot |
| Repeater-ID rewrite on relay | Two peers with forwarding on |
| Any P25 transmission | `testdata/p25/` holds polling traffic only |

Note that the repeater-ID rewrite is **not** closed by this capture. Only one
peer was connected and forwarding was off, so nothing was relayed — QSP observed
and did not transmit.
