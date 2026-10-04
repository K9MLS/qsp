# stun-link-request.bin

**The first bytes a Motorola Quantar sent QSP**, and the capture
[ADR-0060](../../docs/adr/ADR-0060-qsp-terminates-the-serial-tunnel.md) required
before any of `internal/quantar` could be written.

SHA-256 `d8e2bb3fae006b24de514a76a9a3ad4d92bed78209e84e94bc50ad062322fffd`

## Provenance

| | |
|---|---|
| Captured by | K9MLS, Denton TX |
| Date | 2026-10-04, from 12:27:06 UTC, 372 seconds |
| Station | Motorola Quantar, UHF R2, `ASTRO CAI CAPABLE`, control firmware R020.12.016, V.24 card TTN4010D |
| Router | Cisco 2921, serial `encapsulation stun`, `stun group 1`, `clock rate 9600`, `stun route all tcp` |
| Recorded with | `scripts/stun-capture.py`, which accepts the connection and sends nothing |
| Contents | The TCP stream exactly as it arrived, 6625 bytes |

`stun-link-request.log` is the first lines of the recorder's timing log: arrival
time, gap, bytes and running total for each read.

## What it holds

One opening message and 732 identical frames.

```
08 31 00 02 00 1e 01  + 30 bytes     the router's opening message, once
08 31 00 00 00 02 01  fd 3f          every 0.51 s thereafter
```

The header is a marker `08 31`, a two-byte type, a two-byte length and one more
byte. `fd 3f` is the station's own frame with the line's checksum already
removed by the router: address `FD`, control `3F`, which in HDLC is a request
to open the link with the poll bit set.

## What it does not hold

**No answer, and so nothing after one.** Nothing replied, which is why the
station never moved on. The thirty bytes of the opening message are not
interpreted. No voice: the radio was not keyed into an open link, because there
was not one.
