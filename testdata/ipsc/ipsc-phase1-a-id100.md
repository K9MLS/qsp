# ipsc-phase1-a-id100.pcap

**The first capture of Motorola IPSC traffic reaching QSP's network**, and the
file that lifted [ADR-0029](../../docs/adr/ADR-0029-ipsc-from-capture.md)'s
block on writing any IPSC code at all.

SHA-256 `c1c376450435de371dfbfa5c7825567d32a41e2f2fdf7bb8e5e2c6ff81d21d88`

## Provenance

| | |
|---|---|
| Captured by | K9MLS, Denton TX |
| Date | 2026-09-01, 12:30–12:35 UTC |
| Repeater | Motorola XPR8300, model `M27QPR9JA7AN`, firmware **R02.30.20**, codeplug 13.01.03, bootloader R02.03.01 |
| Repeater address | `192.168.1.233` |
| Capture host | `qsp-server`, Ubuntu 24.04 VM at `192.168.1.247` |
| Link type | EN10MB (`tcpdump -i ens192`) |
| Radio ID | **100** |
| Master configured | `192.168.1.247:50000` |
| IPSC authentication | disabled |
| Duration | 280 s, 66 records after trimming |

Captured unfiltered, then trimmed to the repeater with
`tcpdump -r … -w … 'host 192.168.1.233'`. The untrimmed original contained the
operator's SSH session, QSP's production HBP traffic and every broadcast on the
LAN, none of which is in this file.

## What was done

The XPR8300 was configured as an IPSC **peer** pointing at a host running
nothing at all. There is no IPSC implementation in QSP and nothing was listening
on 50000. The repeater's registration attempts going unanswered is the point of
the capture, not a fault in it.

**Why this works without a mirror port.** The packets are addressed to the
capture host, so an ordinary `tcpdump` on an ordinary interface records them.
That stops being true the moment two repeaters talk to each other, which is what
makes the phase 2 capture harder than this one. See
[`CAPTURE-PLAN.md`](CAPTURE-PLAN.md).

## What is in it

| Flow | Packets | What it is |
|---|---|---|
| `192.168.1.233:50002 → 192.168.1.247:50000` | 28 | the repeater's registration request |
| `192.168.1.247 → 192.168.1.233` | 28 | ICMP port unreachable, one per request |
| ARP | 10 | address resolution |

All 28 requests are byte-identical, including the UDP checksum:

```
90 00 00 00 64 6a 00 00 80 4c 04 06 04 00
```

## What it establishes

**The retry interval is ten seconds flat.** Twenty-seven gaps, none deviating
more than 3 ms, no backoff, and no give-up after five minutes.

**The peer sources from 50002 while addressing 50000.** It does not use the
master's port as its own. An implementation that assumes symmetry will pass its
own tests and fail against Motorola.

**ICMP port unreachable is ignored.** The kernel answered every request 80 µs
later and the cadence did not change. A master cannot refuse a peer by staying
silent; refusal has to be an IPSC-level message, and no capture contains one.

**The message carries no counter, nonce or timestamp**, so a recorded request
can be replayed against a master under development without a repeater present.

## What it does not establish

Nothing about a reply, because there was none. The rest of registration,
keepalives, the peer list, voice, private calls, text and disconnect are all
absent, and nine of the fourteen payload bytes have no known meaning. Compare
with [`ipsc-phase1-b-id3132910.md`](ipsc-phase1-b-id3132910.md), which is what
names the four bytes that do.
