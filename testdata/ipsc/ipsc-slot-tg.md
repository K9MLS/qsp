# ipsc-slot-tg.pcap

**The capture that found the timeslot** — the field the Homebrew protocol
requires on every burst and IP Site Connect had never revealed.

SHA-256 `86067f80a59e761daa23b0bf5cb4eeb444b42f2f51b275bdeaf9eba9d4ce0ed5`

## Provenance

| | |
|---|---|
| Captured by | K9MLS, Denton TX |
| Date | 2026-09-02, 00:47–00:48 UTC |
| Master | QSP 0.1.24 in production, `192.168.1.247:50000` |
| Peer | XPR8300, firmware R02.30.20, radio ID 3132910 |
| Capture | `tcpdump -i ens192 host 192.168.1.233` on the VM |
| Packets | 279, containing 15 transmissions |

The repeater was registered to the production listener rather than to
`cmd/ipsc-probe`, which could not bind because QSP already held the port. The
real listener served it identically, which is itself worth recording.

## What was varied

The operator has **two channels programmed**, carrying the **same talkgroup**
and differing by **timeslot**. Fifteen transmissions were keyed, alternating
between them: three on one, three on the other, three back, then six.

**Which channel is timeslot 1 was not written down at the radio and is not
recorded here.** See below.

## The finding

The fifteen transmissions split into exactly two groups by **bit 0x20 of byte
17**, and nothing else in any header differs between them. Destination reads 455
in all fifteen, source and every other field are identical.

```
0x20 set    6 transmissions
0x20 clear  9 transmissions
```

A single-variable experiment with a single-bit answer.

It also completes an earlier half-observation. Byte 17 was noted as `0x20` on
voice frames and `0x60` on terminators, which looked like one value changing.
There are two independent bits in the byte: `0x20` is the timeslot and `0x40`
marks the last frame of a transmission.

## What this capture does **not** establish

**Which value means slot 1.** The bit separates the two slots beyond doubt; its
polarity is one sentence from the operator away and has not been supplied.
`SlotBit` therefore returns the raw bit rather than a slot number, because
mapping it would be a claim rather than an observation.

**The destination.** Both channels carry talkgroup 455, so this capture holds no
talkgroup differential and bytes 9 to 11 remain unproven — as they have through
every capture in this directory. A third channel on any other talkgroup settles
it in one key-up.

## A note on the transmissions

Several are four frames, 180 ms — barely a touch of the PTT, consistent with
keying while changing channel. They are kept: a transmission that short is
exactly the case a naive implementation mishandles, and the listener bounded
every one of them correctly.
