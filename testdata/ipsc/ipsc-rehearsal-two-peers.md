# ipsc-rehearsal-two-peers.pcap

**Registration requests from two different repeaters in one file**, which is
what proves the unexplained bytes are not constant.

SHA-256 `e47080717356db1546ba4915e57679809d6b41d78dddee48523a6591e557c099`

## Provenance

2026-09-01, 12:44–12:53 UTC. Taken to prove the bridge forwarded traffic before
committing an evening to it — the XPR8300 was temporarily returned to peer mode
and pointed at a host running nothing, exactly as in phase 1, but captured
through the bridge instead of directly.

82 packets after trimming, from 738 KB.

## What was not expected

The capture contains a **third party**: `198.51.100.2`, already sending `0x90`
to the XPR8300 every ten seconds, and receiving ICMP unreachable because the
repeater was in peer mode. A remote repeater had been pointed at this network
for some time, patiently retrying.

That is what made the phase 2 captures possible the same afternoon. It also
means a port forward should be assumed to be found: an IPSC port open to the
internet will attract whatever is configured to reach it.

## The finding

Two repeaters, two `0x90` bodies:

```
XPR8300, ID 3132910   90 002fcdee | 6a 00 00 80 4c 04 06 04 00
remote,  ID 315544    90 0004d098 | 66 00 00 80 4c 04 08 04 00
                                     ^^                 ^^
```

Two of the nine bytes past the sender ID differ between equipment. Seven agree.

Patch 0167 recorded one repeater's trailer as `ObservedTrailer` and deliberately
made the parser **accept any value**, on the grounds that two captures from one
repeater said nothing about a second. Four hours later a second repeater
disagreed with it. Had the value been enforced, QSP would have rejected this
peer outright.

The same reasoning now applies to message lengths, which are recorded and not
enforced for exactly the same reason.

## A correction it forced

An earlier note claimed the peer "sources from 50002". This repeater sources
from 50004. The invariant is that a peer does **not** use the master's port as
its own — reply to the port a datagram came from, never the port it went to.
