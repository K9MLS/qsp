# Linking a Motorola P25 repeater

For somebody putting a Quantar or a GTR 8000 on a QSP server. It is the short
way through; [`P25-PLANNING.md`](P25-PLANNING.md) is the long one, with every
wrong turn and the reason behind each step here.

**Two sites have been built from this.** A Quantar in Texas, linked to QSP on
the same LAN, on 2026-10-04, by the person who wrote it; and a second Quantar in
Wisconsin, linked to the same server **across the internet** on 2026-10-22, with
calls carried between the two repeaters both ways. If you are the third, what
you find wrong with it is the most useful thing you can send back.

Every instruction is marked with how far it can be trusted:

| Mark | Means |
|---|---|
| **Proven here** | Done on that Quantar, and seen to work |
| **Published** | From the sources at the foot of the page, and not tried here |
| **Not known** | Nobody this project has read says, and it has not been tried |

## How it goes together

```
P25 radio
    |  RF
Repeater - wireline board - V.24 card - RJ-45 jack
    |  ordinary Ethernet patch lead
RJ-45 to DB-25 adapter, with one jumper inside
    |  Cisco DCE serial cable
Cisco router, serial port set to 9600 and "encapsulation stun"
    |  TCP port 1994, over your network
QSP
```

The repeater's V.24 card speaks a synchronous serial protocol. The router does
nothing clever with it: it puts each frame in a TCP connection to QSP, and QSP
speaks to the repeater as a second repeater would. QSP opens no serial port,
and nothing sits between the router and QSP.

**The voice is copied, never decoded.** What a radio says into one repeater is
what leaves the others, bit for bit.

## 1. What you need

| Piece | What to get | Trust |
|---|---|---|
| Repeater | A Quantar with a wireline board that has ASTRO (V.24) capability. Published builds name the CLN695x boards "or newer" | **Proven here** on a Quantar; the board's own number matters less than what step 2 reports |
| V.24 card | Motorola **TTN4010**, the daughter board on the wireline board. Aftermarket level-converter boards exist and take different switch settings | **Proven here** with a TTN4010D |
| Patch lead | Any straight-through Ethernet lead | **Proven here** |
| Adapter | RJ-45 to **DB-25 male**, the kind sold as a kit with loose pins so you choose the wiring. The published build used Norcomp RJADK25P7080831 | **Proven here** |
| Serial cable | Cisco **DCE** cable ending in a **female DB-25**: `CAB-SS-232FC` for a Smart Serial card, `CAB-232FC` for the older 60-pin cards. The DTE cable has a male DB-25, looks right and is wrong | **Proven here** with CAB-SS-232FC |
| Router and card | See below | |
| Console cable | The light blue Cisco one, and a USB serial adapter | **Proven here** |
| Dummy load | For anything done on a bench | |

### Which router

The router needs one feature, serial tunnelling (**STUN**), and a synchronous
serial port.

**Recommended for a new site: an older router that has STUN in its image with
no licence.** That is what every published build uses: a Cisco 2600XM, 3725,
1841 or 2811 with an Enterprise or Advanced Enterprise IOS image, a `WIC-1T`
serial card and a `CAB-232FC` cable. They are cheap second-hand, and there is
no licence to run out. **Published**; none has been run against QSP.

**What was used here: a Cisco 2921 with an `HWIC-2A/S` card.** **Proven here**,
and not recommended. On these newer routers STUN sits behind the `datak9`
licence, which the router will switch on by itself for an evaluation period and
which Cisco expects to be paid for after it. The older cards also do not fit
it. If a 2921, 2911 or 2901 is what you already have and are licensed for, it
works.

**What the second site used: a Cisco 1841 with an `HWIC-2A/S` card and the
Advanced Enterprise image `c1841-adventerprisek9-mz.151-4.M8`.** **Proven
here**, across the internet. The card is the same family as the 2921's, so the
cable is the same `CAB-SS-232FC`, and the port is `Serial0/0/0`.

Whichever it is, check before buying a card that the router accepts the word:
in configuration mode, `stun peer-name 192.0.2.4` is either taken or refused.
**The feature set is in the image's file name, and most of them lack it.**
Refused here, on routers that were otherwise fine: Advanced IP Services
(`advipservicesk9`) and SP Services (`spservicesk9`) on a 2811, and Advanced
Security (`advsecurityk9`) on an 1841. Accepted: Advanced Enterprise
(`adventerprisek9`).

**These routers read only some CompactFlash cards.** One that ROMMON answers
with `Open Error = -66` on `dir flash:` cannot be read at all, whatever is on
it; a card formatted on a PC as FAT32 or exFAT is the usual reason. Put the
image on a card the router has already booted from.

## 2. Ask the repeater first

Before any cable. Two days were lost here by proving a router three ways while
the station was saying what was wrong with it on a screen nobody had opened.

In the Quantar's service software (RSS), under **Service**:

- **Status Report Screen.** `Station Wireline FW` must name a version. **`NOT_PRESENT`
  means the wireline board is not running**, and nothing below will work until
  it is replaced or repaired. **Proven here**: that was the fault.
- The note at the foot of the **Alignment** screens. `Station is Currently
  ACCESS DISABLED` is a station out of service.
- The station should report itself `ASTRO CAI CAPABLE`.

On the wireline board itself, the **AUX LED** should be lit. Dark means the
V.24 side is not alive.

## 3. Set up the repeater

### The codeplug

On the ASTRO tab. **Proven here**: all five were read back from the working
station.

| Setting | Value |
|---|---|
| Wireline Interface | `V.24 ONLY` |
| Analog Idle Link Check | `DISABLED` |
| Digital Idle Link Check | `ENABLED` |
| External Transmit Clock | `ENABLED` |
| RT/RT Configuration | `ENABLED` |

And `Astro To Wireline: ENABLED`.

`External Transmit Clock` is what makes the repeater take its clock from the
router, and `RT/RT Configuration` is repeater-to-repeater linking, which is
what QSP presents itself as.

**With RT/RT Configuration disabled, audio goes one way.** **Proven here**, at
the second site. Its repeater linked, was heard by QSP, and transmitted every
call sent to it, and **no other repeater transmitted a word it sent**. Every
call it put on the wireline was marked `04` where an enabled repeater marks
`02`, in the start, the end, the header and the first voice record of each
block, and nothing else differed; enabled, the same records carried `02` and
both repeaters transmitted each other's calls. QSP passes one repeater's call
to another unchanged, so it does not mend this; it says so instead, in its log
and on the Overview, naming the repeater. `Digital Idle Link Check` is why a quiet link
still carries frames: without it, a repeater that says nothing is behaving
correctly.

### The switches

On a Motorola TTN4010 there are two banks of four, **S101 and S102**, one for
each jack.

**Switch 1 on, and the other three off, on both banks.** **Proven here.** That
is the setting for a link whose clock comes from outside, which this one does.
Boards are often supplied with every switch off.

**An aftermarket board is not set the same way.** The published setting for
the W9CR level-converter board is switches 1 and 4 on. Read which board you
have, then its line. **Published.**

### Which jack

**The bottom one**, with a Motorola TTN4010 fitted. **Proven here.** The
published adapter table says the top one, and its own author notes that it is
the bottom with a real TTN4010.

## 4. Build the adapter

RJ-45 at the repeater end, DB-25 male at the Cisco cable. **Proven here.**

| RJ-45 | Signal | DB-25 |
|---|---|---|
| 1 | Receive clock | 17 |
| 2 | Carrier detect | 8 |
| 3 | Transmit clock | 15 |
| 4 | Ground | 1 |
| 5 | Receive data | 3 |
| 6 | Transmit data | 2 |
| 7 | Clear to send | 5 |
| 8 | Request to send | 4 |

**Then link DB-25 pin 6 to pin 20 inside the hood.** The router will not bring
its port up without Data Terminal Ready on pin 20, and the repeater's jack has
no pin to send it. This loops the router's own signal back. Without it
everything looks correctly wired and the link never comes up.

**Check for continuity between DB-25 pins 1 and 7** on the finished adapter
with the Cisco cable attached. The table grounds on pin 1 and RS-232 measures
against pin 7; many Cisco cables join them, and if yours does not, add the
link.

## 5. Set up the router

`192.0.2.4` stands for the router's own address and `192.0.2.10` for the QSP
server. The interface names are the ones on a 2921 with the card in slot 3;
yours will differ.

```
stun peer-name 192.0.2.4
stun protocol-group 1 basic
!
interface GigabitEthernet0/0
 ip address 192.0.2.4 255.255.255.0
!
interface Serial0/3/0
 mtu 2104
 no ip address
 encapsulation stun
 clock rate 9600
 stun group 1
 stun route all tcp 192.0.2.10
```

Then `write memory`. **Proven here.**

Four things that each cost an afternoon:

- **`stun peer-name` is the router's own address**, not the server's. The
  server is on the `stun route` line.
- **The router's address must not change.** `stun peer-name` has to equal it,
  so give the router a fixed address or a reservation. One that came from DHCP
  here moved at a reboot.
- **The clock rate is 9600.** A repeater does not synchronise at any other, and
  no log anywhere says why. `show controllers Serial0/3/0 | include clock` is
  the instrument; the `BW` figure in `show interfaces` is not, and does not
  change when the rate does.
- **Changing `encapsulation` removes the two `stun` lines beneath it.** Enter
  them again afterwards.

Set the router's clock from NTP as well. Its log and QSP's are only useful
together if their times agree.

## 6. Prove the wire before blaming anything

**`up/up` on a STUN interface means nothing.** STUN sends nothing of its own,
so the interface reads up with a dead far end, an unplugged cable or no
repeater at all. Likewise `show stun` reading `closed`: the word carries no
information, and the packet counters carry all of it.

Three checks, in order. Each needs only the router's console. **Proven here.**

**The adapter's jumper.** With the cable and adapter on and nothing in the
RJ-45 end, `show interfaces Serial0/3/0` should say `DTR=up`. That proves the
jumper and nothing else.

**The whole path, with a loopback.** Make a spare RJ-45 plug with pin 5 joined
to pin 6 and put it where the repeater's lead goes. Then borrow an
encapsulation that does send something:

```
conf t
interface Serial0/3/0
 encapsulation hdlc
 keepalive 5
end
clear counters Serial0/3/0
```

Twenty seconds later, `show interfaces Serial0/3/0 | include line
protocol|packets input|input errors|abort` should read `line protocol is up
(looped)`, packets in, and **zero errors**. That clears the router, the clock,
the cable and the adapter in one command. Then put it back:

```
conf t
interface Serial0/3/0
 no keepalive
 encapsulation stun
 stun group 1
 stun route all tcp 192.0.2.10
end
```

**Is the repeater sending?** With the repeater connected, `clear counters
Serial0/3/0`, wait fifteen seconds, and read the same line.

| You see | It means |
|---|---|
| `packets input` climbing | The repeater is talking. Go on to QSP |
| Zero packets, zero errors | Nothing is on the wire. Go back to step 2, then the jack and the switches |
| Zero packets, errors or aborts climbing | Something is on the wire and is not being read. The clock rate, `External Transmit Clock`, or the switches |

## 7. Set up QSP

On the console's **Network** page, in **Motorola P25 repeaters**:

- Turn on **Link Motorola P25 repeaters**.
- **Listen on** `0.0.0.0:1994`, the port the router dials.
- **Allowed routers**: the router's address. An empty list accepts any router
  that can reach the port, and the tunnel has no password. **The first address
  added locks out every router not on the list**, a repeater already linked
  included, so add every router at once.
- **Site number**: leave it empty unless the repeater's own site number is 2.
  QSP must not use the repeater's number, which is 1 unless its codeplug says
  otherwise.
- **Present as**: Repeater. Try Console only if the link does not come up.
- **Hold before transmitting**: leave it empty, which is 60 ms.
- **Send a call header to repeaters**: off. The Quantar here keyed without one.

Save, and restart QSP. These settings are read at start.

**What a working link looks like.** In QSP's log, in this order: `a router's
tunnel connected`, `the repeater asked for a link and was answered`, `the
repeater accepted QSP's link request`, `the repeater introduced itself`, and
`the repeater's link is up`. The Overview's P25 row then shows the repeater
with its link open, and the health page says `1 repeater link(s) open`.
**Proven here.** On the repeater, the wireline LED is said to flash while the
link is down and stay on once keepalives arrive. **Published.**

The link stays up, and when the router or QSP restarts it comes back by
itself. **Proven here.**

**Then two calls.** Key a radio through the repeater: the log says `a
transmission ended` with the radio's ID, and the call is in Last heard. Key a
radio into a P25 hotspot linked to this server: the repeater transmits it.
**Proven here**, both ways.

## 8. When it does not work

| You see | Where to look |
|---|---|
| Nothing in QSP's log when the router starts | The `stun route` address, the port, a firewall, and whether the listener is on |
| `a connection was refused: the address is not an allowed router` | **Allowed routers** on the Network page |
| The tunnel connects and nothing follows | The repeater is silent. Step 6's last check, then step 2 |
| `the repeater asked for a link` over and over, and never `the repeater's link is up` | **Present as**, then the site number |
| `the repeater's link dropped: it is asking to open it again` | The path between the router and QSP: the repeater starts again when it hears nothing for about five seconds |
| `the tunnel went quiet and was closed` | Nothing arrived for 30 seconds. The router, or the network |
| The link is up and the repeater will not transmit a hotspot's call | Turn on **Send a call header to repeaters** and restart. Tell the project if that is what it took |
| One repeater's calls never key another, while the other way works | **RT/RT Configuration** on the repeater that is not heard (step 3). The Overview says which, and the log says `this repeater marks its calls RT/RT disabled` |
| Hotspot calls reach the repeater with pieces missing | Whether the hotspot and the repeater share a frequency. The repeater then hears the radio directly as well, with errors, and the two copies compete |

**To see exactly what crossed the link**, give **Record to** a folder. Every
frame in both directions is written there as text. It includes the voice and
grows by about a megabyte for every five minutes of talking, so turn it off
again.

## 9. Across the internet

**Proven here, without a VPN**, on 2026-10-22: a repeater in Wisconsin linked
to a server in Texas, and calls carried both ways with good audio.

**How it was done.** The router at the repeater's site dials out, so nothing
is forwarded there. At the server's site:

- a port forward of **TCP 1994** to the QSP server, limited to the remote
  site's public address where the router allows it;
- **Allowed routers** on QSP holding the remote site's **public** address,
  which is what QSP sees after its NAT, and the local routers' own;
- on the remote router, `stun route all tcp` the server site's **public**
  address. It has to be a number: if that address changes, the line does.

`stun peer-name` stays the remote router's own LAN address, as in step 5.

**The tunnel has no password and no encryption.** What keeps a stranger out is
Allowed routers, and because the tunnel is TCP, an address cannot be forged
far enough to open one. Anybody on the path can read the frames. A VPN between
the sites closes that, and is still the stronger choice; which one is not
settled, and no recipe has been tested. The 1841 used here has a hardware VPN
module.

**Late audio, measured.** Calls from the remote repeater reached the local one
with the worst gap between voice records at 30 to 46 ms and `ran_dry` at 0 to
3, with QSP's hold at its default 60 ms, and the audio was good. If calls
break up on a distant repeater, look in QSP's log for `a call was sent to the
repeater`: a large `worst_gap_ms` or a `ran_dry` well above that says the
audio was already late reaching QSP, and raising the hold will help. If both
are small, the trouble is on the way to your router, and the hold will not
help.

**The repeater gives up after about five seconds of silence** and opens the
link again, so a path that stalls for longer than that drops the link each
time. **Published** for the limit; QSP sends a keepalive every two seconds to
stay inside it.

## 10. More than one repeater

**One tunnel is one repeater.** A router with two serial ports links two: give
each port its own `stun group`, with a `stun protocol-group` line for each
number, and each repeater its own site number in its codeplug; **not run**.
Repeaters on different routers need nothing special: **Proven here**, with two
Quantars, both of which said they were site 1, which did not matter.

Every linked repeater and every linked P25 hotspot hears every call, one call
at a time. A station that keys while another is talking is counted on the
Overview and not carried.

**A call from one repeater to another: proven here**, both ways, once both
had RT/RT Configuration enabled (step 3). QSP sends the call on as the
repeater sent it, record for record.

## The GTR 8000

**Expected to work, and never tried.** A GTR 8000 has a V.24 port on its front,
and its manual gives the pin-outs (section "V.24 Port Pin-Outs" in the base
radio manual). It is configured with CSS rather than RSS, and some of its
features are licensed.

So the router, the adapter's jumper, the clock rate and everything in QSP are
the same, and three things have to come from the GTR's own manual instead of
this page: **the port's pin-out**, which may not match the Quantar table above;
**the settings that correspond to the five in step 3**; and **whether V.24
needs a licence on your station**. Step 6 works on any repeater and will say
whether you have them right.

## Sources

- *IP link Quantar V.24 systems using Cisco routers*, ZL4JY, 2012:
  <https://www.qsl.net/kb9mwr/projects/dv/apco25/IP%20link%20Quantar%20V.24%20systems%20using%20Cisco%20routers%20V2.pdf>.
  The adapter table, the jumper, the router classes and the codeplug settings.
- The W9CR wiki: <https://wiki.w9cr.net/index.php/Quantar_V.24_Interface> and
  <https://wiki.w9cr.net/index.php/Quantar_Linking>.
- *Quantar V.24 Card Switch Settings*, Batboard:
  <https://batboard.batlabs.com/viewtopic.php?p=449181>. Which bank is which
  jack, and which switch selects an outside clock.
- Motorola's Quantar manuals, 68P81088E90 and 6881095E05, for the wireline
  board; and the GTR 8000 base radio manual.
- [`P25-PLANNING.md`](P25-PLANNING.md) for how each "Proven here" was proven.
