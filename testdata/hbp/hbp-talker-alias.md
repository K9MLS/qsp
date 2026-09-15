# hbp-talker-alias.pcap

**A MOTOTRBO's own Talker Alias, which turned the alias builder from a reading
of tables into a recording.**

| | |
|---|---|
| Taken | 2026-09-15, on the production server |
| Command | `sudo tcpdump -i ens192 -n -s0 -w … 'udp port 62031'` |
| Link type | Ethernet (`-i ens192`) |
| md5 | `73c97deafb588bccad2922976b030aff` |
| Size | 65 051 bytes, 584 packets |
| Contents | one transmission, 140 bursts, from the Pi-Star at 192.168.1.155 |

## The alias

A MOTOTRBO with Inband Caller Alias enabled in CPS, transmitting on the
hotspot's channel. Two PDUs:

```
header  04 00 90 4b 39 4d 4c 53 20
block1  05 00 52 37 00 00 00 00 00
```

Which decode as:

| Field | Value | |
|---|---|---|
| FLCO | `0x04` | Talker_Alias_hdr, table 5.4 |
| FID | `0x00` | standard feature set |
| Format | `2` | UTF-8, table 7.25 |
| Length | `8` | characters, table 7.26 |
| **Reserved bit** | **`0`** | table 7.4's note |
| Header data | `"K9MLS "` | octets 3 to 8, six characters |
| Block 1 data | `"R7"` | octets 2 to 8, two used and five zero |

**Alias: `"K9MLS R7"`.**

## What it settled

`TalkerAliasPDUs("K9MLS R7", TalkerAliasUTF8)` produces **byte-for-byte the
same two PDUs the radio transmitted**. So every value 0370 took from the tables
is confirmed by hardware: the FLCOs, the format code, the five-bit length, the
per-PDU field widths, and the block numbering.

**The reserved bit is the one that mattered.** Table 7.4 carries a note saying
the most significant bit of the 49-bit data field is reserved for the 8-bit and
16-bit formats, and that the first valid character then starts at octet 3. That
bit was written because a footnote said so, with nothing but this project's own
decoder agreeing. The radio sets it to zero and starts `K` at octet 3.

A radio also confirms the choice of format. Motorola's CPS offers UTF-8 and
UTF-16BE, and this one sends format `2` — the format `TalkerAliasPDUs`
implements. UTF-16BE remains refused, because §5.4.3's own character boundaries
for it do not reconstruct.

## Two things only real traffic could have shown

**The alias is sent rarely.** 23 complete embedded LC groups in 140 bursts:
**21 voice LC, one alias header, one alias block.** A radio interleaves the
alias sparingly rather than repeating it every four superframes. Anything
reading an alias off the air must accumulate across a whole transmission and
cannot assume it arrives early — a receiver that gave up after a second would
usually see nothing.

**The alias PDUs are not consecutive.** The first attempt at decoding this
capture looked for a header followed immediately by its blocks and found
nothing, because voice LC groups sit between them. **Reassembly has to collect
by FLCO across the transmission, not by adjacency.** That is a defect that
cannot appear against self-generated fixtures, where the PDUs are built in a
row.

## And the path matters: Homebrew carries this, IPSC does not

Three earlier captures of the same radio through the **XPR8300 over IPSC**
contained no alias at all, and the reason is structural rather than a
misconfiguration.

An IPSC voice packet carries the three AMBE frames and the Link Control
**spelled out as fields** — `00 00 02` for the talkgroup, `2f cd ee` for the
source — not the embedded signalling those bits travelled in. It is the same
fact `EMB-CAPTURE-REQUEST.md` records from another angle: IPSC omits the EMB
because a Motorola repeater knows its own colour code and rebuilds it. **The
repeater terminates the air interface and re-originates it**, so the colour
code goes, and so does anything else riding in the embedded LC.

A Homebrew peer passes the bursts through, which is why the alias is here and
why this morning's Pi-Star capture held 68 complete embedded LC groups.

**The consequence for QSP: it can never receive a Talker Alias from an IPSC
peer.** It can still generate one toward Homebrew peers, which is what
ADR-0064 needs for a Zello user — but an alias is a one-way street across the
IPSC boundary.

## A false positive worth recording

Searching the IPSC captures for a byte whose low six bits were 4 to 7 with a
zero after it produced four hits in perfect equal counts — exactly the shape of
a header and three blocks. They were **frame sequence numbers**: the bytes were
`04 00 00 07 80`, `05 00 00 09 60`, `06 00 00 0b 40`, `07 00 00 0d 20`, where
the value after each counter is that counter times 480. Sequence 4 gives
`0x780` = 1920.

A pattern matching in equal counts is not evidence of structure, and the
arithmetic is what gave it away.
