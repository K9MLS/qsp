# IPSC Capture Request

**For: a club with a Motorola MOTOTRBO repeater running IP Site Connect**
**Time needed: ~30 minutes, plus a repeater restart**
**What this is for: building a free, open-source DMR linking server**

Thank you for doing this. **QSP cannot support a Motorola repeater until this
capture exists**, and it cannot be substituted with anything else.

There is no published IPSC specification. Every open implementation of it was
reverse-engineered from traffic, and QSP has decided to build from a capture
rather than from somebody else's implementation — see
[ADR-0029](../docs/adr/ADR-0029-ipsc-from-capture.md). That decision is only
possible because somebody with a real repeater is willing to record one.

What you capture becomes the reference this parser is tested against. It decides
whether the software is correct or merely confident.

---

## What we need, in one sentence

A packet capture of your repeater's IPSC conversation with its master, from
its cold start, through voice on both timeslots, to shutdown.

---

## Before you start

**Find the port.** IPSC commonly uses UDP **50000**, but it is configurable in
the repeater's CPS and installations vary widely. Check the repeater's
programming for the master's UDP port and use that number below.

**Capture on the master's side**, or on a machine that sees both directions. A
capture taken at one repeater in a multi-site system shows only that repeater's
half of the conversation, and the registration handshake is the part where both
halves matter most.

**The registration happens once, at startup.** Start the capture *before* the
repeater connects, or the most important part is already over. That means either
restarting the repeater or restarting whatever it registers with.

---

## The capture

```sh
sudo tcpdump -i any -n -s0 -w ipsc-session.pcap udp port 50000
```

`-s0` records whole packets. Without it they are truncated and the fixture is
worthless for anything but counting.

Leave it running for everything below, then stop it with Ctrl-C.

---

## What to do while it runs

Do these in order, and **write down roughly when you did each one** — a note
saying "voice on TS2 about two minutes in" is worth as much as the packets.

1. **Start the repeater**, or restart its network connection, so the whole
   registration sequence is recorded.
2. **Wait two or three minutes** doing nothing. This records the keepalive
   intervals, which matter as much as the format: an implementation that guesses
   them looks correct until a repeater silently drops.
3. **Key up on timeslot 1** for about five seconds, on a group call.
4. **Key up on timeslot 2** for about five seconds, on a group call.
5. **Make a private call** from one radio to another, if you have two.
6. **Send a text message**, group or private.
7. **If you have a second repeater in the system**, let it register while the
   capture runs. The peer list exchange is how IPSC repeaters learn about each
   other, and it cannot be seen with only one.
8. **Shut the repeater down cleanly**, if the equipment offers a way. A clean
   disconnect went unimplemented in the other protocol for exactly as long as no
   capture contained one.

---

## What to send

- `ipsc-session.pcap`
- A note listing:
  - **Equipment**: model and firmware version, for every repeater involved.
  - **What you did and roughly when**, matching the list above.
  - **The talkgroups and radio IDs used** — these are public information in
    amateur radio and are what let somebody check the parser found the right
    numbers.
  - **Anything unusual** about the installation: a non-standard port, a
    non-Motorola master, authentication enabled.

**On authentication:** if your system uses an IPSC authentication key, say so,
but **do not send the key**. The capture is more useful without it, and a
capture that needs a secret to interpret is one nobody can safely publish.

---

## What happens to it

It goes in `testdata/ipsc/` with a sibling `.md` recording where it came from
and what is in it, exactly as the existing HBP fixtures do. See
[`testdata/README.md`](README.md).

Radio IDs and callsigns stay as they are — they are public. If your capture
contains anything else identifying, say so and it will be sanitized or the
capture will be kept unpublished and used only for testing.
