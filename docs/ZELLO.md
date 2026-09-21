# Zello

**Researched 2026-09-13**, the day before a DVMEGA DVstick 30 arrives, so that
the hardware meets a plan rather than a question.

Nothing here is built. `AMBE_AUDIO` appears in this repository only in prose —
README, BLUEPRINT and PROJECT_MEMORY — and in no Go file.


## What you need to buy

**An AMBE vocoder dongle.** Zello carries ordinary audio and DMR carries
AMBE+2 digital voice, and the conversion between them is done in hardware
QSP talks to through AMBEserver. **QSP is tested with the DVstick 30**
(AMBE-3000F). Other AMBE-3000 USB dongles that AMBEserver supports should work
and have not been tested. One dongle carries one conversation at a time, in one
direction at a time. Everything else — the Zello account, the API key, the
connector — is free.

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

## Running the dongle as a service

**AMBEserver has to run all the time, and started by hand it does not.** A
foreground AMBEserver dies with its terminal. Install it as a service, and the
latency rule with it:

```sh
sudo cp deploy/systemd/ambeserver.service /etc/systemd/system/
sudo systemctl edit ambeserver      # set AMBE_DEVICE to the dongle's /dev/serial/by-id path
sudo cp deploy/udev/99-ambe-dongle-latency.rules /etc/udev/rules.d/
sudo cp deploy/udev/99-ambe-dongle-restart.rules /etc/udev/rules.d/
sudo cp deploy/systemd/ambeserver-replug.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo udevadm control --reload-rules
echo 1 | sudo tee /sys/bus/usb-serial/devices/ttyUSB0/latency_timer   # now, without replugging
sudo systemctl enable --now ambeserver
```

- **The unit admits only this machine.** AMBEserver binds every interface with
  no authentication, and a malformed packet can wedge the chip until it is
  unplugged; `IPAddressAllow=localhost` keeps everyone else out whatever it
  binds.
- **The latency rule is not optional.** An FTDI adapter holds a short reply
  for its 16 ms default before the host sees it. Measured on production: a
  decode took 26.8 ms against the 20 ms real time allows, so audio going to
  Zello was choppy and stretched; at 1 ms it takes 11.9 ms.
- **The Zello page shows the service and the adapter**, flags a latency timer
  above 1 ms, and has Start, Restart and Stop buttons, with a pause after each
  press so repeated clicks cannot trip systemd's start limit. If the limit has
  tripped anyway — five starts in five minutes — the panel says so and offers
  **Reset and start**. They need
  `deploy/polkit/50-qsp-ambeserver.rules` in `/etc/polkit-1/rules.d/`, which
  lets the `qsp` user do exactly those three things to `ambeserver.service` and
  nothing else; without it they report "not authorized". Every use is audited.
- **The restart rule is what makes a replug survivable.** AMBEserver does not
  notice its dongle leaving: it keeps running, keeps 2460 bound, and answers
  nothing, while `systemctl` still calls it active. Moving the dongle to another
  port is enough, because the device comes back as a different `ttyUSB`. The
  rule asks `ambeserver-replug.service` to restart AMBEserver whenever an FTDI
  adapter appears, and it is not enabled on purpose: udev starts it, and a
  machine where AMBEserver is disabled stays untouched. **QSP needs nothing
  after that** — it reopens a vocoder that stopped answering, within seconds.
- **AMBEserver restarting is survivable.** QSP sets the DMR rate at the start of
  every call and reopens a vocoder that stops answering, so a restart costs at
  most the call in progress. Before 0.1.243 it silently garbled every call
  until QSP was restarted too.

## When Zello goes quiet both ways

**Symptom, from production on 2026-09-19.** Zello transmissions arrive and are
logged, radios key up and are relayed, and nothing is heard in either
direction. `systemctl is-active qsp qsp-zello ambeserver` says active three
times. The connector reports `state: connected`. The only sign is QSP warning
on every call:

```
"transcoder problem" ... "a call arrived and the vocoder is not reachable"
"transcoder problem" ... "audio from USRP arrived and the vocoder is not reachable"
```

**Cause.** AMBEserver is holding a dongle that is no longer there. It was
started while the dongle was on another port or another machine, and it kept
its file descriptor. Check whether it predates the device:

```sh
systemctl show ambeserver -p ExecMainStartTimestamp
ls -l /dev/ttyUSB* /dev/serial/by-id/
ss -lunp | grep 2460      # bytes queued here mean audio arriving and nothing draining
```

A start timestamp older than the device's is the answer.

**Fix.** Restart AMBEserver alone. QSP reopens the vocoder within seconds, so
there is no need to restart QSP or the connector, and doing so costs airtime
for nothing:

```sh
sudo systemctl restart ambeserver
curl -s http://<this machine>:8080/healthz | grep -A3 '"transcoder:zello"'
```

**Health, not the log**, and a replug on production on 2026-09-20 is why: the
vocoder reopened and carried audio again within seconds, and QSP logged nothing
at all about it. `vocoder ready` is printed when a channel opens the chip, not
every time a supervisor reconnects one, so an operator grepping for it after a
recovery finds nothing and concludes the recovery failed. The health check
names the chip and counts the frames it has carried, which is the thing being
asked about. The console's Zello page shows the same.

**Not this.** A chip that answers but garbles audio is the wedged case, and no
restart clears it; unplug the dongle for ten seconds instead. The difference is
whether QSP says the vocoder is unreachable, which is this, or reports it
reachable while audio is wrong, which is that.

From 0.1.258 the restart rule above does this on replug, so this section
applies to installs without it, and to a dongle moved while AMBEserver was
stopped.

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

## Talker Alias

**Configured on the Zello page, and sent from 0.1.264.** Section 2, "Name
radios show", beside the gateway's DMR ID — both answer what a radio sees when
Zello talks, one as a number and one as text. Every transmission QSP builds
from Zello audio carries it; leave it empty and none does, which is the
default. It is the transcoder's `alias` in the configuration file, and the page
is the way to set it: saving there is versioned and validated, and editing the
file by hand is neither. It is the operator's string and only theirs: a
Zello display name is chosen by its user, so an alias taken from one would let
a Zello user appear on a licensed operator's repeater under that operator's
callsign (ADR-0064 §3).

**ASCII, up to 31 characters.** It goes out in the 7-bit format, the only one
that reaches 31; `-check` refuses anything else, because the multi-byte formats
are eight bits per character and the standard states their length element in
bytes in one place and characters in another.

**The Link Control comes first, then the alias, then round again.** A
superframe is 360 ms and each PDU takes one, so a callsign lands about
three-quarters of a second into an over and a longer alias later than that. A
short transmission carries part of the cycle and no alias, which is inherent
rather than a fault: a 142 ms Zello over has room for the Link Control and
nothing more. The repeat is what serves a radio that joined late or lost a
burst to a fade.

**Display data, not station identification.** A receiving radio may not support
it, may have it switched off, and a network may strip it. The operator
identifies by voice.

### How it is built

`TalkerAliasPDUs` in `internal/dmrfec` builds the Link Control PDUs for an alias:
one header and up to three blocks, nine bytes each. From **ETSI TS 102 361-2
V2.3.1 (2016-02)**, `md5 68543536068f01fe965b827e2f498ae3` — §5.4.3 is the
service, tables 7.4 and 7.5 are the layouts, tables 7.25 and 7.26 are the
format and length elements, table 5.4 lists the four FLCOs (0x04 to 0x07).

**The carriage is built too**, from **TS 102 361-1 V1.4.5**,
`md5 b0148d72137287d622fe391e85cadf70` — annex B. `EncodeEmbeddedLC` turns a
nine-octet Link Control into the four 32-bit fragments that ride the middle of
bursts B to E of a superframe, and `EmbeddedLCMiddles` goes straight to the
four values `AssembleBurst` wants, EMB and LCSS included.

An eight-by-sixteen matrix carrying 77 information bits — the 72 of the Link
Control plus a five-bit checksum — under Hamming (16,11,4) row codes and even
column parity, interleaved by columns into four bursts. §B.2.1 and figure B.3
are the matrix, table B.16 the generator, §B.3.11 the checksum, which is the
sum of the nine octets modulo 31 and so never reaches 31.

**Figure B.3 prints its own answer for the interleave**, which is the check
worth having: burst 1 begins LC(71), LC(60), LC(49), LC(39), LC(29), LC(19),
LC(9), PC(15) and ends LC(16), LC(6), PC(12), and the column reading
reproduces every one of those positions. A reading that missed them would be a
reading to throw away.

**No error correction, deliberately.** The Hamming rows and column parity
could locate and fix a single bit, but QSP reads these from its own output and
from captures rather than off the air. A corrector nothing exercises is one
nobody can trust — ADR-0034's reasoning a layer down. What is guaranteed is
that a corrupted fragment never yields a *different* Link Control: either the
original, or a refusal.

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

**Confirmed against a radio on 2026-09-15**, and no longer a reading. A
MOTOTRBO with Inband Caller Alias enabled sent the alias `"K9MLS R7"` in UTF-8
as two PDUs, and `TalkerAliasPDUs` builds both byte for byte — including the
reserved bit that had been written purely on the strength of table 7.4's
footnote. See `testdata/hbp/hbp-talker-alias.md`.

**But QSP can only ever send one, not receive one from Motorola.** Three
captures of the same radio through the XPR8300 over IPSC contained no alias at
all, because an IPSC voice packet carries the Link Control spelled out as
fields rather than the embedded signalling it travelled in — the same reason
IPSC omits the EMB. The repeater terminates the air interface and
re-originates it. A Homebrew peer passes the bursts through, which is why the
alias is in a Pi-Star capture and not a repeater one.

## The codec, and the build tag that keeps it out of QSP

`internal/opus` wraps libopus, and it is **behind a `zello` build tag**. That
is not tidiness: a cgo package in the tree without a tag breaks
`CGO_ENABLED=0 go build ./...`, and with it `gofmt`, `go vet`, `staticcheck`
and `go test ./...` for the whole repository. A server running plain DMR would
stop compiling because of a connector it does not run.

```sh
CGO_ENABLED=1 go build -tags zello ./...   # the companion, needs libopus
go build ./...                             # QSP, and it must not see the tag
```

**Zello's parameters are fixed**: 16 kHz mono, 60 ms frames, codec `opus` and
no PCM option. DMR is 8 kHz and 20 ms, so `internal/audio` resamples and
repacketises three radio frames into one Zello packet — which costs **60 ms of
latency toward Zello**, unavoidable at a fixed packet size and the dominant
delay in the path.

**A 60 ms frame can only be SILK.** RFC 6716 §2 gives CELT 2.5 to 20 ms and
SILK 10 to 60 ms, so the speech model is forced by the packet length rather
than chosen by the application hint — setting the hint to `audio` changes
nothing measurable. It also means a CELT-only encoder cannot serve Zello at
all, whatever its quality, which is sharper than ADR-0062's rejection of the
pure-Go candidates on quality grounds.

**libopus's bitrate is read back rather than assumed.** A control returning
`OPUS_OK` has been accepted, not necessarily applied: libopus clamps to what
the mode can carry, and a silently clamped rate is a stream costing more or
sounding worse than the configuration claims.

## The Channels API, from the specification

`internal/zello` speaks the wire protocol: commands, responses, events and
binary audio packets. Pure Go, no network, no cgo — so all of it is testable
without an account, and the session that carries it over a WebSocket is a
separate thing that is not yet built.

**Source: the Zello Channel API specification v1.0**,
`github.com/zelloptt/zello-channel-api/API.md`. Written from it rather than
from recollection, and **three values would have been wrong from memory**:

- **`packet_id` is filled with zeroes when streaming to the server**, which
  ignores it. A counter is the plausible wrong answer and would have appeared
  to work.
- **`channels` on logon is an array**, not a single name.
- **`frames_per_packet` is 1 or 2 only.**

**Two things the transport must do.** TLS only — the specification supports no
other connection. And **answer the server's pings**: it sends a WebSocket Ping
every 30 seconds and terminates the connection if a Pong takes longer than 30
to arrive. A library that does not answer automatically gives a link that drops
every half minute and looks like a network fault.

**The codec header's sample rate is little-endian** while every other
multi-byte field in the protocol is network byte order. That asymmetry is
Zello's, and getting it backwards produces a header declaring 32 kHz — so the
tests assert the raw bytes `80 3e 01 3c` against the specification's own
published `gD4BPA==` rather than only round-tripping.

**A stream cannot start before its channel is online.** The specification's
`channel is not ready` error says to wait for `on_channel_status` with status
`online`. And an incoming stream declares **its own** codec header, which need
not be QSP's: assuming 16 kHz and receiving 8 plays somebody's audio at half
speed, which sounds like a radio fault rather than a bridge one.

**Errors are classified rather than matched at the call site.** Credentials are
not retryable and most other things are: a client retrying `not authorized`
forever hammers the service with a password that will never work, and one
giving up on `server closed connection` stays down after a blip the
specification explicitly says to reconnect from.

`platform_name` is set to include "Gateway" on purpose — the specification says
Zello's Alarms service tracks the online status of a client whose platform name
contains it, and QSP is a gateway.

## The bridge, which is the part that was missing

`internal/zellobridge` puts the pieces in order, behind the `zello` build tag
because it reaches the cgo codec.

**Toward Zello**: USRP frames of 8 kHz PCM, three at a time, resampled to
16 kHz and repacketised into one 60 ms block, encoded as Opus, sent on a
stream. **Toward the radio**: an Opus packet decoded to 60 ms of 16 kHz,
resampled down, split into three 20 ms frames.

Every piece was proved on its own before this — the rate conversion and
packetisation in `internal/audio`, the codec against libopus, the wire
protocol and session in `internal/zello`. **Nothing put them in order**, and
that is all this package does.

**The things it gets right because they are each an audible defect:**

- **The stream opens on the keyup, not on the first frame.** Zello identifies
  every packet by stream ID, so the identifier must exist before there is
  audio — and opening on the first frame would spend its 20 ms on a round trip.
- **The last partial block is flushed before the stream closes.** A
  transmission is rarely a multiple of three frames, and dropping the
  remainder clips the last word of every single call.
- **The stream closes even when the tail cannot be sent.** One left open holds
  the channel against everybody else until the server times it out.
- **The radio is keyed before the first frame reaches it**, because the far
  side opens a channel on the keyup and audio arriving first has nowhere to be
  played.
- **A new stream ID ends the previous transmission**, detected from the packets
  rather than only from `on_stream_start` — a bridge that missed that event
  would otherwise feed one caller's audio into another's open transmission.
- **A stop naming another stream is ignored**, because acting on it cuts a
  transmission in progress.

**The 60 ms is the cost and it is not a defect.** Two frames are held while
the third arrives; unavoidable at a fixed packet size, and the resampler's
filter adds under two milliseconds beside it.

## The logon token, and where the key lives

`auth_token` on the logon is a JWT signed RS256 with the private key from
Zello's developer portal. Its shape, read from a token the portal issued:

```
header  {"typ":"JWT","alg":"RS256"}
claims  {"iss":"<issuer>","exp":<unix>,"azp":"dev"}
```

The issuer's first segment is itself base64 and decodes to
`ZC:<account>:<key number>` — so the issuer says which key pair, and the
signature proves possession of it.

**QSP mints its own token rather than storing one.** A token from the portal
carries an expiry about a month out, and storing that means a credential that
silently stops working on a date nothing records, at a moment nobody chose —
the failure being a connector that has worked for weeks refusing to log on.
The *private key* is the stored credential; the token is made fresh for each
connection, so a reconnection after an outage is never blocked by an expiry
that passed while the link was down.

**The private key goes in the credential store** — typed into the console,
encrypted at rest, outside the configuration document, never returned by any
console endpoint. It is not a configuration value and must never appear in one,
because `configuration_versions` keeps the full document for every save.

**And it never leaves QSP.** `qsp-zello` asks QSP for a logon over a Unix
socket at every connection, and is handed a freshly signed token, the username
and the password — never the key ([ADR-0066](adr/ADR-0066-a-connector-is-handed-a-logon-never-a-key.md)).

## Setting it up

**Everything is on the console's Zello page** (Administration → Zello), in the
order the work is done, with a checklist read from this server's health report.
Nothing needs editing by hand but the connector's own four-line file, which the
page generates.

1. **Before you start** — none of this can be done from QSP: a Zello account for
   the gateway, an API key pair and its issuer from the developer portal, the
   channel joined *in the Zello app with that account* and set to Zelect, a
   DMR ID for the gateway from RadioID.net, and AMBEserver running as a service
   ("Running the dongle as a service", below).
2. **The Zello account** — the channel and issuer, then the username, password
   and private key, each stored the moment its button is pressed. They are
   never shown again, only whether each is stored. A key that cannot sign is
   refused as it is entered, with the reason.
3. **The vocoder** — the AMBEserver address and the gateway's DMR ID, and the
   level in each direction. DMR audio measured about 13 dB quieter on Zello
   than Zello audio arrives; "Level toward Zello" at +10 to +13 dB evens it.
   Levels are soft-limited, so a boost never clips, and need a QSP restart.
4. **What Zello carries** — the talkgroup and timeslot, and the repeaters whose
   owners have agreed. None is chosen for you; a repeater not listed neither
   hears Zello nor is heard on it. Motorola repeaters are covered by the same
   list, by repeater ID, and key up only for a talkgroup and timeslot their
   channel carries.
5. **The connector** — copy the generated `qsp-zello.json`; build `qsp-zello`;
   install `deploy/systemd/qsp-zello.service`, running as the `qsp` user.

   **On the Docker install**, there is nothing to build: save the file beside
   the compose files and add `deploy/docker/docker-compose.zello.yml`, which
   runs the published `qsp-zello` image beside QSP's container.
   deploy/docker/README.md, "Adding Zello", has the commands. The rest of this
   step is for the systemd install.

   **Build it for the server's C library, not the build machine's.** It is the
   one cgo binary, and one built on a newer distribution will not start on an
   older one. Building in a Debian container gives a binary that runs on
   Ubuntu 24.04 and anything newer; the server then needs only `libopus0`:

   ```sh
   mkdir -p build && podman run --rm --security-opt label=disable \
     -v "$PWD":/src -v "$PWD/build":/out -w /src docker.io/library/golang:1.27-bookworm \
     sh -c 'apt-get update -qq && apt-get install -y -qq libopus-dev pkg-config >/dev/null &&
            CGO_ENABLED=1 go build -buildvcs=false -trimpath -tags zello -o /out/qsp-zello ./cmd/qsp-zello'
   objdump -T build/qsp-zello | grep -o 'GLIBC_[0-9.]*' | sort -Vu | tail -1   # at most the server's
   ```

   `--security-opt label=disable` rather than `:Z`, which relabels every file
   under the mounted directory for SELinux.
6. **Switch Zello on, save, restart QSP.** The checklist goes green one line at
   a time: serving logons, credentials stored, vocoder reachable, audio both
   ways.

**Switching Zello off keeps every value.** A paused bridge may name a paused
transcoder, so the talkgroup and repeaters are there when it is switched back
on.

Its `/healthz` on `health_listen` names the action a failure needs:
`qsp_unreachable` (is QSP running with the socket set?),
`credentials_missing` (the Zello page), `credentials_unusable` (the key or
issuer), `zello_refused` (Zello's side — the account or channel membership;
retried every five minutes, not every few seconds), `zello_unreachable`
(the network).

**`azp` is `dev`, and that is settled by a logon, not by the specification.**
The first real connection, 2026-09-16, logged on to channel "QSP Server 1" with
a token carrying `azp: dev` on its first attempt. It stays configurable in case
Zello changes that, but `dev` is no longer a guess.

**What the far side sends is not what QSP sends.** The same connection showed
the Zello app's packets carrying more than one 60 ms frame; a decoder sized for
QSP's own packets refused every one. The decoder is sized for the longest legal
Opus packet, 120 ms, and `qsp-zello` logs each incoming stream's declared codec
header, so the next surprise of this kind is read from a log line rather than
inferred from an error.

**A PEM header is five dashes each side.** A copy that lost one is unreadable
everywhere while looking entirely normal, so the parser names that specific
cause rather than only reporting an unreadable key. Both `BEGIN PRIVATE KEY`
and `BEGIN RSA PRIVATE KEY` are accepted, because the two look almost
identical and nobody should have to know which their tool produced.

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

**Mapping reaches the chip as of 2026-09-16, both ways.** A bridge naming a
transcoder delivers DMR to it, and `internal/vocoderlink` sends the decoded
audio out as USRP (`usrp_listen`, `usrp_peer`). Audio arriving from USRP is
encoded into a whole DMR transmission under the transcoder's `radio_id` and
routed, so item 4's Zello-to-DMR identity is the gateway ID ADR-0064 chose.
What remains is the `qsp-zello` companion; `HANDOVER.md` keeps the current list.


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
