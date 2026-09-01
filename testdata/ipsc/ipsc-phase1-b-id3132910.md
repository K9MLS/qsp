# ipsc-phase1-b-id3132910.pcap

**The same capture with one setting changed**, which is what identifies the peer
ID field. On its own this file says little; against
[`ipsc-phase1-a-id100.pcap`](ipsc-phase1-a-id100.md) it names four of fourteen
bytes without a specification and without reading anybody's implementation.

SHA-256 `19d3f072996382cccccf185c37e0e7bdb4e4ea31ce29d23ac6ce0c32561aa5e8`

## Provenance

Identical to capture A in every respect — same repeater, same firmware
R02.30.20, same addresses, same master `192.168.1.247:50000`, same disabled
authentication, same `tcpdump -i ens192` — **except**:

| | |
|---|---|
| Date | 2026-09-01, 12:42–12:44 UTC |
| Radio ID | **3132910** (was 100) |
| Capture filter | `host 192.168.1.233`, applied at capture time |
| Duration | 60 s, 18 records |

Codeplug written at 07:40 local (12:40 UTC) per the repeater's own *Last
Programmed* field, between the two captures.

## The differential

```
A  90 00 00 00 64 6a 00 00 80 4c 04 06 04 00     Radio ID 100
B  90 00 2f cd ee 6a 00 00 80 4c 04 06 04 00     Radio ID 3132910
        ^^^^^^^^^^^
```

`0x00000064` = 100. `0x002FCDEE` = 3132910. Both exact.

**The peer ID is a big-endian uint32 at payload offset 1.** Not offset 2, and it
does not extend into byte 5: `0x6a` held still across a change that moved
everything the ID touches, so it belongs to something else. Ten of the fourteen
bytes were unmoved by the only setting that changed.

The UDP checksum moved with the payload, `0x7730` to `0xbd62`, which is the
consistency check that says the difference is real rather than an artefact of
how the file was read.

## A false start worth recording

The first attempt at this capture, at 12:39 UTC, came back **byte-identical to
capture A** and was discarded. The codeplug write had not yet landed — it
completed at 12:40, one minute later. The repeater's own *Last Programmed*
timestamp is what settled it, independently of anything inferred from the bytes.

That is the failure mode of a differential capture: it looks like a finding
about the protocol when it is a finding about the procedure. Read the codeplug
back off the radio before concluding that a field is not where it was expected.

## What is in it

| Flow | Packets | What it is |
|---|---|---|
| `192.168.1.233:50002 → 192.168.1.247:50000` | 7 | registration request, new ID |
| `192.168.1.247 → 192.168.1.233` | 7 | ICMP port unreachable |
| ARP | 4 | address resolution |

Retry cadence unchanged at ten seconds, confirming it does not depend on the ID.

## What remains unknown

Bytes 5 to 13 — `6a 00 00 80 4c 04 06 04 00`. Both captures came from one
repeater on one firmware with one codeplug, so their being identical says
nothing about whether they are constant. `internal/protocol/ipsc` records them
as `ObservedTrailer` and **accepts any value**, so that a repeater sending
something else is understood rather than rejected on the strength of one
XPR8300.

The next single-variable captures that would name more of them: timeslot 2
disabled, a different master UDP port, a different CAI network number. Each is a
two-minute capture and each changes exactly one thing.
