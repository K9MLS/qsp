# p25-gateway-idle.pcap

P25Gateway polling traffic captured incidentally alongside the DMR sessions.

## Provenance

| | |
|---|---|
| Captured by | K9MLS |
| Date | 2026-08-23 |
| Software | WPSD, P25Gateway + P25Parrot + MMDVMHost |
| Link type | LINUX_SLL2 |
| Contents | 155 packets |

## What it contains

| Count | Tag | Length | Flow |
|---|---|---|---|
| 77 | `0xF0` + callsign | 11 | P25Gateway → MMDVMHost, `42020 → 32010`, every ~5 s |
| 39 | `stat` | 6 | dashboard status query |
| 39 | `p25:` | 8 | status reply |

## Known limitation

**This contains no P25 voice.** No P25 transmission occurred during the capture
window, so it documents only the idle polling rhythm.

It is retained because it is real, correctly attributed traffic and the polling
interval is itself worth knowing. **It is not sufficient to implement a P25
parser against.** P25 work is phase 4 and needs a capture containing an actual
transmission, ideally through P25Parrot for a clean round-trip.

See also `docs/adr/ADR-0008-protocol-licensing.md`.
