# Bringing other radios in with DVSwitch

For somebody running a QSP server who wants an AllStar repeater or node, or
another digital mode, on the network. The node owner's half is the console's
public **DVSwitch guide**, at `/dvswitch` on every server and linked from the
Join page, with this server's address, ports and talkgroups filled in. Send
node owners that link; this page is the server's half and the reasons.

**QSP has no AllStar connector and converts nothing between modes, and
neither is planned.** It does not need to: **DVSwitch** does both on the node
owner's own machine. Its MMDVM_Bridge logs in to a DMR master exactly as a
hotspot does, and its P25Gateway reaches a P25 reflector exactly as a Pi-Star
does. To QSP a node is one more hotspot, or one more P25 gateway. The decision
and its reasons are at the foot of this page.

**Nobody has linked DVSwitch to QSP yet.** Every instruction is marked with
how far it can be trusted, as [`P25-REPEATER-SITE.md`](P25-REPEATER-SITE.md)
does, and the console's guide carries the same marks:

| Mark | Means |
|---|---|
| **Proven here** | Done against a QSP server, and seen to work |
| **Published** | From DVSwitch's own documentation and guides, and not tried here |
| **Not known** | Nobody this project has read says, and it has not been tried |

The first node linked turns these into **Proven here**, and what it finds
wrong with this page and the guide is the most useful thing it can send back.

## What it gets a network

| Route | What changes for the people on the radios | Who sets it up | Mark |
|---|---|---|---|
| AllStar to DMR | Anyone on the node, on any FM radio, talks on a DMR talkgroup here | The node owner | **Published** |
| AllStar to P25 | The same on the P25 side, heard by every P25 gateway and Motorola repeater linked here | The node owner | **Published** |
| YSF, D-STAR or NXDN onto DMR | A reflector of another mode and a DMR talkgroup here become one conversation | The administrator | **Published** |
| DMR and P25 as one conversation | A DMR talkgroup and a P25 one here are heard on both | The administrator | **Not known** |

## How it goes together

```
AllStar node (ASL3)
    |  chan_usrp: 8 kHz audio over UDP, on the node
Analog_Bridge   - turns audio into AMBE (DMR) or IMBE (P25), and back
    |  local UDP
MMDVM_Bridge    - DMR: logs in to a DMR master, as a hotspot does
    |             P25: hands frames to P25Gateway
    |  across the internet
QSP             - DMR on UDP 62031, P25 on UDP 41000 by default
```

Everything above the last line runs on the node owner's machine, with their
vocoder: a DV3000 or ThumbDV, or the software one DVSwitch offers on some
machines. **QSP transcodes nothing for it**, so a node takes nothing from the
AMBE dongle the Zello connector uses. **Published.**

## On the QSP server

### For a node on DMR

1. **Admit the node.** On **Access control**, under **Who may connect**, add
   the DMR ID the node registers with: the `Id` in its `MMDVM_Bridge.ini`.
   **Proven here** for hotspots; a node is not different to QSP.
2. **Give it a password.** The network's shared password, or one of its own
   from **Issue a password** on the same page. It goes to the node owner
   separately; the guide does not show it.
3. **Decide what it hears.** With **What each member hears** on the Network
   page off, which is how it starts, every peer receives every talkgroup, and
   the node does too. With it on, a peer hears a talkgroup only after it has
   transmitted on it, and pinning a talkgroup to a peer is not yet editable in
   the console, so a node that should be there before anybody speaks needs
   that setting off.
4. **List the talkgroups** under the Join page's settings. The guide shows
   the node owner each one as it arrives here, which is what `txTg` must be:
   a node has no hotspot rewriting its number.

### For a node on P25

1. **Turn on Accept P25 gateways** in **Network settings**, under **P25
   gateways**. Until it is, the guide says this network takes no P25 gateways
   and gives no P25 settings, rather than a port nothing answers on.
2. **Answer its callsign.** If **Allowed gateways** lists any, add the
   callsign in the node's `P25Gateway.ini`. As
   [`P25-GATEWAY.md`](P25-GATEWAY.md) says, a callsign is a claim and not a
   credential: that is the reflector protocol, not QSP.
3. **Tell the node owner the reflector number** your gateways use. QSP's P25
   side is a flat reflector today, so any number reaches the same audio; the
   number matters to the node owner's host file, and `P25-GATEWAY.md` has the
   trap where a downloaded host list overrides a local entry.

A node on P25 is heard by the Motorola repeaters linked here as a Pi-Star's
gateway is: QSP relays between gateways and repeaters both ways.

### For the administrator's own bridges

**Other modes onto DMR.** A DVSwitch machine with a vocoder logs in here as a
hotspot on one talkgroup (admit its ID and give it a password, as above) and
to a YSF, D-STAR or NXDN reflector on its other side. **Published.**

**DMR and P25 together.** The same machine on both of this server's sides: a
hotspot on a DMR talkgroup and a P25 gateway on a reflector number, with
Analog_Bridge converting AMBE to IMBE between them. Whether DVSwitch's IMBE
conversion is software or needs hardware of its own is **Not known**: the
guides read for this set `ambeMode = P25` and name no device, and none says
outright.

## On the node

The guide gives the node owner these with the server's values filled in. The
names are the ones DVSwitch's sample files and DVSwitch Server's `dvs` menu
use, usually under `/opt`; check them against the version installed.
**Published.**

DMR, `MMDVM_Bridge.ini`:

```
[General]
Id=<the DMR ID admitted under Access control>

[DMR Network]
Enable=1
Address=<this server's address>
Port=62031
Password=<the password from the network's administrator>
```

DMR, `Analog_Bridge.ini`, under `[AMBE_AUDIO]`:

```
ambeMode=DMR
gatewayDmrId=<the same DMR ID>
txTg=<a talkgroup as it arrives here>
txTs=<its timeslot>
```

P25, the same files changed, and P25Gateway's:

```
Analog_Bridge.ini   [AMBE_AUDIO]  ambeMode=P25, txTg=<the reflector number>
MMDVM_Bridge.ini    [P25 Network] Enable=1
P25Gateway.ini      [General] Callsign=<allowed callsign>
                    [Network] Startup=<the reflector number>
local hosts file    <the reflector number>  <this server's address>  41000
```

## What a working link looks like

On DMR the node appears on the Overview and the **Peers** page as a Homebrew
peer with the ID it registered, and a call keyed on the AllStar side appears
in **Last heard** under the talkgroup set in `txTg`. On P25 it appears among
the P25 stations on the Overview by its callsign. **Not known** until a node
has done it.

## When it does not work

| You see | Where to look |
|---|---|
| The node's address under **Logins being refused** on the Overview | The ID under **Who may connect**, then the password |
| Nothing at all on QSP | The address and port in the node's files, and whether it can reach the server's UDP port |
| The node connected, and calls from it never appear | `txTg` and `txTs`, then `gatewayDmrId` |
| A P25 node never appears | The listener, the allowed callsigns, and `P25-GATEWAY.md`'s host-file traps |
| Calls from DMR are not heard on AllStar | **What each member hears** on the Network page, step 3 above |
| Audio too loud or too quiet in one direction | Analog_Bridge's own gain settings, on the node. QSP's Zello levels do not touch this path |

## Why there is no native connector

The blueprint once planned one: QSP already speaks USRP, the audio protocol
AllStar's `chan_usrp` uses, for Zello, so a second USRP endpoint looked cheap.
It was withdrawn on 2026-10-26, as EchoLink was in 0418, for three reasons.

- **DVSwitch already does it**, maturely, for DMR and P25 alike, and needs
  nothing from QSP.
- **The vocoder.** A native connector would transcode on QSP's AMBE dongle,
  which is one channel and is the Zello connector's. A Zello call and an
  AllStar call at once would need a second dongle and a scheduler between
  them. Through DVSwitch, each node brings its own.
- **Levels and timing** would have to be learned again for a second analogue
  path, on the operator's hardware rather than the node owner's.

The same reasoning keeps cross-mode conversion out of the server
([ADR-0034](adr/ADR-0034-p25-is-native.md)): a vocoder in QSP would be one
shared channel deciding who is heard, and a DVSwitch machine per bridge puts
the cost with whoever wants the bridge.

If native AllStar is ever taken up, it goes back into `unbuiltSubsystems` in
`cmd/qsp/app.go` first, and the health report says so.
