# P25: what it would take

**Status:** the reflector half is built and running; the Motorola half is scope.

**Updated 2026-09-12.** [ADR-0057](adr/ADR-0057-p25-is-a-full-network.md)
commits QSP to a **full P25 network** — a Motorola P25 repeater is a peer of a
QSP server in the same sense a Motorola DMR repeater already is, speaking an
interface that is QSP's own. This document records the route to that, and the
scaffolding that carries traffic in the meantime.

**The reflector half shipped 2026-09-11**, patches 0326–0333, built from three
captures taken off the operator's Pi-Star. Hotspots reach QSP through
P25Gateway on port 41000. Nothing in that path decodes audio.

Originally researched 2026-09-06 after the operator asked whether a P25-only
network — Motorola Quantar to Motorola Quantar, never touching DMR — could be
built without buying a vocoder dongle.

The answer is yes, and the reason is [ADR-0034](adr/ADR-0034-p25-is-native.md):
a dongle exists to transcode, DMR is AMBE+2 and P25 Phase 1 is IMBE, and if P25
never meets DMR then nothing transcodes. QSP would move IMBE frames from one
endpoint to another as opaque payload. **A P25-only network is the designed
case, not a workaround.**

## The two problems, which are not the same difficulty

### The reflector side is ordinary work

P25 hotspots — Pi-Star, WPSD, MMDVMHost — reach a network through G4KLX's
P25Gateway, which speaks a UDP protocol to a reflector on port 41000 by
default. Hosts are configured as a talkgroup, an address and a port.

This was the direct analogue of the Homebrew work: a listener, registration,
peer records, call tracking, relaying. **No vocoder work of any kind.**

**Built 2026-09-11.** `internal/protocol/p25` holds the frame layer and
`internal/p25link` the listener. Nine frames per logical data unit, the
talkgroup in frame `0x65` and the source radio in `0x66`, and there is no
registration — a gateway polls and the far end returns the identical datagram.
All 565 frames of `testdata/p25/p25-voice.pcap` round-trip byte for byte.

**Not yet met a real gateway.** The listener is tested against a real UDP
socket with real frames from the captures, and no P25Gateway has ever linked to
it. Pointing the Pi-Star's P25Gateway at the test server is the cheapest real
test available and needs no hardware.

### The Quantar side is a different shape of problem

**Easier on protocol than IPSC was.** There is no vocoder work, no BPTC, no FEC
wrapper, no superframe reconstruction, no timeslot bit — the entire class of
defect that consumed the IPSC effort does not exist here. There is also a
published standard and prior public reverse engineering, where IPSC had
neither.

**Harder on plumbing.** A Quantar's linking interface is not IP. It is a V.24
daughtercard on the wireline board, and V.24 names the physical interface only;
the protocol above it is bit-oriented HDLC. Nothing in QSP opens a serial port.

## What is known about the V.24 interface

From the reverse engineering published at
<https://wiki.w9cr.net/index.php/Quantar_V.24_Interface>, taken from Matt Ames'
wiki and originating in work on the p25.ca forum. It was done with an HP J2302
protocol analyser, frame by frame — the same method this project uses, done by
somebody else a decade earlier.

- The link is established with SABM frames, answered by UA, followed by an XID
  exchange in both directions.
- Both ends then send Receive Ready frames as keepalives. A Quantar that hears
  none for about five seconds reverts to sending SABM.
- Conventional P25 speech travels in unnumbered information frames carrying the
  encoded voice, in frames of roughly 18 to 34 bytes.
- The voice frames carry a sequential first byte across a run, which looks like
  the same kind of superframe structure IPSC turned out to have.
- The wireline board's J300 header exposes the interface at 5V TTL without the
  daughtercard, with the pinout documented on that page. Cable length is limited
  to three or four feet, in an RF-hot environment, so it needs screening.

**None of this has been verified by this project.** It is somebody else's
capture, and it is recorded here as a starting point for ours, not as fact.

## The published standard

TIA-102.BAHA, *Project 25 Fixed Station Interface Messages and Procedures*,
June 2006, describes the Digital Fixed Station Interface. A fixed station
connects to one host via either an analog or a digital fixed station interface;
the digital one supports the models appropriate to the P25 common air
interface. A copy is at
<https://www.qsl.net/kb9mwr/projects/dv/apco25/TIA-102.BAHA-2006.pdf> and the
TIA-102 series is on archive.org.

**The permission question is narrower than it first appears.**

ADR-0029 governs IP Site Connect, which has no published specification, and
forbids reading other people's *implementations* because of the derivative-work
consequence. [ADR-0040](adr/) already settled the other case: the DMR air
interface is ETSI TS 102 361-1, a free download, and `internal/dmrfec` carries
Golay, Reed-Solomon and BPTC(196,96) from it with a clause citation on each.
**A published standard is neither a capture nor somebody's code.**

So TIA-102.BAHA is the P25 analogue of TS 102 361-1 and the existing precedent
covers it. What ADR-0029 still governs is anything Motorola-proprietary — the
V.24 framing above HDLC has no published specification, and the third-party
write-ups of it are somebody else's captures rather than a standard.

**What follows is the original framing, kept because the distinction is worth
seeing rather than being told.** That ADR forbids reading
DMRlink, HBlink3 or other implementations, and says protocol knowledge must
come from captures. A published standard is a specification rather than
somebody's code, and the V.24 write-ups are captures somebody else took and
published — but this would be the first time protocol knowledge entered the
project from a document.

The decision belongs to the operator and should be an ADR before any P25 code
is written, not a comment discovered afterwards. **It is now
[ADR-0058](adr/ADR-0058-the-p25-fixed-station-interface-is-specified.md),
Proposed**, carrying this recommendation unchanged: reading the standard is
fine, reading anyone's implementation stays forbidden, a third-party capture is
a hypothesis and never a fact, and a claim taken from the standard is still
verified against a capture before code depends on it — exactly as an inference
is today.

## The GTR 8000, which this document did not mention until 2026-09-12

The operator owns one, and it looks like the easier box because it has an
Ethernet port. For this purpose it is the harder one.

**Its V.24 is the same interface as a Quantar's.** Motorola's own manual says
the GTR 8000 offers 4-wire and V.24 connections to a DIU or an ASTRO-TAC 3000
Comparator *using the same V.24 connector pin-outs as a QUANTAR*, and a
Motorola representative has confirmed the station is both V.24 and IP capable
with no separate option needed to enable V.24 — though the conventional
software option must be ordered. So one V.24 rig serves both repeaters, and the
GTR 8000 adds nothing to the protocol question.

**Its IP interface is not DFSI.** The manual describes a V.24 interface to a
Channel Bank, DIU, Conventional Channel Gateway, MLC 8000 or ASTRO-TAC 3000,
and an IP interface carrying digital voice and data *to a CCGW or GCM 8000
Comparator*. Both are Motorola-proprietary and unpublished. The clearest
evidence is that a product exists to translate it: the RIC-Mz converts Motorola
V.24 into TIA-102.BAHA-A DFSI over IP and is sold as speaking both Quantar V.24
and GTR IP. If GTR IP were DFSI there would be nothing to convert.

**And it is a dealer-tooled box.** It is configured with CSS and the Software
Download Manager rather than RSS, and its features are software-licensed per
option. A Quantar's V.24 path is thoroughly documented in amateur hands; a
GTR 8000's conventional V.24 configuration may not be reachable without a
dealer.

**Recommendation: bring up the Quantar first.** Not because the GTR 8000 is out
of scope — ADR-0057 says both are in it — but because it is the same protocol
behind a worse door, so it teaches nothing the Quantar does not and costs more
to try. Once the Quantar path works, the GTR 8000 is a cable and a codeplug.

## The network side, which is the larger half

**[docs/P25-NETWORK.md](P25-NETWORK.md)** holds the assessment of what it takes
for P25 to be as capable as the DMR side: the parity table, the one unsolved
problem (P25 has no stream identifier), the one permanent limitation (a P25
gateway does not authenticate), two capacity facts, and the staged order of
work. Written 2026-09-12.

The short version: the Quantar is one row of that table, everything else is the
network, and **none of the network work needs hardware.** The first item —
talkgroup routing and contention inside `internal/p25link` — closes a real
defect today: the talkgroup is read and then not used for routing, so QSP is
currently a flat reflector where every gateway hears every transmission.

## QSP is the repeater's master directly

**Decided 2026-09-12, [ADR-0060](adr/ADR-0060-qsp-terminates-the-serial-tunnel.md).**
QSP terminates the serial tunnel itself, reads the Motorola framing and carries
the audio. No bridge host and nothing between the router and QSP.

The research that produced the scaffold below also dissolved the argument for
it. A Quantar's V.24 link is carried over IP by a Cisco router using STUN,
Cisco's serial tunnel — `basic` mode carries everything from one side to the
other on TCP 1994 — so **nothing opens a serial device on this path**. The
router does the physical layer in hardware and hands over a TCP stream, which
is the same shape as every other listener QSP has. And `stun route all tcp`
accepts any address, so the captures need no second machine either.

### The four phases, and the instrument for each

| Phase | What | Instrument |
|---|---|---|
| 1 | Capture the keepalive: tunnel pointed at a QSP host, `tcpdump` plus a socket that accepts and records | The wireline card's LED, which flashes while the link is down |
| 2 | QSP answers the keepalive | The LED goes steady — *the poll is the registration*, a second time, on a different transport |
| 3 | Capture and parse voice, handheld into a dummy load | Frame counts against the reflector side's, and `internal/protocol/p25`, which already reads these logical data units |
| 4 | Relay a Quantar transmission to a hotspot and back | Last heard, and the traffic counters on both sides |

**No code before phase 1 produces bytes.** The Motorola framing above HDLC has
no published specification, so this is ADR-0029 exactly: captures taken here,
never anyone's implementation. Scale, honestly: comparable to the IPSC listener
or the P25 reflector — several sessions, not one.

## The DVSwitch chain, which is now a fallback rather than a first step

Available before any QSP code, and no longer the plan.

```
Quantar ──V.24──▶ Cisco WIC-1T ──▶ Quantar_Bridge ──▶ MMDVM_Bridge ──▶ P25Gateway ──UDP:41000──▶ QSP
```

Quantar_Bridge lets a Quantar operate natively P25 on the MMDVM reflector
system, using the same pieces the P25NX network uses to reach a Quantar; a
repeater already connected to P25NX needs no changes, only a different process
started. The far end QSP would see is **P25Gateway** — the same software the
three existing captures came from — so QSP needs no new protocol knowledge at
all for this.

**What it still buys, after ADR-0060:**

- It keeps a repeater on the air while the native path is being written.
- It is a known-good far end if phase 3 turns out to need one.

**What it no longer buys.** Its main justification was being a reference to
measure against, and the capture is that reference — taken with `tcpdump` and a
socket, with none of the four processes, two of which this project cannot
instrument. If phases 1 and 2 go cleanly it is never stood up.

**What it costs, stated so it is not forgotten**: a defect can live in five
processes and QSP owns one. This project's diagnostic method is to log the same
fact at two layers and read the gap, and four of those layers are not ours to
instrument. Tolerable to prove a path, not as a standing arrangement.

**It comes out when the native interface carries a call.** A scaffold with no
removal date becomes the building.

### DFSI, which is a different box rather than a substitute

Still worth doing, and not the same thing as being a Quantar's master.

The published standard is TIA-102.BAHA-A, the Digital Fixed Station Interface,
which is what the commercial converters and third-party consoles already speak.
Implementing from that document is [ADR-0058](adr/ADR-0058-the-p25-fixed-station-interface-is-specified.md),
which is Proposed and needs a yes.

Speaking DFSI has a second payoff beyond the Quantar: it is the interface a
RIC-Mz presents, which is also how a GTR 8000 reaches QSP without QSP ever
learning a proprietary protocol, and it is what third-party dispatch consoles
consume. No free software occupies that position today.

## The capture problem

**A V.24 link cannot be captured with tcpdump.** It is synchronous serial, not
Ethernet, so the differential method that has been right twelve times has no
instrument here.

The way out is the converter hardware, which turns it back into something
loggable. Two routes:

- **Cisco router with a WIC-1T serial module**, the P25NX approach. Documented
  at <https://www.qsl.net/kb9mwr/projects/dv/apco25/> and used by several
  networks. The operator already owns a router.
- **W3AXL DVM-V24-V2**, which converts the synchronous 9600-baud HDLC on the
  V.24 RJ45 to asynchronous serial over USB-C at 115200 baud, and is confirmed
  against Quantar and Quantro.

Either way the traffic becomes something a file can hold, and a log of framed
bytes is this project's equivalent of a pcap.

## What is decided, and what is still open

**Decided** ([ADR-0057](adr/ADR-0057-p25-is-a-full-network.md)): the P25 side is
a full network. A Motorola P25 repeater is a peer of a QSP server, speaking an
interface QSP owns. QSP is not a client of somebody else's P25 network.

**Decided 2026-09-12, [ADR-0060](adr/ADR-0060-qsp-terminates-the-serial-tunnel.md)**:
which process opens the V.24 serial port. **None of them.** The Cisco router
does the physical layer in hardware and carries the frames over TCP with STUN,
so QSP terminates a tunnel rather than a serial device, and there is no bridge
host. A USB V.24 converter feeding a serial device remains a possible second
path and is not what this builds.

**Still open**: [ADR-0058](adr/ADR-0058-the-p25-fixed-station-interface-is-specified.md),
the permission to implement from TIA-102.BAHA-A. It gates DFSI, not the Quantar
work — the Motorola framing has no standard to read either way.

**This was nearly decided the wrong way round.** The first write-up of the
2026-09-12 research argued against QSP owning the repeater interface at all,
using an argument that was really about where a few hundred lines of serial code
live. Two questions collapsed into one; see ADR-0057's context. The scope
question is settled and this one is not.

Answered from a capture, not from argument. §8a: when a reading is plausible and
cheap to test, test it rather than arguing for it.

## Hardware

The operator has Quantars and a Cisco router, so the shopping list is short or
empty. Recorded for completeness:

| Piece | Notes |
|---|---|
| Quantar with wireline card | CLN695x or newer. **Held** |
| GTR 8000 | Same V.24 pinout as a Quantar; CSS-configured, features licensed. **Held** |
| V.24 access | Motorola TTN4010 daughtercard, or the W9CR level shifter that replaces it — sold by W3AXL, with a built-in V.24 network tap that is an instrument in its own right. Or direct off J300 at TTL |
| V.24 to something loggable | Cisco WIC-1T with a CAB-232FC and a CAT-5 to DB-25 cable. **Router and WIC held.** The V.24 card must be set for external clocking for this path |
| A P25 handheld | To key with. **Held** — an APX, already used for the talkgroup captures |
| A second Quantar | For the back-to-back configuration the linking work is built around |
| DVM-V24-V2 | Converts the synchronous 9600-baud HDLC to async serial over USB-C at 115200. **Belongs to the other route** — it feeds dvmhost, which speaks its own network protocol rather than the reflector protocol QSP implements. Not needed for the scaffold |

## The plan, in order

1. ~~**Capture the reflector protocol on the Pi-Star.**~~ **Done 2026-09-11.**
   Three captures, and the listener built from them.
2. **Point the Pi-Star's P25Gateway at QSP.** Free, no hardware, and it closes
   the largest untested P25 claim: the listener has never met a real gateway.
3. **Decide [ADR-0058](adr/ADR-0058-the-p25-fixed-station-interface-is-specified.md).**
   The scaffold in step 4 does not need it; everything after step 5 does.
4. **Buy the V.24 daughtercard.** The only item on the hardware list that
   survives every route, including the one that replaces all of this.
5. **Build the capture rig**: router, serial card, cable, the DTR jumper in the
   DB-25 hood, tunnel pointed at a QSP host. No bridge host, no DVSwitch.
6. **Phase 1 and 2** — capture the keepalive, then answer it. The wireline
   card's LED going steady is the milestone.
7. **Phase 3 and 4** — voice, then relay.
8. **Then the GTR 8000**, which by then is a cable and a codeplug.

The scaffold is not in this list. It is a fallback if phase 3 needs a
known-good far end, and a way to keep a repeater on the air meanwhile.

No longer deferred. ADR-0034 deferred this until the DMR and IPSC work was
finished, and the routing core was assessed as essentially complete on
2026-09-11.
