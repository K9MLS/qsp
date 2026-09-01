# IPSC capture plan — XPR8300 and SLR5700

**This is the plan for the equipment actually available**: K9MLS's XPR8300 in
the Denton lab, and an SLR5700 belonging to KD9EJA.
[`../IPSC-CAPTURE-REQUEST.md`](../IPSC-CAPTURE-REQUEST.md) is the general
version, written to be handed to a club nobody knows. This one names machines
and assumes two people on a phone call.

[ADR-0029](../../docs/adr/ADR-0029-ipsc-from-capture.md) governs it. **No IPSC
wire-format code is written before a capture exists** — not a parser, not a
constant, not a message type. And DMRlink and HBlink3 stay unread until a
capture is in hand and has been shown not to answer something: reading them
makes QSP's IPSC a derivative work permanently, and no amount of later rewriting
undoes it.

---

## The problem this plan exists to solve

A MOTOTRBO repeater is a closed box. There is no shell on it, so `tcpdump`
cannot run where the traffic is, and a switched network does not deliver one
host's unicast to another. **The single most common way this goes wrong is
running the capture on a convenient machine that is merely on the same LAN and
recording an empty file**, which looks like a protocol problem for an hour
before anyone checks.

So the capture host has to be somewhere the packets genuinely pass through. That
is the whole difficulty, and everything below is about placing it.

---

## Phase 0 — before any of this is scheduled

Three things to establish, because each one can cancel the session.

**Is IP Site Connect licensed and enabled on each repeater?** It is a purchased
feature on MOTOTRBO, not something every unit has. Check in CPS on both. If the
XPR8300 does not have it, phase 1 is impossible too and KD9EJA's repeater becomes
the only route.

**What CPS version does each repeater need?** The XPR8300 is a much older
platform than the SLR5700, and one CPS may not program both. Worth knowing
before an evening is booked, not during it.

**Will they link at all?** Firmware compatibility across a wide generational gap
is a real risk with IPSC. *Unverified — this is a caution, not a claim, and the
capture is what will settle it.* If they refuse to link, phase 1 still works
with the XPR8300 alone, and the full capture becomes KD9EJA plus a second
repeater of his.

**What is KD9EJA's SLR5700 doing now?** If it is carrying a club's traffic rather
than sitting on a bench, the session is a negotiation with a club and not an
evening with a friend, and it should be planned as one.

---

## Phase 1 — one repeater, no master, and it can be done alone today

**Point the XPR8300's IPSC master address at a Linux box. Do not run anything on
that box except `tcpdump`.**

The repeater will try to register, get no answer, and try again. That failure is
the point: **the packets are addressed to the capture host**, so an ordinary
`tcpdump` on an ordinary interface records them. No mirror port, no bridge, no
extra hardware, no second repeater, nothing on the air.

On the capture host:

```sh
sudo tcpdump -i eth0 -n -s0 -w ipsc-phase1.pcap host <repeater-ip>
```

Use the **real interface name**, not `-i any`: on Linux `-i any` records
Linux cooked-mode headers and throws the Ethernet header away.

Do not filter on `udp port 50000`. 50000 is the common default and installations
vary; filter on the repeater's address instead and read the port out of what
arrives. Let it run five minutes and stop it.

### What this yields

- The registration request, whole.
- The repeater's peer ID on the wire, which can be checked against the number
  CPS says it is — the single best sanity check available without a spec, and
  the one that anchors every offset found later.
- The retry interval, and whether there is a backoff.
- Source and destination port behaviour.

### What it cannot yield

Anything that needs an answer: the peer list, keepalive cadence in both
directions, voice, a private call, a disconnect. Phase 1 is not the capture. It
is the free half of it, and it de-risks the evening when both repeaters and both
people are committed.

### The technique that makes phase 1 worth more than one file

**Capture the same event twice with exactly one setting changed, and diff the
two.** Change the repeater's radio ID in CPS, restart, capture again. The bytes
that moved are the ID field, and its offset, width and endianness fall out
without a specification and without reading anybody's implementation. The same
trick works on the talkgroup, the slot and the declared port.

Two captures that differ in one known way are worth more than ten that differ in
unknown ways. Name the files for what was changed:
`ipsc-phase1-id-3132910.pcap`, `ipsc-phase1-id-3132911.pcap`.

---

## Phase 2 — two repeaters, and the capture that actually unblocks IPSC

Make the **XPR8300 the IPSC master**, on the Denton LAN, and KD9EJA's SLR5700 the
peer. Master at the end where the capture host is, because the master's side
sees both halves of every conversation and because restarting the master is what
produces a registration on demand.

Then place the capture host, in order of preference.

### (a) Bump in the wire — always works

A Linux box with two network interfaces, bridged, sitting physically between the
XPR8300 and the switch. Every packet to or from the repeater crosses the bridge,
in both directions, and nothing else needs configuring. A USB gigabit adapter
is enough to give any laptop a second interface.

```sh
sudo ip link add br0 type bridge
sudo ip link set eth0 master br0        # to the switch
sudo ip link set eth1 master br0        # to the repeater
sudo ip link set eth0 up
sudo ip link set eth1 up
sudo ip link set br0 up
sudo ip addr add 192.168.1.NNN/24 dev br0   # so markers below can be sent
sudo tcpdump -i br0 -n -s0 -w ipsc-phase2.pcap
```

Unfiltered. The repeater is the only thing on that segment, so the volume is
small and the port number is one of the things being learned. Everything else
the repeater says — its other management chatter — is information, not noise.

### (b) A mirror port, if the switch is managed

Mirror the repeater's port to the capture host's port. Equivalent result, no
extra hardware, and it depends on owning a switch that offers it.

### (c) The router, if it runs Linux or BSD

With one repeater local and one remote, **all** traffic between them crosses the
router, so `tcpdump` on the WAN interface catches the entire conversation. This
is the cheapest option when it is available. It is listed third only because a
consumer router usually is not.

### What will not work

`tcpdump` on the QSP VM at 192.168.1.247, or any other host that merely shares
the LAN. A switch forwards unicast to one port. The capture will contain
broadcast traffic and nothing else, and it will look like the repeaters are not
talking.

---

## Marking the capture from inside itself

A written note and a wall clock drift, and they do not survive the file being
handed to somebody else. Put the notes **in the packets**:

```sh
mark() { echo "MARK $*" | nc -u -w0 <repeater-ip> 9; }

mark ts1 group voice start
mark ts1 group voice end
mark private call k9mls to blake
```

Port 9 is discard. The repeater ignores it, the payload is readable text in the
capture, and `tcpdump -A -r ipsc-phase2.pcap 'udp port 9'` prints the running
commentary with timestamps attached. Chapter markers, for free.

---

## The sequence, once the capture is running

Do these in order. Mark each one.

1. **Restart the master.** The registration sequence happens once, and a capture
   started afterwards has missed the part with the most structure in it.
2. **Let the peer register**, and mark it. This is the peer list exchange, the
   thing one repeater cannot produce, and the reason two are needed at all.
3. **Wait three minutes doing nothing.** Keepalive intervals matter as much as
   keepalive format; an implementation that guesses them looks correct until a
   repeater silently drops.
4. **Group call on timeslot 1**, about five seconds. Unkey cleanly and let it
   hang.
5. **Group call on timeslot 2**, about five seconds.
6. **A second group call on TS1 on a different talkgroup**, so the talkgroup
   field can be found by diffing two calls rather than guessed.
7. **A private call**, radio to radio, both directions if two radios allow it.
8. **A text message**, group then private.
9. **Pull the peer's network cable** and wait for the master to notice. The
   timeout path is a real state transition and nothing else produces it.
10. **Reconnect it**, so a re-registration appears in the same file.
11. **Shut the peer down cleanly**, if the equipment offers a way. `RPTCL` went
    unimplemented in HBP for exactly as long as no capture contained one.

---

## What comes back with the file

- The `.pcap` files, unfiltered, `-s0`.
- Model and **firmware version** of both repeaters.
- The radio IDs, talkgroups and IPSC ports used. These are public in amateur
  radio and are what let the parser be checked against the truth.
- Which repeater was master.
- Anything non-default: a changed port, authentication enabled, a slot disabled.

**If IPSC authentication is enabled, say so and do not send the key.** A capture
that needs a secret to interpret is one nobody can publish, and the authenticated
frames are less useful than the unauthenticated ones. Turn it off for the
capture if the installation allows.

Each file lands in this directory with a sibling `.md` recording where it came
from, what is in it and what was done to it, exactly as the HBP fixtures do. See
[`../README.md`](../README.md).
