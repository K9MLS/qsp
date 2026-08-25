# hbp-login-session.pcap

Complete Homebrew Protocol login handshake, captured from a live hotspot
connecting to a BrandMeister master, plus 90 seconds of steady-state keepalives.

## Provenance

| | |
|---|---|
| Captured by | K9MLS |
| Date | 2026-08-23 |
| Hotspot | WPSD (Pi-Star fork), MMDVM_HS_Dual_Hat, MMDVMHost + DMRGateway |
| Master | BrandMeister 3102 United States |
| Link type | LINUX_SLL2 (`tcpdump -i any`) |
| Duration | 100 s, 250 packets |

The operator captured his own hotspot's session. No third-party traffic is
present: nobody transmitted during the capture window.

## What it contains

Two independent HBP conversations from **two different implementations**:

- **WAN** — DMRGateway to BrandMeister, `192.168.1.155:45381 ↔ 198.51.100.19:62031`
- **Loopback** — MMDVMHost to DMRGateway, `127.0.0.1:62032 ↔ 127.0.0.1:62031`

### The login sequence (WAN, t+10.03 to t+10.06)

```
RPTL   →   "RPTL"   + repeater_id(4)                       8 bytes
RPTACK ←   "RPTACK" + salt(4)                              10 bytes
RPTK   →   "RPTK"   + repeater_id(4) + sha256_digest(32)   40 bytes
RPTACK ←   "RPTACK" + repeater_id(4)                       10 bytes
RPTC   →   "RPTC"   + repeater_id(4) + config(294)         302 bytes
RPTACK ←   "RPTACK" + repeater_id(4)                       10 bytes
```

The whole exchange completes in about 30 ms. Steady state follows:
`RPTPING`/`MSTPONG` every 10 seconds.

### Authentication

**`digest = SHA-256(salt ‖ password)`**, where `salt` is the 4 bytes returned
in the first `RPTACK` and `password` is the shared secret, concatenated as raw
bytes with no separator or encoding.

This was verified empirically against the original capture before sanitization.

### RPTC layout (294 bytes after the tag and repeater ID)

| Offset | Len | Field |
|---|---|---|
| 8 | 8 | callsign |
| 16 | 9 | rx_freq |
| 25 | 9 | tx_freq |
| 34 | 2 | tx_power |
| 36 | 2 | color_code |
| 38 | 8 | latitude |
| 46 | 9 | longitude |
| 55 | 3 | height |
| 58 | 20 | location |
| 78 | 19 | description |
| 97 | 1 | slots |
| 98 | 124 | url |
| 222 | 40 | software_id |
| 262 | 40 | package_id |

Sums to exactly 302. All fields are ASCII, space-padded.

### DMRC — DMRGateway's abbreviated dialect

The loopback link uses `DMRC` (119 bytes) instead of `RPTC`. It is identical
through offset 38, then **omits** latitude, longitude, height, location,
description and url, continuing straight to `slots`, `software_id`,
`package_id`.

Its keepalive is a bare 4-byte `DMRP` with no repeater ID, versus the WAN's
11-byte `RPTPING`.

**This divergence is why two implementations were captured rather than one.**

## Sanitization

The following were altered. Every replacement preserves field length exactly,
so all offsets remain valid.

| Field | Replaced with |
|---|---|
| latitude | `+00.0000` |
| longitude | `-000.0000` |
| location | `Example, AA00aa` |
| description | `XX, EXAMPLE` |
| url | `https://example.invalid` |
| RPTK digest | recomputed (see below) |

UDP checksums on modified packets are set to zero, which IPv4 defines as
"no checksum".

**The auth digest was recomputed against a published test password**, so this
fixture is a known-answer test rather than an opaque blob:

```
salt     = 9947b430
password = qsp-test-password
digest   = 470eb0cf86ee3a0854f7ccbc20cb7373a16cea6cbd75d58f5a4518a7d596b4f6
```

A correct implementation must reproduce that digest from those inputs.

Callsign, radio ID and frequencies are retained. All are public information for
a licensed amateur station.

## Expected parser behaviour

- Parse all six handshake packets and both keepalive dialects without error
- Reproduce the digest above from the published salt and password
- Decode every `RPTC` field at the documented offsets
- Handle `DMRC` as a distinct, shorter message rather than a truncated `RPTC`
- Treat `DMRP` (4 bytes, no repeater ID) as a valid keepalive
