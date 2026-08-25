# hbp-voice-session.pcap

Seven complete DMR voice transmissions with their headers and terminators,
captured across two HBP links simultaneously.

## Provenance

| | |
|---|---|
| Captured by | K9MLS |
| Date | 2026-08-23 |
| Hotspot | WPSD (Pi-Star fork), MMDVM_HS_Dual_Hat, MMDVMHost + DMRGateway |
| Master | BrandMeister 3102 United States |
| Link type | LINUX_SLL2 (`tcpdump -i any`) |
| Duration | 383 s, 1072 packets, 916 DMRD frames |

## What it contains

All seven transmissions are from radio ID 3132910 to talkgroup 9999, TS2,
group call.

| Stream ID | Frames per link | Duration |
|---|---|---|
| `0x3afcd858` | 182 | 10.92 s |
| `0x74e24778` | 80 | 4.86 s |
| `0x14d361d4` | 68 | 4.14 s |
| `0xcbf751c0` | 50 | 3.06 s |
| `0x0580a89a` | 32 | 1.98 s |
| `0x232715cb` | 32 | 1.98 s |
| `0x470d0e37` | 14 | 0.90 s |

Each stream appears on **both** links, so there are 14 link traversals of 7
logical streams. Any analysis must key on stream ID **and flow**; keying on
stream ID alone double-counts.

### DMRD layout (55 bytes)

| Offset | Len | Field |
|---|---|---|
| 0 | 4 | `"DMRD"` |
| 4 | 1 | sequence |
| 5 | 3 | source radio ID |
| 8 | 3 | destination ID |
| 11 | 4 | repeater ID |
| 15 | 1 | bits: `0x80` slot, `0x40` call type, `0x30` frame type, `0x0F` data type |
| 16 | 4 | stream ID |
| 20 | 33 | DMR payload |
| 53 | 2 | trailing bytes (BER/RSSI, added by MMDVMHost) |

### Verified invariant

**Every stream carries exactly two frame-type-2 (sync) packets** — the voice
header and the voice terminator — bracketing its voice frames. This holds for
all 14 stream/flow pairs and is the primary structural check for a parser.

### The repeater-ID rewrite — observed, but NOT in this fixture

The **raw** capture showed that a gateway rewrites the repeater ID when relaying
a frame inbound:

```
WAN (from master):        repeater_id = 3132910    (the station's own ID)
loopback (from gateway):  repeater_id = 1074180087 (0x4006aff7)
```

**This behaviour is not present in the committed fixture.** The only inbound
frames in the raw capture were the third-party transmissions removed for consent
reasons, and the rewrite went with them. Sanitising for privacy destroyed the
evidence for a protocol behaviour.

It is recorded here because the observation was real and losing it would be
worse than noting the gap. It must be treated as **unverified** until a capture
containing the operator's own inbound traffic reproduces it. A talkgroup 9990
parrot session would supply that.

`TestRepeaterIDFieldPerLink` asserts what this fixture actually contains, so the
gap is visible in the test suite rather than buried in prose.

## Sanitization

**182 DMRD frames from two other stations were removed** — radio IDs 3202678
and 3126165, both to talkgroup 31673. The operator's BrandMeister static
talkgroups were active during capture and third-party traffic arrived.

Their removal deletes whole packets and does not alter any retained packet, so
every remaining stream keeps its header and terminator intact. This was
verified after stripping.

WPSD dashboard status polling and P25 gateway traffic were also removed; the
P25 portion is preserved separately in `testdata/p25/`.

Callsign, radio ID and frequencies are retained as public information.

## Known limitations

**All seven streams are outbound only.** The operator transmitted to talkgroup
9999, which did not echo. There is no example here of a voice stream arriving
from a master.

Inbound voice appears only in the third-party frames that had to be removed for
consent reasons. **A future capture using talkgroup 9990 (BrandMeister Parrot)
would supply a clean round-trip** and should be added. It would also restore the
repeater-ID rewrite evidence described above, using the operator's own traffic
rather than a third party's.
