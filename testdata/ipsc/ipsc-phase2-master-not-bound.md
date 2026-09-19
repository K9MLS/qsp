# ipsc-phase2-master-not-bound.pcap

**A failure, kept on purpose.** Fifteen minutes of a correctly configured IPSC
master refusing every request, because it was serving a different port from the
one it advertised. This cost most of an afternoon and it will cost somebody else
the same unless the evidence is here to recognise.

SHA-256 `cb4727638f40ce750cae3ed9d23ea2de541e3e5b11f71e5a9a3e23a2d57f9745`

## Provenance

2026-09-01, 13:09–13:24 UTC. Same equipment and bridge as the other phase 2
captures. 82 packets after trimming.

## What it shows

Fifty-three `0x90` requests from `203.0.113.60:50004` to `192.168.1.233:50000`,
every ten seconds, each answered by an **ICMP port unreachable from the
repeater's own IP stack** — not from a firewall, not from the router. The
repeater itself was saying nothing is bound to 50000.

`tcpdump 'src host 192.168.1.233 and udp'` on the untrimmed original returns
nothing at all. The master sent no UDP from any port for fifteen minutes.

## The cause

**The XPR8300's CPS has two port fields and they are not the same thing.**

| Field | Value | What it is |
|---|---|---|
| Master UDP Port | 50000 | the master this repeater dials |
| UDP Port | 50001 | the port this repeater binds |

Configured as a master, it bound 50001 and advertised 50000. `nmap -sU` across
49995–50010 found exactly one open port — 50001, every neighbour closed — which
is what turned a third theory into a measurement.

Setting `UDP Port` to 50000 produced a registration within seconds.

## Why it is a fixture and not a deleted file

Two wrong diagnoses came before the right one: that the codeplug write had not
landed, and that the master IP was wrong. Both were plausible, both were
checkable, and both were wrong. The observation that ended it was a port scan of
the repeater rather than another reading of the capture.

The lesson is cheap to state and expensive to learn: **when a device's own IP
stack sends the refusal, the configuration is not doing what the configuration
page says.** Ask the device what it bound.
