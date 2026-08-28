# The DMR Homebrew Protocol, as observed

Two sources, and every claim below says which one it comes from:

- **Observed** — derived from the captures in `testdata/hbp/`.
- **Specified** — derived from the published Homebrew repeater protocol
  specification (DL5DI, G4KLX, DG1HT, 2015-07-26). No implementation source has
  been read; see [ADR-0008](../adr/ADR-0008-protocol-licensing.md).

**Where the two disagree, observed wins.** QSP has to interoperate with the
software people actually run, not with the document.

## Message catalogue

| Tag | Size | Direction | Purpose |
|---|---|---|---|
| `RPTL` | 8 | peer → master | Login request |
| `RPTACK` | 10 | master → peer | Salt, or acknowledgement — **ambiguous** |
| `RPTK` | 40 | peer → master | Authentication response |
| `RPTC` | 302 | peer → master | Full configuration |
| `RPTPING` | 11 | peer → master | Keepalive |
| `MSTPONG` | 11 | master → peer | Keepalive reply |
| `DMRD` | 53 + 0–8 | both | Voice or data frame |
| `DMRC` | 119 | peer → gateway | Abbreviated configuration |
| `DMRP` | 4 | gateway → peer | Keepalive reply, no ID |

### Specified but never observed

Implemented from the document, with no fixture behind them. Each is a tag
followed by a four-byte repeater ID.

| Tag | Size | Purpose |
|---|---|---|
| `MSTNAK` | 10 | Master rejects a login, or tells a stale peer to log in again |
| `MSTACK` | 10 | The acknowledgement as specified — real masters send `RPTACK` |
| `MSTCL` | 9 | Master shutting down |
| `RPTCL` | 9 | Peer disconnecting cleanly |
| `MSTPING` | 11 | Master-initiated keepalive |
| `RPTPONG` | 11 | Peer's reply to `MSTPING` |

`RPTCL` shares its first four bytes with `RPTC`, so dispatch tests the longer
tag first. Without that, a peer's clean disconnect would parse as a
configuration announcement.

### Neither specified nor observed

Still refused with `ErrNotCaptured`: `RPTO` (peer options) and `RPTSBKN`
(beacon).

---

## Where the specification and reality disagree

Three divergences, all found by comparing the document against the capture. In
each case QSP follows what was observed, because that is what interoperates.

### 1. The acknowledgement tag

The specification says a master replies `MSTACK`. **BrandMeister and MMDVMHost
use `RPTACK`.** QSP sends `RPTACK` and accepts both.

### 2. Keepalive direction

The specification has the master polling (`MSTPING`) and the peer answering
(`RPTPONG`). **Observed traffic runs the other way**: the peer sends `RPTPING`
every 10 seconds and the master answers `MSTPONG`. QSP answers `RPTPING`, and
understands the specified direction too.

The specification also says pings are sent every minute; observed interval is
10 seconds.

### 3. The flags byte — the one that mattered

The specification's table describes the bit layout as:

```
Slot       0b0000000x     Call type  0b000000x0
Frame type 0b0000xx00     Data type  0bxxxx0000
```

**Real traffic is the exact inverse of that**, and the difference is not
cosmetic. Decoded per the table, the capture yields transmissions that switch
timeslot mid-stream, flip between group and private, and carry voice-sequence
numbers of 8, 9 and 10 — values that do not exist in DMR.

Decoded as QSP implements it:

```
Slot  0x80    Call type 0x40    Frame type 0x30    Data type 0x0F
```

the same bytes yield a voice sequence cycling 0–5 (the A–F superframe), a voice
sync frame at the start of each superframe, one timeslot per stream, and exactly
two data-sync frames per transmission — the header and the terminator.

**14 of 14 captured streams satisfy that. 0 of 14 satisfy the table's reading.**

The table is presumably using a different bit-numbering convention. Either way,
this is the clearest argument in the project for capturing before implementing:
a specification read literally would have produced a decoder that was wrong
about every frame.

## Login

```
peer                                          master
 │  RPTL   + repeater_id(4)                      │
 ├───────────────────────────────────────────────▶
 │                        RPTACK + salt(4)       │
 ◀───────────────────────────────────────────────┤
 │  RPTK   + repeater_id(4) + digest(32)         │
 ├───────────────────────────────────────────────▶
 │                        RPTACK + repeater_id   │
 ◀───────────────────────────────────────────────┤
 │  RPTC   + repeater_id(4) + config(294)        │
 ├───────────────────────────────────────────────▶
 │                        RPTACK + repeater_id   │
 ◀───────────────────────────────────────────────┤
```

Completed in ~30 ms in the captured session. `RPTPING`/`MSTPONG` follows every
10 seconds.

### Authentication

```
digest = SHA-256(salt ‖ password)
```

Four salt bytes immediately followed by the shared password as raw bytes. No
separator, no length prefix, no text encoding.

Verified empirically against a live login before the fixture was sanitised, and
pinned by a known-answer test using a published test password.

**Getting this wrong means no peer can ever connect, with no partial credit.**

### The RPTACK ambiguity

`RPTACK` has one wire shape and two meanings, distinguished only by what the
peer last sent. The codec does not choose; see
[ADR-0010](../adr/ADR-0010-protocol-codec-shape.md).

## Configuration field layout

`RPTC` — 294 bytes of fixed-width, space-padded ASCII after the tag and ID:

| Offset | Len | Field |
|---|---|---|
| 8 | 8 | callsign |
| 16 | 9 | rx_freq |
| 25 | 9 | tx_freq |
| 34 | 2 | tx_power |
| 36 | 2 | color_code |
| 38 | 8 | latitude |
| 46 | 9 | longitude |
| 55 | 3 | height |
| 58 | 20 | location |
| 78 | 19 | description |
| 97 | 1 | slots |
| 98 | 124 | url |
| 222 | 40 | software_id |
| 262 | 40 | package_id |

Sums to exactly 302. `DMRC` is identical through offset 38, then **omits**
latitude through url, continuing at `slots`. Sums to exactly 119.

That both layouts close with no remainder is what confirms these boundaries.

## DMRD

| Offset | Len | Field |
|---|---|---|
| 0 | 4 | `"DMRD"` |
| 4 | 1 | sequence |
| 5 | 3 | source radio ID |
| 8 | 3 | destination ID |
| 11 | 4 | repeater ID |
| 15 | 1 | flags |
| 16 | 4 | stream ID |
| 20 | 33 | DMR burst |
| 53 | 0–8 | trailer (MMDVMHost sends 2) |

Flags byte: `0x80` timeslot, `0x40` call type, `0x30` frame type, `0x0F` data
type. The low nibble's meaning depends on frame type and was not established
from the capture, so it is exposed raw.

### Stream structure

A **stream** is one keyup. Every frame of a transmission shares a stream ID; it
changes on the next keyup.

**Each stream contains exactly two frame-type-2 (sync) frames** — the header and
the terminator. This holds for all fourteen stream traversals in the voice
fixture and is the primary structural check on a decoder. A stream that loses
its terminator is the classic cause of a bridge welded open.

### Stream identity is per-link

A gateway relays a transmission onto a second link **with the same stream ID**.
Anything counting streams must key on stream ID *and* the connection it arrived
on. Keying on stream ID alone silently merges two traversals and double-counts
every frame — a mistake made and caught during this work.

### Repeater ID is not transmission identity — *unverified*

The raw capture showed a gateway substituting its own value into the repeater ID
field when relaying inbound frames (`3132910` on the master link became
`1074180087` on the local link).

**This is not demonstrated by any committed fixture.** The only inbound frames
in the raw capture were third-party transmissions removed for consent reasons,
and the evidence went with them.

It is recorded because the observation was real, but it must be treated as
unverified until a capture of the operator's own inbound traffic reproduces it.
A talkgroup 9990 parrot session would supply that.

`SourceID` is the radio that keyed up; `RepeaterID` is only the sender of that
particular packet.

## Validated against real hardware

**2026-08-23.** A WPSD hotspot (MMDVMHost + DMRGateway, radio ID 3132910)
connected to QSP as a custom DMR master, registered, and stayed connected for
three and a half minutes with keepalives cycling normally.

Confirmed by that run:

- The six-step login handshake, end to end
- `SHA-256(salt ‖ password)` — MMDVMHost computed the digest QSP expected
- The `RPTC` field offsets: callsign and colour code decoded correctly, and the
  colour code matched the hotspot's own dashboard
- **The keepalive direction.** The specification says the master pings and the
  peer answers; QSP follows the captured traffic instead, and MMDVMHost
  confirmed that reading by staying connected

**2026-08-25.** The same hotspot, with a DMRGateway rule routing TG 11 on TS2
to QSP, carried five live transmissions.

Confirmed by that run:

- **`DMRD` decoding against a live radio.** 556 voice frames across five
  streams, zero dropped, zero collisions.
- **Frame timing.** Rates of 16.44–16.59/s against DMR's nominal 16.67/s, held
  across durations from 3.77 s to 14.58 s. Within 1.5 % throughout.
- **Byte-exact round-tripping.** All 576 LAN payloads — `Data`, `Ping`, `Pong` —
  re-marshal identically to what arrived.
- **The talkgroup rewrite.** Dialled as TG 11, arrived as TG 9, matching
  `TGRewrite0=2,11,2,9,1`.
- **Both dialects concurrently.** The capture holds the master link's 11-byte
  `MSTPONG` and the MMDVMHost/DMRGateway loopback's 4-byte `DMRP` side by side.

Fixture: [`testdata/hbp/hbp-voice-live.pcap`](../../testdata/hbp/hbp-voice-live.pcap).

Still unvalidated: forwarding between two real peers, and the
specification-derived messages (`RPTCL`, `MSTNAK`).

**A trap when reading these captures.** Frames below the 60-byte Ethernet
minimum are zero-padded, and the padding is recorded. An 11-byte `MSTPONG` makes
a 39-byte IP datagram padded by 7 bytes, so slicing from the end of the UDP
header to the end of the record yields an 18-byte message no parser accepts —
indistinguishable at a glance from a protocol defect. Clip to the UDP length
field; `internal/peers/pcap_test.go` is the reference.

## Master-side behaviour

`internal/peers` implements the master half of this protocol, derived from
BrandMeister's observed replies in the login capture.

State progression, strictly ordered — an out-of-order message is dropped:

```
        RPTL              RPTK               RPTC
  (none) ───▶ challenged ───▶ authenticated ───▶ configured
                                                    │
                                          may pass traffic
```

Only a **configured** peer may send `DMRD`. Accepting frames from a merely
authenticated peer would route traffic from a station whose timeslot and colour
code are still unknown.

Timeouts: an incomplete handshake expires after 30 s, a configured peer after
60 s of silence — five missed keepalives at the observed 10 s interval.

## DMRGateway blanks the announced position

**A hotspot behind DMRGateway announces latitude and longitude of zero, and
there is nothing QSP can do about it.**

Observed on 2026-08-28 against a WPSD hotspot. `/etc/mmdvmhost` held
`Latitude=33.1481` and `Longitude=-97.1201`; the `RPTC` arriving at QSP carried
`0.000000` and `00.000000` in those fields, with the location text `Denton,
EM13kd` intact immediately after them — which is also what confirms the field
offsets, since a shifted layout would have corrupted the place name too.

DMRGateway builds its own `RPTC` for each upstream rather than forwarding the
one MMDVMHost produced. Setting `Enabled=1` in its `[Info]` block, with correct
coordinates already present there, changed nothing across a service restart.

Two consequences worth stating plainly:

- **QSP's position handling is correct and its map will be empty for most WPSD
  users**, because DMRGateway is the common configuration. A hotspot pointed
  straight at a master, with no gateway in between, is the case where a position
  arrives.
- The Null Island rule in `internal/peers/position.go` was a guess when it was
  written: 0,0 is what an unset field looks like rather than a place. This is
  the hardware that confirms it. Without it, the first pin QSP ever drew would
  have been in the Gulf of Guinea.

## Gaps

| Gap | What would close it |
|---|---|
| `MSTNAK` on a refused login never seen on a wire | A login with a wrong password. The stale-peer use is exercised on every master restart. `RPTCL` is no longer in this row: a WPSD hotspot sent one on 2026-08-28 and the master logged `peer disconnected cleanly`, which is the behaviour this implementation was written for. A capture would still be worth keeping |
| Relay repeater-ID rewrite unverified against hardware | Two physical hotspots. Verified against synthetic peers in `internal/peers/fanout_test.go`, which replays the captured frames through the real protocol stack — the rewrite is correct, but no second radio has received the result |
| `description` / `slots` split unverified | A hotspot configured for one timeslot |
| `RPTO` options string | Capture a hotspot configured with options |
| Trailer bytes uninterpreted | Correlate with MMDVMHost's reported BER/RSSI |
| Whether any gateway forwards a position | A hotspot connected directly to a master, with no DMRGateway between. DMRGateway is known to blank it; nothing else has been tried |
