# Linking an AllStar node

For somebody running a QSP server whose network has an AllStar node, often on
a repeater, that wants to talk to it. The node owner's half is on the
console's **Join** page, under **Linking an AllStar node**, with this
server's address filled in.

**QSP has no AllStar connector, and none is planned.** It does not need one:
an AllStar node reaches a DMR network through **DVSwitch**, whose
MMDVM_Bridge logs in to a DMR master exactly as a hotspot does. To QSP the
node is one more hotspot. The decision and its reasons are at the foot of
this page.

**Nobody has linked a node to QSP this way yet.** Every instruction is marked
with how far it can be trusted, as [`P25-REPEATER-SITE.md`](P25-REPEATER-SITE.md)
does:

| Mark | Means |
|---|---|
| **Proven here** | Done against a QSP server, and seen to work |
| **Published** | From DVSwitch's own documentation and sample files, and not tried here |
| **Not known** | Nobody this project has read says, and it has not been tried |

The first node linked turns these into **Proven here**, and what it finds
wrong with this page is the most useful thing it can send back.

## How it goes together

```
AllStar node (ASL3)
    |  chan_usrp: 8 kHz audio over UDP, on the node
Analog_Bridge   - turns audio into AMBE and back, on the node's own vocoder
    |  local UDP
MMDVM_Bridge    - logs in to a DMR master, as a hotspot does
    |  Homebrew, UDP 62031, across the internet
QSP
```

Everything above the last line runs on the node owner's machine, with their
vocoder: a DV3000 or ThumbDV, or the software emulator DVSwitch offers.
**QSP transcodes nothing for it**, so a node linked this way takes nothing
from the AMBE dongle the Zello connector uses. **Published.**

## On the QSP server

1. **Admit the node.** On **Access control**, under **Who may connect**, add
   the DMR ID the node registers with: the `Id` in its `MMDVM_Bridge.ini`.
   That is the same thing a hotspot owner is asked for. **Proven here** for
   hotspots; a node is not different to QSP.
2. **Give it a password.** The network's shared password, or one of its own
   from **Issue a password** on the same page. It goes to the node owner
   separately; the Join page does not show it.
3. **Decide what it hears.** With **What each member hears** on the Network
   page off, which is how it starts, every peer receives every talkgroup, and
   the node does too. With it on, a peer hears a talkgroup only after it has
   transmitted on it, and pinning a talkgroup to a peer is not yet editable in
   the console, so a node that should be there before anybody speaks needs
   that setting off.
4. **Nothing else.** The UDP port hotspots reach is the one the node reaches.

## On the node

The Join page gives the node owner these with the server's address and port
filled in. The names are the ones DVSwitch's sample files use; check them
against the version installed. **Published.**

`MMDVM_Bridge.ini`:

```
[General]
Callsign=<the node owner's callsign>
Id=<the DMR ID admitted under Access control>

[DMR Network]
Enable=1
Address=<this server's address>
Port=62031
Password=<the password from the network's administrator>
```

`Analog_Bridge.ini`, under `[AMBE_AUDIO]`:

```
txTg=<a talkgroup from the Join page's table>
txTs=<its timeslot>
```

Linking the node's AllStar side to Analog_Bridge, and choosing a vocoder, are
DVSwitch's own setup and are not repeated here.

## What a working link looks like

The node appears on the Overview and the **Peers** page as a Homebrew peer
with the ID it registered, and a call keyed on the AllStar side appears in
**Last heard** under the talkgroup set in `txTg`. **Not known** until a node
has done it.

## When it does not work

| You see | Where to look |
|---|---|
| The node's address under **Logins being refused** on the Overview | The ID under **Who may connect**, then the password |
| Nothing at all on QSP | The `Address` and `Port` in `MMDVM_Bridge.ini`, and whether the node can reach the server's UDP port |
| The node connected, and calls from it never appear | `txTg` and `txTs` in `Analog_Bridge.ini` |
| Calls from DMR are not heard on AllStar | **What each member hears** on the Network page, step 3 above |
| Audio too loud or too quiet in one direction | Analog_Bridge's own gain settings, on the node. QSP's Zello levels do not touch this path |

## Why there is no native connector

The blueprint once planned one: QSP already speaks USRP, the audio protocol
AllStar's `chan_usrp` uses, for Zello, so a second USRP endpoint looked cheap.
It was withdrawn on 2026-10-26, as EchoLink was in 0418, for three reasons.

- **DVSwitch already does it**, maturely, and needs nothing from QSP.
- **The vocoder.** A native connector would transcode on QSP's AMBE dongle,
  which is one channel and is the Zello connector's. A Zello call and an
  AllStar call at once would need a second dongle and a scheduler between
  them. Through DVSwitch, each node brings its own.
- **Levels and timing** would have to be learned again for a second analogue
  path, on the operator's hardware rather than the node owner's.

If native AllStar is ever taken up, it goes back into `unbuiltSubsystems` in
`cmd/qsp/app.go` first, and the health report says so.
