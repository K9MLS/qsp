# ipsc-phase2-registration.pcap

**Two Motorola repeaters registering to each other over the internet**, with a
capture host in the path. This is the file that took IPSC from one message type
to seven, and it contains the reply to `0x90` — the message a QSP master will
have to produce.

SHA-256 `69db0ad650eaa24aa0e6ca328efdec6cc0366091d1e9667f955b8467d30a6c5e`

## Provenance

| | |
|---|---|
| Captured by | K9MLS, Denton TX |
| Date | 2026-09-01, 13:22–13:25 UTC |
| Master | XPR8300, firmware R02.30.20, `192.168.1.233`, radio ID **3132910** |
| Peer | remote repeater at `198.51.100.2`, radio ID **315544**, TTL 50 on arrival so roughly fourteen hops |
| Capture host | Fedora workstation bridging `eno1` and a USB NIC, captured on the repeater-facing port |
| Link type | EN10MB |
| Authentication | disabled both ends |
| Packets | 24 after trimming to the two endpoints |

Captured unfiltered on a bridge port and trimmed to traffic involving
`192.168.1.233`. The untrimmed original was 136 KB of LAN broadcast, since a
bridge floods broadcast out every port.

**Equipment detail for the remote repeater — model, firmware, codeplug version —
is not yet recorded and should be**, to the standard the XPR8300's entry sets.

## Why a bridge

Phase 1 worked with plain `tcpdump` because the packets were addressed to the
capture host. Once two repeaters talk to each other, a switch forwards their
unicast to one port and nothing else sees it. A repeater is a closed box with no
shell, so the capture host has to be physically in the path: two interfaces,
bridged, with the repeater's cable on one side.

## The registration exchange

Six packets in 100 milliseconds:

| Time | Direction | Type | Len |
|---|---|---|---|
| 13:22:46.538 | peer → master | `0x90` | 14 |
| 13:22:46.546 | master → peer | `0x91` | 16 |
| 13:22:46.607 | peer → master | `0x96` | 14 |
| 13:22:46.611 | peer → master | `0xf0` | 9 |
| 13:22:46.611 | master → peer | `0x97` | 14 |
| 13:22:46.638 | master → peer | `0xf1` | 44 |

Registration is **not** a request and an acknowledgement. The master's reply
arrives 8 ms after the request; the peer then starts keepalives and separately
sends `0xf0`, which draws the largest message in any capture.

## What it establishes

**Byte 0 is the type and bytes 1–4 are the sender's own radio ID.** Phase 1
showed those bytes track the configured Radio ID by changing it, but one
repeater talking into silence cannot distinguish "who sent this" from "who this
concerns" — there was only ever one party. Here, in one exchange, the peer's
messages carry 315544 and the master's carry 3132910.

**Keepalives run at fifteen seconds, not ten.** Ten is the *unregistered* retry.
Two clocks, two states.

## What it does not establish

No voice, no private call, no text, no disconnect — nobody was at the remote
end. Nine of `0x91`'s sixteen bytes and thirty-nine of `0xf1`'s forty-four are
unexplained.

`0xf1` was captured with **one** peer registered. If it is a peer list, its
length varies with the number of peers, which is why
`internal/protocol/ipsc` records observed lengths and does not enforce them.
