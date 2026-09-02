# ipsc-two-peers.pcap

**Three repeaters registered to QSP, two of them transmitting at the same time.**
The richest IPSC capture in this project, and the one that established that
IP Site Connect relays voice through the master rather than meshing between
peers.

SHA-256 `cbf0dca974aaff248cb5c051dff2cc0c69c56fe1c14c7faf37e24a3f9e2cae0a`

## Provenance

| | |
|---|---|
| Captured by | K9MLS, Denton TX |
| Date | 2026-09-02, 19:36 UTC |
| Master | QSP 0.1.34 in production, `192.168.1.247:50000` |
| Peer | XPR8300, firmware R02.30.20, radio ID 3132910, `192.168.1.233` |
| Peer | SLR5700 (KD9EJA), radio ID 315544, `198.51.100.2` |
| Peer | KB9TYC's repeater, radio ID 3155412, `198.51.100.172` — model not yet recorded |
| Capture | `tcpdump -n -i any udp port 50000` on the VM |
| Link type | LINUX_SLL2 (276), not Ethernet |
| Packets | 349 over 49 seconds |

**The remote repeaters' models and firmware should be recorded** to the standard
the XPR8300's entry sets. KB9TYC's is unknown even by model.

This is the first fixture captured with `-i any`, so it carries Linux cooked v2
headers rather than Ethernet. The test reader accepts both, because a server
with several interfaces cannot capture any other way.

## What it establishes

**Voice is relayed, not meshed.** Two peers transmitted simultaneously and every
one of 326 voice frames was addressed to the master. None went peer to peer.
Had IPSC exchanged voice directly between peers, the format QSP must send would
be the format it already receives — fully decoded — and the reverse path would
need no further discovery. It does not, so a master sending voice remains a
direction nothing has captured.

**A transmission is three headers, a superframe cycle, and one terminator.**

```
54 54 54            first flagged 0x80dd
52 57 57 57 66 57   repeating
54                  byte 17 0x60, flags 0x805e
```

Six is a DMR superframe, and the payload length varies with the position in it.
These are one voice frame carrying different embedded signalling by position,
not four unrelated types. **Motorola sends the header three times**, presumably
so a receiver joining late catches one; a QSP master relaying voice will have to
decide whether to do the same.

Both repeaters produce the identical pattern, which is the first evidence that
transmission structure is not specific to one model.

## What it does **not** establish

**What a master sends to a peer.** QSP sent ten packets in the whole capture,
every one a `0x97` keepalive reply. The master relayed nothing, because QSP does
not do that yet — which is exactly why two Motorola operators on this network
cannot hear each other.

It also cannot disprove a mesh on its own: it was taken on the master, and
traffic between two other hosts would never reach it. What it shows is that both
peers addressed the master while transmitting, which is what a relayed
architecture looks like and what a mesh would not require.

**Who keyed and when** was not recorded at the time and should have been.
