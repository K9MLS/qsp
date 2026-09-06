# P25: what it would take

**Status:** research only. Nothing here has been captured by this project, and
under ADR-0029 nothing here may be implemented until it has been.

Researched 2026-09-06 after the operator asked whether a P25-only network —
Motorola Quantar to Motorola Quantar, never touching DMR — could be built
without buying a vocoder dongle.

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

This is the direct analogue of the Homebrew work: a listener, registration,
peer records, call tracking, relaying. **No vocoder work of any kind.** It can
be captured today, on hardware the operator already owns, at no cost — there
are P25Gateway logs on the Pi-Star already.

Worth doing on its own merits even if the Quantar half never happens: it makes
QSP a P25 reflector, which is useful to any club with hotspots and no Quantar.

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
is written, not a comment discovered afterwards. The recommendation, for what
it is worth: reading the standard is fine, reading anyone's implementation
stays forbidden, and a claim taken from the standard is still verified against
a capture before code depends on it — exactly as an inference is today.

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

## The architectural fork

A Quantar's interface is local. So either QSP runs at the repeater site and
opens the serial device, or a small bridge sits at each site and speaks IP back
to QSP.

**The second is what a multi-site network needs anyway**, and it keeps QSP's own
job exactly what it already is: a UDP server that relays frames. The first
would put a serial dependency into a binary whose whole selling point is that it
has none.

Not decided. It should be decided from a capture, after Phase 2 below.

## Hardware

The operator has Quantars and a Cisco router, so the shopping list is short or
empty. Recorded for completeness:

| Piece | Notes |
|---|---|
| Quantar with wireline card | CLN695x or newer |
| V.24 access | Motorola TTN4010 daughtercard, or direct off J300 at TTL |
| V.24 to something loggable | Cisco WIC-1T, or a DVM-V24-V2 |
| A P25 handheld | To key with |
| A second Quantar | For the back-to-back configuration the linking work is built around |

## The plan, in order

1. **Capture the reflector protocol on the Pi-Star.** Free, today, no new
   hardware. Produces the fixture that would let a P25 listener be written.
2. **Decide the ADR-0029 question** before anything is implemented.
3. **Capture the V.24 link** with one Quantar and a handheld, through the
   router. This is the one that needs the bench set up.
4. **Then** decide whether QSP speaks HDLC itself or a site bridge does.

Deferred until the DMR and IPSC work is finished, per ADR-0034.
