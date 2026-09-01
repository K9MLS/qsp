# ipsc-probe-voice.pcap

**A Motorola repeater registered with software that is not Motorola's, and then
sent voice through it.** Both are firsts for this project, and the second is the
larger of the two.

SHA-256 `14f2a2a3fbd473f8072971ff929d41db1bca35a606814d0661bde828cd831cc9`

## Provenance

| | |
|---|---|
| Captured by | K9MLS, Denton TX |
| Date | 2026-09-01, 19:35–19:37 UTC |
| Master | `cmd/ipsc-probe` on the Ubuntu VM at `192.168.1.247:50000`, announcing radio ID **3132911** |
| Peer | XPR8300, firmware R02.30.20, `192.168.1.233:50002`, radio ID 3132910 |
| Capture | `tcpdump -i ens192 host 192.168.1.233` on the VM. No bridge needed: the packets are addressed to the capture host |
| Packets | 83 |

## What had to be got right first

The probe was first run announcing ID **3132910** — the repeater's own — and the
repeater refused it thirty-nine times over six minutes, never advancing past
`0x90`. It was not the unexplained bytes. **A repeater will not register with
itself.** One flag changed it.

That is worth stating because the failure looked exactly like a protocol
failure: correct replies, sent promptly, ignored completely.

## What is in it

| Type | Count | |
|---|---|---|
| `0x90` | 4 | registration requests — **three refused before the probe started, one answered** |
| `0x91` | 1 | the probe's reply, replaying the captured master's bytes |
| `0x96`/`0x97` | 5 / 4 | keepalives at fifteen seconds |
| `0xf0`/`0xf1` | 1 / 1 | |
| `0x85` | 1 | |
| `0x80` | 66 | **voice**, in three transmissions |

The capture was started before the probe by accident, so it holds the same link
being refused and then accepted minutes apart. That is a better fixture than
either state alone.

## The voice header

```
[0]      80              type
[1:5]    00 2f cd ee     sender ID, 32-bit (the envelope, shared with every kind)
[5]      02              call counter
[6:9]    2f cd ee        source radio ID, 24-bit
[9:12]   00 01 c7        destination, 24-bit — UNVERIFIED, see below
[12:15]  02 00 00        unknown
[15:17]  33 60           stream ID, constant within a call
[17]     20              unknown; 0x60 on the last frame
[18:20]  80 dd           flags: 80dd first frame, 805d during, 805e last
[20:22]  d9 bc           sequence, +1 per frame
[22:26]  4d 26 db 9b     timestamp, +480 per frame
[26:]                    burst payload, 26 to 40 bytes
```

**480 samples at 8 kHz is 60 ms, and 60 ms is one DMR voice frame.** The frames
also arrived 60 ms apart, so this is a media clock rather than an arrival time.
Frame lengths cycle 52, 57, 57, 57, 66, 57 — six frames, a DMR superframe,
bursts A to F.

## What is demonstrated, and the one field that is not

Sequence, timestamp, stream ID, call counter and source all moved in ways that
were watched. The call counter counted **1, 2, 3, 4** across four key-ups
including a restart of the probe, so it belongs to the repeater and not the
session — and it sits exactly where a timeslot would sit, which is what made it
worth checking rather than assuming.

**Destination is not demonstrated.** All four transmissions read 455 because all
four went to the same place. Its position is a reading, not an observation. Two
key-ups on different talkgroups settle it in two minutes and that capture has
not been taken.

## What this still does not answer

**Answered on 2026-09-01: it does not, and it does not matter.** The payload is
a 19-byte vocoder core plus a trailer of 0, 5 or 14 bytes. The 14-byte case
totals 33, which is a DMR burst size and a coincidence — the Link Control sits
at the end where a real burst carries it in the middle.

`internal/dmrfec` converts between the two shapes and the conversion is
bit-exact over 884 real bursts, so bridging costs computation and no audio. See
[ADR-0037](../../docs/adr/ADR-0037-dmr-fec-is-a-wrapper-not-a-codec.md).

Also absent: private calls, text, and what a master must send to make a repeater
*play* audio rather than only send it. The probe never replied to a voice frame
and the repeater never asked it to.
