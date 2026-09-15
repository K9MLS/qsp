# Zello

**Researched 2026-09-13**, the day before a DVMEGA DVstick 30 arrives, so that
the hardware meets a plan rather than a question.

Nothing here is built. `AMBE_AUDIO` appears in this repository only in prose —
README, BLUEPRINT and PROJECT_MEMORY — and in no Go file.

## Recovery, first, because it was needed

**If the dongle stops answering, unplug it physically for ten seconds.**

On 2026-09-14 a malformed control packet — field `0x0a` sent with one argument
byte where it takes twelve — left the chip mid-field permanently. AMBEserver
reported `Couldn't find start byte in serial data` on every start afterwards. A
software reset could not clear it, and **neither could detaching and
re-attaching the USB device in ESXi**: the device node came back new and the
chip was still lost. Only removing power did it.

**The field lengths that make a packet malformed are now held in one place**:
`internal/ambe`, from the AMBE-3000F users manual version 3.7 (October 2016,
`md5 f20fd488960efdfbf2ba51165c6c7718`), with the manufacturer's four worked
example packets as a fixture in `testdata/ambe/manual-examples.hex`. Nothing in
this project builds an AMBE packet by hand. A field whose declared length
disagrees with the manual is refused before it reaches a socket, which is the
only form of that check that survives a change to the call site.

**Read the F manual and not the R.** The board answers `PKT_PRODID` with
`AMBE3000F`. The two manuals differ where this work touches: the RESET pin is an
I/O on the F, and the echo canceller and echo suppressor are documented as
unsupported in packet mode there. The manual cannot be fetched into a container
— every attempt truncates well before §6.6 — so it has to be downloaded and
attached.

This is written first because it was learned last, after the experiment rather
than before it. A device that can be wedged by a malformed packet needs its
recovery path known in advance.

## The chain

**Superseded by [ADR-0062](adr/ADR-0062-as-much-as-possible-in-qsp.md)** — the
four-process chain below was refused, and QSP speaks to AMBEserver directly.
Kept because it is what the published builds do and is still the thing to
compare against.

```
QSP (DMR, AMBE frames)
   |  AMBE_AUDIO - TLV frames over UDP        <- the only piece QSP builds
Analog_Bridge         ambeMode = DMR
   |  serial, or an AMBEserver socket
DVstick 30            AMBE+2 <-> PCM
   |  USRP - 8 kHz 16-bit PCM plus PTT, over UDP
asl-zello-bridge
   |  WebSocket, JSON commands, Opus audio
Zello channel
```

**Four processes is the right answer here, and it is not the Quantar case.**
[ADR-0060](adr/ADR-0060-qsp-terminates-the-serial-tunnel.md) collapsed a chain
because one of its links spoke a protocol QSP already spoke. Here the links are
a vocoder and a codec QSP must never contain
([ADR-0034](adr/ADR-0034-p25-is-native.md)), so collapsing them would mean
shipping a vocoder. QSP speaks to the first process and stops.

## What each piece does

**Analog_Bridge** has exactly two sides. Everything arriving on the USRP port is
encoded for transmission on the AMBE_AUDIO port in the format named by
`ambeMode`, and the reverse. `ambeMode` accepts DMR, DMR_IPSC, DSTAR, NXDN,
P25, YSFN and YSFW — so the mode is configuration, not code.

**USRP** is borrowed from AllStarLink's `chan_usrp` driver and carries
uncompressed 8 kHz 16-bit audio plus push-to-talk signalling over UDP. It is
what makes the analogue side interchangeable: AllStar, EchoLink, or another
Analog_Bridge.

**asl-zello-bridge** connects Zello Free or Zello Work channels to USRP. It runs
on 1 vCPU and 1 GB, on AMD64 or ARM, and needs a Zello account to log in with.

**The Zello Channels API** is a WebSocket carrying a JSON protocol with Opus
audio. A message is a stream: `start_stream`, then binary packets carrying the
returned `stream_id`, then `stop_stream`. It needs an account and API keys from
Zello's developer portal, and **it is documented as beta and subject to
change** — a dependency risk to record rather than ignore.

## The dongle, in detail

Researched 2026-09-13, the day before it arrived. Everything here is from
working installations rather than from the datasheet, because the failures are
all in the gap between the two.

### What it presents to Linux

An **FTDI FT230X**, so the driver is `ftdi_sio` and the device appears as
`/dev/ttyUSB0`. A working AMBEServer reports it as:

```
/dev/serial/by-id/usb-FTDI_FT230X_Basic_UART_DO011ZUX-if00-port0 at 460800 baud
```

**Use the `by-id` path, not `ttyUSB0`.** It carries the device's own serial
number and does not renumber. On a VMware guest, where ESXi may attach the
device after the guest has booted and again after every reboot, `ttyUSB0` is a
guess and `by-id` is a fact.

### Baud is 460800, and the default is the other one

DVMEGA's own product page says to set 460800. **AMBEServer defaults to 230400**
— its `-s` flag — so an unconfigured start talks to the stick at the wrong
rate. Early ThumbDVs really are 230400, which is why the default is what it is
and why half the guides on the internet say the other number.

### Stock AMBEServer may not drive a DVMEGA board at all

Forks exist for this specific reason: they add support for the DVMEGA AMBE 3000
board and others that are not in the correct mode at boot because of their
hardware configuration, by sending a `RESETSOFTCFG` packet instead of `RESET`
to override the chip's hardware configuration. `marrold/AMBEServer` and
`FRS077/AMBEserver` both describe the same fix.

**So if stock AMBEServer opens the port and then does nothing useful, that is
the reason** — not the cabling, not ESXi, and not a config value. The fix is a
different build.

### And the chip has to be reset after it reboots

A known issue: **if the AMBE chip reboots, AMBEServer must be restarted.** On
ESXi that happens every time the device is detached and re-attached, and on
every guest reboot. Without handling it the service is up and deaf — running,
logging nothing wrong, and passing no audio. Exactly the failure this project
distrusts most.

### Which chip, read rather than assumed

The device announces itself on the first exchange:

```
AMBE device response -> Type: 0x0, Length: 11, Data: 0AMBE3000R
```

**AMBE3000R and AMBE3000F are different**, and DVSwitch publishes separate
images for each. Read that line. The second response carries the full firmware
string, worth recording in the handover once it exists — it is the kind of
detail that explains behaviour six months later.

### Test tools, and the order to use them

`AMBEtest3.py` exercises the dongle directly and `ambesocktest.py` exercises
AMBEServer over its socket, both from NW Digital Radio's install repository.
**Stop AMBEServer before the direct test**: only one process can hold the port.

That gives a bring-up ladder with one variable per rung:

| | Step | Instrument |
|---|---|---|
| 1 | Device present | `lsusb`, `ls /dev/serial/by-id/` |
| 2 | Chip answers | `AMBEtest3.py`, and the `AMBE3000x` line |
| 3 | Server answers | AMBEServer at 460800, then `ambesocktest.py` |
| 4 | Transcoder connects | Analog_Bridge `[DV3000]` at 127.0.0.1:2460 |
| 5 | Zello | the USRP side, and the Channels API |

Baseline taken before the hardware arrived: `lsusb` on 192.168.1.247 shows only
a VMware virtual hub and mouse, and there is no `/dev/ttyUSB*` or
`/dev/ttyACM*` on either server. Afterwards there should be exactly one new
device.

### Capacity and codec

**AMBE-3000, one channel, one call at a time.** BLUEPRINT §7 is explicit that a
club bridge with four simultaneous transcoded talkgroups needs four vocoder
channels and that this must be a first-class concept rather than an
afterthought. A second simultaneous transcoded call has to be **refused**, not
mangled.

**It does not do P25 full rate.** PROJECT_MEMORY §8i recorded this when the
operator was looking at a DVSI USB-3003-P25: only the P25 variants do full
rate, and this would otherwise "have been discovered months later." So the
DVstick 30 serves DMR, D-Star and YSF, and has no part in the Quantar work.

**Direct serial or over a socket.** Analog_Bridge's `[DV3000]` section takes
either an AMBEserver address on port 2460 or a device such as `/dev/ttyUSB0`
with `serial = true`. AMBEserver shares a dongle over a network socket, so the
stick and the bridge need not be on the same machine. Baud is 230400 or 460800.

**A config key that is silently ignored.** A DVSwitch issue records the shipped
`.ini` using `address` and `baud` where the program wanted `server` and `port`;
it logged `Unknown section/name in .ini file: DV3000/address` and carried on.
**Read the log on first start rather than trusting the file** — a setting that
is accepted and ignored is this project's least favourite kind of defect.

**Software fallback exists.** `decoderFallBack` permits md380-emu for AMBE+2,
which covers DMR but never D-Star's older codec. So for Zello the dongle is the
audio-quality option rather than a hard requirement — useful if it is dead on
arrival.

**ThumbDV and DVstick 30 are the same thing** for these purposes: same AMBE
chip, same serial converter, even the same casing, differing mainly in country
of manufacture. Documentation written for one applies to the other.

## Both QSP servers are virtual machines

Checked 2026-09-13 on 192.168.1.247: `lsusb` shows `0e0f:0002` and `0e0f:0003`,
a VMware virtual USB hub and mouse, and nothing else. The test server is on
`ens192`, likewise VMware.

**So the stick cannot simply be plugged into a QSP server.** It goes into the
ESXi host, which passes it through to the guest — a layer above the Docker
USB passthrough BLUEPRINT already names as a silent-failure source. Two
passthroughs to declare, each of which fails by the device quietly not being
there.

Baseline before the hardware arrived: no `/dev/ttyUSB*` and no `/dev/ttyACM*`
on either server. Afterwards there should be exactly one new device.

## Talker Alias: the PDUs are built, and nothing carries them

`TalkerAliasPDUs` in `internal/dmrfec` builds the Link Control PDUs for an alias:
one header and up to three blocks, nine bytes each. From **ETSI TS 102 361-2
V2.3.1 (2016-02)**, `md5 68543536068f01fe965b827e2f498ae3` — §5.4.3 is the
service, tables 7.4 and 7.5 are the layouts, tables 7.25 and 7.26 are the
format and length elements, table 5.4 lists the four FLCOs (0x04 to 0x07).

**Nothing carries them yet.** The embedded Link Control that spreads a 9-byte
LC across the four middle bursts of a voice superframe is a BPTC(16,7) with a
five-bit checksum, and that is specified in **TS 102 361-1**, which is not in
the tree. `EncodeBPTC` in that package is the 196-bit data-burst code — a
different thing with a similar name.

**Two places the standard contradicts itself, both refused rather than
guessed:**

- **UTF-16BE.** §5.4.3's character boundaries for it run 3, 6, 10, 13 —
  increments of 3, 4 and 3, where a 56-bit block holds three and a half 16-bit
  characters. The 7-bit and 8-bit formats increment evenly and reconstruct
  exactly. A callsign needs none of it.
- **Non-ASCII in an 8-bit format.** §7.2.19's prose calls the length element
  "the length in bytes" and its own table 7.26 calls it "Length in
  characters". They agree for ASCII and differ for anything else, and a radio
  told the wrong number displays a truncated alias.

**And it has never been compared against a radio.** Everything here is a
careful reading of tables. A capture of a MOTOTRBO with Inband Caller Alias
enabled is what would turn it into a recording, and that is the test worth
running before anything transmits one.

## Identity: settled in ADR-0064

**A Zello user transmits under the gateway's own DMR ID and identifies by
voice.** Radios embed the source ID in every burst, a Zello user has no radio,
so QSP supplies one — and it supplies a real one rather than minting per-user
IDs that would collide with a live radio on a linked network.

- **Admission is the Zello channel's job.** Added to a moderated channel means
  trusted means permitted. QSP keeps no second allow-list; the channel's
  moderators are the operator's own admins, and two gates deciding one
  question is the second code path that rots.
- **The gateway needs a DMR ID of its own** — not a repeater's and not a
  hotspot's, or transcoded traffic is indistinguishable from that machine's in
  Last heard and on every linked server.
- **The alias is administrator-set and never derived from the Zello
  username**, because Zello display names are chosen by the user and an alias
  from one lets somebody appear as a licensed operator's callsign.
- **Identification is the operator's obligation, by voice**, exactly as on an
  EchoLink-equipped repeater. The alias is display data: §97.119 does not count
  embedded signalling inside a phone emission, a radio may have it switched
  off, and a network may strip it.

**Where the responsibility sits, said plainly**: the licensing judgement rests
with whoever moderates the channel. Zello has no field for a callsign or a
licence — trusted there means a moderator approved somebody. EchoLink differs,
because it will not issue an account without callsign validation, so "trusted
on EchoLink" carries an implicit "licensed" that Zello does not. Nothing in
QSP can tell a licensed Zello user from an unlicensed one, and it does not
pretend to.

## The part that is a licensing question, not a wiring one

BrandMeister's Zello integration is the reference implementation, and most of
its rules are about who is allowed to talk:

- **One talkgroup to one Zello channel.** Each must be associated explicitly.
- **Moderated channels only — SELECT or SELECT+ — because of radio
  regulations.** Channel passwords are not supported.
- A username of the form `callsign-DMRID` maps a Zello caller to a DMR ID.
  Where that fails, the talkgroup ID is used and the Zello username is carried
  as a talker alias.
- A dedicated Zello account per bridge, and at least one set of API keys per
  system.

**A Zello user is not necessarily a licensed operator, and their audio ends up
on RF.** That is the whole reason for moderated channels, and it is why
BLUEPRINT §7's per-repeater "accept transcoded audio" refusal — off by default,
enforced by the routing engine — is the right existing mechanism rather than a
new one. A repeater owner opts in, in writing, rather than discovering
transcoded audio on their machine.

## What QSP has to build

**Items 1 and 2 are built and proved on hardware** (0355 to 0362, §8q and
§8r). `internal/ambe` holds the AMBE-3000F field table, the rate table, a
builder that refuses a malformed packet, and a client that brings the chip up,
holds one call at a time and carries frames both ways. Real DMR audio has been
decoded to speech and listened to. What follows is the original list; items 3
and 4 are the remaining QSP-side work and neither needs a Zello account.


1. **An AMBE_AUDIO link.** TLV frames over UDP to Analog_Bridge, with the
   talkgroup and source identity a transcoded call carries.
2. **Capacity, as a first-class concept.** One dongle is one channel; a second
   simultaneous call is refused with a reason an operator can read, not
   interleaved. The P25 contention work in `docs/P25-NETWORK.md` §7 is the same
   shape of problem and should probably share a vocabulary.
3. **Talkgroup to channel mapping**, and the per-repeater refusal above.
4. **Identity**, both ways: a Zello username reaching Last heard as something
   meaningful, and a DMR talker's callsign reaching Zello.

## What is not known yet, and the capture that settles it

**The AMBE_AUDIO frame on the wire.** The port numbers and the direction of
each are documented; the bytes are not. It is a capture job, and the capture is
available the moment Analog_Bridge runs: a LAN, UDP, and a known-good reference
beside it. The same method as IPSC and P25.

**How a transcoder announces its capacity**, if it does at all. It may simply
have to be configured, which is an honest answer as long as the number is the
operator's rather than a guess.
