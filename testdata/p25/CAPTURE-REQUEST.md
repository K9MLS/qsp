# P25 voice capture request

QSP carries P25 natively — bytes in, bytes out, no vocoder — for the reasons in
[ADR-0034](../docs/adr/ADR-0034-p25-is-native.md). It cannot be built without a
capture of a real P25 call.

`p25-gateway-idle.pcap` already covers the idle path: 155 packets of P25Gateway
polling and status, captured 2026-08-23. **It contains no voice**, which is the
part that matters.

## What we need, in one sentence

A packet capture of P25Gateway's conversation with MMDVMHost from before the
gateway starts, through at least two voice transmissions, to a clean stop.

---

## Why the boundaries matter more than the middle

**Start the capture before the gateway.** Registration and the first exchange
happen once. A capture that begins after the gateway is already running shows
steady state and nothing about how it got there — and steady state is the part
that can be guessed at, while the handshake is not.

**Two transmissions, not one.** One shows the shape. Two show what changes
between them: whether a stream identifier increments, resets or is random, and
whether the second call carries anything the first did not. A field that is
constant across one call and varies across two is a field nobody can identify
from a single capture.

**Let it run quiet in between.** Thirty seconds of nothing happening records the
keepalive interval, and an interval guessed wrong looks right until a gateway
drops an hour later.

## On the hotspot

Make the filesystem writable first:

```sh
rpi-rw
```

Stop the gateway, start the capture, then start the gateway:

```sh
sudo systemctl stop p25gateway
sudo tcpdump -i any -w /tmp/p25-voice.pcap -s 0 'udp and (port 42020 or port 32010)'
```

Leave that running. In a second session:

```sh
sudo systemctl start p25gateway
```

Wait about thirty seconds, then make **two** transmissions on a P25 talkgroup
with a gap between them, then wait another thirty seconds and stop the capture
with Ctrl-C.

```sh
sudo systemctl stop p25gateway
```

## What to send

The `.pcap`, and a note recording:

| | |
|---|---|
| Captured by | callsign |
| Date | |
| Software and versions | P25Gateway, MMDVMHost, WPSD release |
| Talkgroup used | |
| Radio | make and model |
| Anything unusual | a retry, a reconnect, a call that did not go through |

**The unusual things are worth as much as the clean ones.** A failed
registration in a capture is a documented failure mode; a failed registration
nobody captured is a bug report six months later with no evidence attached.

## What we will not do with it

Read another implementation to interpret it. ADR-0008 records why: reading a GPL
implementation to learn a protocol binds this project to a derivative-work
licence from the moment it is read, whether or not a line is copied. The capture
and the published specification come first, in that order.
