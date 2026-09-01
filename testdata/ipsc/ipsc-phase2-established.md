# ipsc-phase2-established.pcap

**Twenty-four minutes of a settled IPSC link**, doing nothing. A link that has
been up a while behaves differently from one still registering, and the cheapest
data in this project is the data you get by leaving a capture running.

SHA-256 `c57b2c19e5a24499215db2d858b05773e288281906012749f63b241f2b5a53f1`

## Provenance

Same equipment, bridge and endpoints as
[`ipsc-phase2-registration.pcap`](ipsc-phase2-registration.md), immediately
afterwards. 2026-09-01, 1,431 seconds, 205 packets after trimming from 2.1 MB.

## What is in it

| Type | Direction | Count | Cadence |
|---|---|---|---|
| `0x96` | peer → master | 96 | every 15.00 s, spread 20 ms |
| `0x97` | master → peer | 96 | within milliseconds of each request |
| `0x85` | peer → master | 12 | every 64.26 s, with a gap |
| `0x85` | master → peer | 1 | once, inside that gap |

**Nothing else.** An established link with no traffic on it is keepalives and
`0x85`, and that is the whole of it.

## The `0x85` anomaly

The peer sent `0x85` at 50.1 s and 114.4 s — 64.26 s apart — then **stopped for
738 seconds**. The master sent a single `0x85` of its own at 718.1 s, inside the
silence. The peer resumed at 852.3 s and then held 64.26 s exactly for the rest
of the capture.

Every instance is byte-identical apart from the sender ID:

```
peer     85 0004d098 00 00 00 01 01 02
master   85 002fcdee 00 00 00 01 01 02
```

**No theory is offered.** The obvious story — the peer went quiet and the master
prodded it — fits, and a story that fits is not evidence. What would settle it
is another twenty-minute capture: if the gap recurs, it is behaviour; if it does
not, it was an event.

## Why the fixture is worth its size

`0x85`'s interval could not have come from the registration capture: two
instances give one interval, and one interval is not a cadence. Twelve give
eleven, and eleven that agree to a hundredth of a second are a finding.
