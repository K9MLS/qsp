# P25: what it would take

**Status:** the reflector half is built and running; the Motorola half is scope.

**Updated 2026-09-26.** [ADR-0057](adr/ADR-0057-p25-is-a-full-network.md)
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
  exchange in both directions. **Seen here on 2026-10-04**: this station sends
  `FD 3F` every 0.51 seconds until accepted (`testdata/quantar`), and QSP
  0.1.303 accepts it. The XID exchange has not been seen here yet.
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
live. Two questions collapsed into one; see ADR-0057's context.

## Hardware, and how it goes together

Sources: ZL4JY's *IP link Quantar V.24 systems using Cisco routers* (2012,
uploaded to the project 2026-09-12), the W9CR wiki, and the DVSwitch
Quantar-Bridge group.

### The chain

```
APX handheld
    |  RF
Quantar - Wireline board (CLN695x+) - V.24 daughtercard - RJ-45
    |  straight-through Ethernet patch lead
RJ-45 to DB-25 male adapter hood     [pin 6 -> pin 20 jumper inside]
    |  CAB-232FC (DB-60) or CAB-SS-232FC (Smart Serial) -- DCE, never DTE
Cisco serial card - DCE, clockrate 9600, encapsulation stun
    |  TCP 1994, STUN basic
QSP
```

### The parts

| Piece | Notes | Status |
|---|---|---|
| Quantar with wireline card | CLN695X or newer | **held** |
| GTR 8000 | Same V.24 pinout as a Quantar; CSS-configured, features licensed | **held** |
| V.24 daughtercard | Motorola TTN4010, the W9CR level shifter sold by W3AXL, or an aftermarket board from AE4ML on the DVSwitch group. **The only item that survives every route.** The station's V.24 interface has never been alive — its **AUX LED has never lit**, identified 2026-09-27 — and a replacement card is on the way from KD9EJA; which card it is was not yet known when this was written | **inbound — the only thing still needed** |
| RJ-45 to DB-25 male adapter | Norcomp RJADK25P7080831 hood hardware, plus loose pins (Norcomp 100 170-101-170L001) for the jumper below. **Built, and the jumper proven electrically** on 2026-09-26 by `DTR=up` — see bring-up step 2 | **held** |
| Cisco serial cable | CAB-232FC for a WIC-1T, CAB-SS-232FC for WIC-2T/HWIC. Male DB-60 to **female** DB-25, with "Cisco" and "DCE" moulded in. **Not** the DTE cable with the male DB-25. A group thread discusses using CAB-SS-232MT instead; unresolved from the title alone, so check what is actually in hand before ordering. **In `Serial0/3/0` and confirmed DCE RS-232 on 2026-09-26** — the card reads the cable's own identification and `show controllers` prints it, which settles the DTE question without unscrewing anything | **held** |
| Router | **Settled: the operator's CISCO2921/K9**, 15.4(3)M3 universalk9, STUN verified at the console 2026-09-13 after self-activating the `datak9` right-to-use licence. Sixty days from that date | **held** |
| Serial card | **HWIC-2A/S in slot 0/3**, two Smart Serial ports. `encapsulation stun` accepted on `Serial0/3/0`, 2026-09-21, so the HWIC-1T this row used to name is not needed. Legacy WICs still do not fit an ISR G2 | **held** |
| Console cable | Light blue RJ-45 to DB-9 plus a USB serial adapter. One ships with every Cisco router and can often be had for the asking | **held** |
| Dummy load | For bench work. Named in every published build | **needed** |
| RS-232 breakout box | Shows the handshake states and clock activity. Strongly recommended — there is no other instrument for a synchronous serial link | recommended |
| P25 handheld | An APX, already used for the talkgroup captures | **held** |

**No bridge host.** ADR-0060: no Pi, no second virtual machine.

### The two things that stop it working

**The DTR jumper.** The Cisco serial interface as DCE needs DTR on pin 20
asserted, and the Motorola RJ-45 has too few pins to carry one. The solution is
Cisco's own DSR output on pin 6 driving DTR — **link pins 6 to 20 inside the
DB-25 hood.** ZL4JY built a batch of adapters before discovering this; without
it everything looks correctly wired and the link never comes up.

**Clocking direction.** The Quantar must take clock from the Cisco, which is
the "Cisco clocks Quantar TX data" wiring option and `External Transmit Clock:
ENABLED` in the codeplug, with `clockrate 9600` on the router. Then the
daughtercard switches:

- **OEM TTN4010:** S101 switch 1 on, the rest off. Often shipped all off.
- **W9CR board:** DIP 1 and 4 on. Cisco provides clock on TX and RX, sends CTS
  while the Quantar sends RTS, and needs CD active. Verified 2024-06-26.

**These two lines are for different boards and the distinction is load-bearing.**
On 2026-09-26 the "1 and 4" figure was quoted at a station carrying a Motorola
wireline board, where the published setting is switch 1 of S101 on and the rest
off. Setting 4 on changed nothing and was reverted. Read the board first, then
the line.

**S102 exists and no source describes it.** The wireline board carries a second
four-way bank beside S101. Neither the published builds, the W9CR wiki nor
Repeater-Builder mention it; all three point at the Motorola manuals
(**68P81088E90-E**, **6881095E05-D**) for switch-level documentation, which is
where to look rather than guessing.

**And a port gotcha:** the published adapter table refers to the *top* V.24
port, but it is the *bottom* port with a real TTN4010 daughter board.

### The adapter pinout, from the published table

RJ-45 at the daughtercard, DB-25 male at the Cisco DCE cable:

| RJ-45 | Signal | DB-25 |
|---|---|---|
| 1 | RCLK, receive clock | 17 |
| 2 | CD, carrier detect | 8 |
| 3 | TCLK, transmit clock | 15 |
| 4 | GND | 1 |
| 5 | RxD | 3 |
| 6 | TxD | 2 |
| 7 | CTS | 5 |
| 8 | RTS | 4 |

Plus the pin 6 to pin 20 jumper inside the hood, above. **Note the table grounds
on DB-25 pin 1**, which is protective ground, where RS-232 references signals to
pin 7; many Cisco cables tie the two, and if a hand-built hood does not, nothing
crossing the RJ-45 has a return path. Worth a continuity check between 1 and 7
on any adapter built from this table.

### Codeplug, from the ASTRO tab

`Wireline Interface: V.24 ONLY`, `Analog Idle Link Check: DISABLED`, `Digital
Idle Link Check: ENABLED`, `External Transmit Clock: ENABLED`, `RT/RT
Configuration: ENABLED`. Modem input level 0 to -28 dBm, output -14 dBm.

Recorded from ZL4JY's screenshots of the back-to-back case. **Whether RT/RT is
right when the far end is QSP rather than a second Quantar is not yet known**
— the DVSwitch group's long "Quantar setup" thread is the likely source and
groups.io returns 402 to anything past its topic list.

### The Cisco side

STUN is the choice of three candidates — L2TPv3 and circuit emulation over IP
being the others — because it is well supported in old IOS, needs no special
hardware and is simple. **It requires the Enterprise feature set**, because
that is where the old IBM SNA support lives, and SDLC is what HDLC was
developed from. Minimum 16 MB flash and 64 MB RAM for a 12.2 image.

Working configuration, from the documented build with the far end pointed at
QSP instead of a second router:

```
hostname P25R1
stun peer-name 192.168.1.50
stun protocol-group 1 basic
!
interface FastEthernet0/0
 description LAN
 ip address 192.168.1.50 255.255.255.0
!
interface Serial0/0
 mtu 2104
 no ip address
 encapsulation stun
 clockrate 9600
 stun group 1
 stun route all tcp <the QSP server>
```

`show stun` reports the circuit state, and `copy run start` saves it.

### The router: settled on the operator's own 2921, 2026-09-13

**A CISCO2921/K9 on 15.4(3)M3 `universalk9` runs STUN.** Verified at the
console rather than inferred: `stun peer-name 192.0.2.1` was accepted in
configuration mode, which on IOS means it exists.

The licence was the blocker and it is **self-activating**:

```
data   None    None            None       <- before
data   datak9  EvalRightToUse  datak9     <- after
```

`show license feature` had listed `datak9` with `Enforcement yes`,
`Evaluation yes`, `Enabled no`, **`RightToUse yes`** — the feature is present
in the universal image and the operator can enable it. One command, an EULA
prompt, `write memory` and a reload:

```
license boot module c2900 technology-package datak9
```

**Sixty days, from 2026-09-13.** `EvalRightToUse` is a timer, not a grant: the
EULA states payment is due to Cisco beyond the evaluation period, and that it
is the user's responsibility to know when the period ends. That date is around
**12 November 2026**. If the Quantar link becomes permanent, a surplus 1841
with 12.4 `adventerprisek9` has the feature in the image with no licence and no
clock, and costs about what a serial card does — moving the configuration
across is a ten-minute job.

**A correction, recorded rather than quietly fixed.** This document previously
said the data licence "gates STUN on ISR G2" and offered a surplus 1841 as the
fallback, citing a 2901 that rejected every `stun` command. The citation was
true and the conclusion was wrong: the feature is in the image, and the
difference between "buy a different router" and "type one command" is exactly
the sort of thing this project is supposed to check before asserting.

**What is still true about the chassis.** Legacy WIC-1T and WIC-2T are *not*
supported in ISR G2 EHWIC slots — a c2921 boots with
`%MAINBOARD-1-UNKNOWN_WIC ... unknown id 0x2`. So it is an HWIC card with a
**CAB-SS-232FC**, not the WIC-1T and CAB-232FC of the published builds. The
operator's is an **HWIC-2A/S**, which carries STUN: see below.

**And the slots are free.** `show inventory` on the operator's unit lists
`VWIC3-2MFT-T1/E1` in 0/0 and `EHWIC-4ESG` in 0/1, leaving 0/2 and 0/3 empty.
The T1/E1 card cannot do this job — RJ-48 trunks, not V.24 synchronous RS-232
— but the four-port gigabit switch is useful: it gives the router its own LAN
segment for the tunnel, which is what the published builds do with a second
Ethernet interface.

**Set the clock.** That unit logged `*Jan 2 00:00:03` until a console
configuration corrected it. A router capture and a QSP log that cannot be lined
up by timestamp cost real time, and the whole diagnostic method here is two
readings of one event. Point it at NTP before the first capture.

### The router, configured: 2026-09-21

**The HWIC-2A/S carries STUN.** It is an asynchronous-or-synchronous card, where
the published builds used a synchronous-only one, so this was asked of IOS
rather than assumed: `encapsulation stun` was accepted on `Serial0/3/0`, and IOS
set `mtu 2104` itself, the MTU the published builds use. The port is
synchronous by default.

**`stun peer-name` is the router's own address, not the far end's.** This
document's working configuration above shows that, with the peer name equal to
the Ethernet address, but it does not say it, and the first configuration today
set it to the QSP server's address by mistake. The far end is `stun route`.
Removing the peer name also removes the protocol group, so the group has to be
entered again after changing it.

What is saved on the operator's router:

```
ip name-server <the LAN's DNS server>
stun peer-name 192.168.1.45
stun protocol-group 1 basic
!
interface GigabitEthernet0/0
 ip address dhcp                 ! reserved as 192.168.1.45 on the LAN
!
interface Serial0/3/0
 mtu 2104
 no ip address
 encapsulation stun
 stun group 1
 clock rate 9600
!
clock timezone CST -6 0
clock summer-time CDT recurring
ntp server pool.ntp.org
```

**The clock rate was `125000` until 2026-09-26, and it was wrong.** It was set
while proving `encapsulation stun` parsed, with no radio attached and nothing
depending on the rate. A Quantar's wireline card expects 9600, which this
document said twice in the clocking section above while the saved configuration
said otherwise — two statements individually defensible and together a lie,
§8a's pattern exactly. At 125 kbps the link would never have synchronised and
nothing in any log would have said why; the afternoon would have gone on the
cable, the jumper and the daughtercard switches.

**`BW` is not the instrument for the clock rate.** `show interfaces` reports
`BW 128 Kbit/sec` on this card regardless, because bandwidth is a static routing
default rather than a live read of the clock generator. It did not change when
the rate did. The hardware is the instrument, and it is the same command that
found the 125000 in the first place:

```
show controllers Serial0/3/0 | include clock
```

**Deliberately absent: `stun route all tcp …`**, the line naming the far end.
It points at the test server for the first capture, never production, and it
goes in when the Quantar is demonstrably sending — not merely cabled. See the
counter differential in step 3 below: a tunnel pointed at a silent wire produces
an empty capture and an afternoon of wondering whether the rig or the radio is
at fault.

**The clock is synchronised, stratum 3**, and reads CDT: the configuration had a
timezone and no summer-time rule, so local time was an hour behind until it was
added. Its log timestamps are UTC, which is what lines up with QSP's.

**The enable and `admin` secrets are scrypt, type 9**, replacing MD5 hashes.

**Reaching it from Fedora needs a hop, and there are two walls rather than one.**
This paragraph named only the host key until 2026-09-26, when both were measured
in order:

1. **Key exchange fails first.** A bare `ssh admin@192.168.1.45` from Fedora
   never reaches the host key: every method the router offers is SHA-1
   (`diffie-hellman-group-exchange-sha1`, `group14-sha1`, `group1-sha1`) and
   Fedora's policy permits none. `-o KexAlgorithms=+diffie-hellman-group14-sha1`
   clears this one, which is how the second wall became visible at all.
2. **The `ssh-rsa` host key fails next, and `-o HostKeyAlgorithms=+ssh-rsa`
   does not clear it.** The option is accepted and the connection still fails
   with *no matching host key type found. Their offer: ssh-rsa*, because the
   crypto policy bans SHA-1 signatures below ssh's configuration layer: ssh
   prunes `ssh-rsa` from the usable set, and `+ssh-rsa` appends something that is
   then filtered out. The failure looks like a configuration error and is a
   policy one.

From the test server, which runs Ubuntu, this works and is the documented route:

```sh
ssh -o KexAlgorithms=+diffie-hellman-group14-sha1 -o HostKeyAlgorithms=+ssh-rsa \
    -o MACs=+hmac-sha1 admin@192.168.1.45
```

Re-enabling SHA-1 on Fedora system-wide (`update-crypto-policies --set
DEFAULT:SHA1`) would also work, and was not done: it weakens every connection
the development machine makes, to reach one old router. A per-process
`OPENSSL_CONF` override is the third option and was **not** tested; it is
recorded as untested rather than as a limitation, because §7 says a limitation
written down becomes a limitation nobody retests. The hop costs one command and
the console cable is always there.

**Step 1 of the bring-up below is done.** The previous configuration was the
operator's own, so it was kept rather than wiped.

### Bring-up order, each step with a checkpoint

1. Router alone, no radio. Wipe the previous owner's config — break into rommon
   within 60 seconds of power-on, `confreg 0x2142`, `reset`, then
   `config-register 0x2102` and `write erase`. **Checkpoint:** `stun peer-name`
   parses.
2. Build the adapter, cable it, and bring the port up with **no `stun route`**.
   **Checkpoint:** `DTR=up` in `show interfaces Serial0/3/0`. As DCE the router
   drives DCD, DSR and CTS and reads DTR and RTS, so DSR out on pin 6 through
   the jumper and back in on pin 20 as DTR tests the jumper electrically, in
   place, with no multimeter. **This checkpoint proves the hood and nothing
   else** — the loop is entirely inside the DB-25 shell and reads `up` with
   nothing plugged into the RJ-45 end at all. It was written as "continuity, and
   the jumper actually present" until 2026-09-26; the electrical test is
   strictly better and the caveat is the half that matters.
3. **Prove the whole physical path with a loopback, before blaming the radio.**
   This is the single best instrument this project has for a synchronous serial
   link and it needs no test equipment at all — one jumper wire.

   STUN generates no traffic of its own, so borrow an encapsulation that does.
   Jumper **RJ-45 pin 5 to pin 6** (RxD to TxD) on a spare plug and put it where
   the daughtercard's cable goes, so the hood and the Cisco cable stay in the
   path. The DCE generates both clocks itself, so the data loop alone is enough.

   ```
   conf t
   interface Serial0/3/0
    encapsulation hdlc
    keepalive 5
   end
   clear counters Serial0/3/0
   ```

   Twenty seconds later, `show interfaces Serial0/3/0 | include line
   protocol|packets input|input errors|abort`. **`line protocol is up (looped)`
   with packets in and zero errors** proves the router's serial path, the clock
   rate, the cable, the hood and its wiring, end to end, with the radio out of
   the picture. Zero means the fault is in the hood or the cable — move the loop
   to **DB-25 pin 2 to pin 3** to take the hood out and diff the two.

   On 2026-09-26 this returned 552 bytes in 8 frames with zero CRC, frame and
   abort errors, which is what cleared the entire router side in one command.

   Restore afterwards — changing encapsulation drops the STUN lines from the
   interface:

   ```
   conf t
   interface Serial0/3/0
    no keepalive
    encapsulation stun
    stun group 1
    stun route all tcp <far end>
   end
   ```

   **`up/up` under STUN means nothing.** STUN runs no keepalives, so the
   interface reads up with a dead far end, an unplugged cable or no radio at all.
   It read `up/up` through an entire afternoon of a completely silent link. Under
   HDLC with a keepalive the line protocol only comes up if frames genuinely
   return, which is the whole reason this test works.
4. **Is the Quantar sending anything?** `RTS` is the only signal the Quantar
   itself drives, but it is not conclusive: the link comes up on the DTR jumper
   alone, and a Quantar may hold RTS low while idle. The counters settle it,
   because HDLC frames on RXD are counted whether RTS is up or not.
   `clear counters Serial0/3/0`, wait fifteen seconds, then read
   `show interfaces Serial0/3/0 | include packets input|input errors|abort`.
   **Three outcomes, and they mean different things:**
   - **`packets input` climbing** — the Quantar is talking and RTS is a red
     herring. Go to step 4.
   - **Zero in, zero errors** — nothing on RXD at all. The radio end is not
     connected or not configured: the daughtercard, then
     `Digital Idle Link Check`, which is what makes a Quantar put idle frames on
     a quiet link and whose absence makes silence the *correct* behaviour.
   - **Zero packets with input errors or aborts climbing** — something is on the
     wire and the framing or clock is wrong. The most informative of the three,
     and it points at the clocking direction or a DIP switch rather than the
     codeplug.
5. Point the tunnel at a QSP host and capture: `stun route all tcp <far end>`,
   `scripts/stun-capture.py` there, `tcpdump` alongside it.
   **Checkpoint:** bytes on disk.

Steps 1–4 are all instrument, no QSP code. Step 5 is ADR-0060 phase 1.

**`debug stun packet` printing nothing is evidence, not a broken debug.** With
`debug serial interface` logging the port coming up in the same session, STUN
logging nothing at all means it has no frame to forward and is idle by design.
Likewise `show stun` reading `closed` with `0 rx_pkts, 0 tx_pkts`: Cisco's own
troubleshooting guide shows a `closed` circuit alongside 5,729 received packets,
so **the word `closed` carries no information and the counters carry all of it.**

**The old step 3 described a loopback rig that did not hold together.** It called
for looping TXD to RXD at the far end while also claiming "every keepalive the
Quantar sends returns over the IP route", and named the wireline LED as its
checkpoint — but a loopback plug where the Quantar should be means the Quantar
is disconnected and sending nothing, and the LED is on the Quantar. The two
halves described different rigs. The instinct was right and the mechanism was
missing: a loopback **does** settle this, but only with an encapsulation that
generates its own traffic.

**And the wireline LED is a phase 2 instrument, not a phase 1 one.** The
published build says it "flashes when the link is not up and will go steady on
when keep alives are being **received**." Received — so against
`scripts/stun-capture.py`, which deliberately answers nothing, that LED cannot
go steady however perfect the wiring is. A flashing LED during a phase 1 capture
is the expected state and says nothing at all about whether the link works. It
becomes an instrument only once QSP answers, which is exactly what ADR-0060
phase 2 is.

### The router half is finished: 2026-09-26

Steps 1 to 3 are done and the router is not the open question any more. Each row
is a fact with the instrument that produced it, because a fact without one is
what put `clock rate 125000` in this document for five days:

| Fact | Instrument |
|---|---|
| The HWIC-2A/S carries STUN and the encapsulation is up | `show stun`, `Serial0/3/0 is up, line protocol is up` |
| A **DCE RS-232** cable is attached, not the male-DB-25 DTE trap | `show controllers` naming the cable it reads |
| The clock is **9600** at the hardware | `show controllers ... include clock` |
| The hood and the pin 6-to-20 jumper work | `DTR=up` with the port no longer shut |
| **Nothing is arriving from the Quantar** | zero packets **and zero errors** after `clear counters` |

Zero input with zero errors is a silent wire, not a mis-framed one — something
on the wire with the wrong framing shows CRC errors or aborts.

### And the fault is a missing level converter: 2026-09-26, later

The same afternoon closed it out. Everything else was eliminated with an
instrument rather than an argument:

| Eliminated | By what |
|---|---|
| Router configuration | compared line for line against the published build; identical |
| Serial path, cable, hood, 9600 clock | the HDLC loopback in step 3: `up (looped)`, 552 bytes, **zero errors** |
| Codeplug | all five ASTRO settings read back from the station |
| DIP switches | S101 switch 1 on, rest off — the published setting for a Motorola board |

**`RT/RT Configuration` was found DISABLED and was corrected** during this pass.
It changed nothing on its own, but it was genuinely wrong and would have bitten
later. It is the one setting that had never been verified rather than assumed.

**What remains is a V.24 interface on the station that has never been alive.**

**Corrected 2026-09-27.** This section previously said the station had *no*
TTN4010 fitted, reasoned from a missing object in a photograph plus W9CR's
description of the TTN4010 as "a level converter board from the TTL levels
present on the wireline". Right location, wrong mechanism, and the evidence was
thin: a thing absent from a picture is weak grounds for a hardware claim. What
settled it was **the AUX LED, which should be lit and never has been** — the
board's own report that its V.24 section is not running. A replacement card is
on the way. Keep the reasoning visible rather than quietly editing the
conclusion: the measurements were sound throughout and the interpretation of
them was not.

**Check the AUX LED first, before anything else.** It is the cheapest
observation available on this whole path and it is the one that would have saved
two days. On 2026-09-26 and 27 the router side was proven three separate ways
while the radio's own indicator sat dark the entire time and nobody looked.

For the record, the assembly examined on 2026-09-26: the wireline board is a
`TRN7477D11` — the older 4-wire board of the CLN6955 family — carrying an
MC68302, two 1995 firmware EPROMs and a Peripheral TDM device, sandwiched with
the line-interface half that holds the ETAL transformers, the `J500` LINE 1–4
header, both RJ-45 jacks, S101, S102 and the DS100/DS101 red and green LEDs. Per
the published daughtercard documentation, **both front-panel RJ-45 jacks belong
to the TTN4010 and the bottom one is the V.24 port** — so two jacks implies the
card is present, which is what made the "not fitted" reading wrong.

**When the replacement arrives, what to re-check depends on which card it is.**
A daughtercard alone: the bottom V.24 jack, S101 at switch 1 on and the rest
off, and the AUX LED. A whole wireline board: all of that plus the codeplug's
`Options → Wireline` hardware declaration, both switch banks, and the analog
line levels on the Alignment screen.

**A caution for whoever fits the card.** RS-232 swings to ±12 V and TTL inputs
expect 0–5 V, and the Cisco was connected to that jack for roughly two hours on
2026-09-26. The board may well have protection and was still driving its LEDs
afterwards, but confirm the wireline board behaves before blaming a new TTN4010.

**And note the port gotcha above**: with a real TTN4010 fitted it is the
**bottom** V.24 port, not the top one the published adapter table names. Both
jacks were tried on 2026-09-26 with the original adapter and the bottom jack
again on 2026-09-27 with a replacement adapter, and neither produced anything —
expected from an interface that was never running.

### Ask the station before measuring it: the Service screens

**`Station Note: Station is Currently ACCESS DISABLED`** was found on
2026-09-27, printed at the foot of every RSS Alignment screen. An
access-disabled Quantar is out of service. Six rounds of counter tests were run
at the router before anyone opened a screen where the station says what it
thinks of itself.

The RSS tree under **Service** holds `Version Screen`, `Alignment Screen`,
`Metering Screen`, `Status Report Screen`, `Test And Measurement Screen` and
`Status Panel Screen`. **Open these first** when the radio appears silent:

- The **station note and status** at the foot of the Alignment screens — access
  state and keyed state, in the station's own words.
- **Status Report** and **Status Panel** for alarms and link state.
- **Version Screen** for the firmware in those 1995 EPROMs, since V.24 support
  is firmware-dependent.
- On the Alignment screen, note whether **`TDATA Calibration` and `RX Wireline
  (line 4)`** are greyed out. TDATA is the ASTRO digital path; on 2026-09-27
  both were grey while the analog TX/RX wireline tabs were live, and whether
  that is a consequence of access being disabled is not yet known.

This is §8a's rule in its original form: when several changes in a row produce
nothing visible, that is evidence about the feedback path, not a reason for a
cleverer fix — make the system report its own state.

## What QSP replaces, and what the existing stack gets wrong

The DVSwitch group's own wiki describes Quantar_Bridge as emulating **the far
end of the Cisco STUN connection as well as a connected V.24 device**, parsing
the voice frames out and passing them to MMDVM_Bridge. That is the clearest
statement of what QSP has to be, and it is two roles rather than one:

- a STUN endpoint that accepts the router's session, and
- **a V.24 device that behaves like the far end** — answering keepalives and
  holding the right control state, which is what makes the wireline LED go
  steady. Not merely a parser.

The chain QSP collapses runs 34103/34100 between Quantar_Bridge and
MMDVM_Bridge, then 42020/32010 on to P25Gateway, then P25Gateway to the
reflector. Four processes and six ports become one listener.

**A defect that has outlived three projects.** A group thread titled
"Transmitted HDU always starts the call with TG=10200 even when TG != 10200"
concerns the network-to-RF direction: raised in 2018 and still reproducing in
December 2020 against Quantar_Bridge 1.6.0, MMDVM_Bridge 1.6.2 and both
P25Gateway builds, by using any talkgroup other than 10200.

**That is a requirement for QSP**: a header sent towards RF carries the
talkgroup the call is actually on. And it is the four-layer problem exactly as
ADR-0060 describes it — a defect that survived two years across three projects
because nobody owned the whole path.

### The implementation is not available, and that is the ideal position

`DVSwitch/Quantar_Bridge` on GitHub is a **filesystem mirror, not source**: a
README, a logrotate config, a systemd unit, three compiled binaries and an
`.ini`. Checked 2026-09-12 by listing the repository rather than reading it.

So ADR-0029 has nothing to forbid here. The `.ini` is an interface description
in the same category as a port number, and it is where `quantarPort = 1994`
comes from. The framing itself has no public source and no published standard,
which means QSP learns it the way it learned IPSC: from captures taken here.

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
   **Everything on the router side was proven across 2026-09-26 and 27** —
   configuration, cable, two separate adapters, 9600 clock and the codeplug,
   each with its own instrument, and the router is saved in that state with the
   port shut. What is outstanding is the station's V.24 interface, whose AUX LED
   has never lit; a replacement card is inbound.
6. **Phase 1 and 2** — capture the keepalive, then answer it. The wireline
   card's LED going steady is the milestone.
7. **Phase 3 and 4** — voice, then relay.
8. **Then the GTR 8000**, which by then is a cable and a codeplug.

The scaffold is not in this list. It is a fallback if phase 3 needs a
known-good far end, and a way to keep a repeater on the air meanwhile.

No longer deferred. ADR-0034 deferred this until the DMR and IPSC work was
finished, and the routing core was assessed as essentially complete on
2026-09-11.
