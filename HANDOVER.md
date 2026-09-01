# Handover, evening of 2026-09-01

Read `NEW-SESSION.md` for the standing brief and **§8d** of `PROJECT_MEMORY.md`
for where to start. This is what changed today.

## The headline

**QSP serves Motorola repeaters.** This morning IPSC was an empty fixture
directory and a line in the phase table. Tonight an XPR8300 is registered to the
production server, its transmissions are recorded, and the audio conversion
between Motorola and the rest of the network is proved lossless against real
captured traffic.

Nine patches, 0.1.11 to 0.1.19. Nothing was derived from another
implementation: DMRlink and HBlink3 remain unread, and one that surfaced during
research was deliberately not opened.

## What is done

- **Seven IPSC fixtures**, including two failures kept on purpose.
- **Nine message types.** The envelope is a type byte and a big-endian *sender*
  ID.
- **A listener in the binary**, wired to configuration, health and the
  lifecycle, live on `qsp-server:50000`.
- **`internal/dmrfec`**, converting between IPSC's 49-bit vocoder parameters and
  the 72-bit protected frames a DMR burst carries. **884 real bursts
  round-tripped bit-exact.**

## Do this first

**IPSC → HBP routing.** Motorola in, hotspots out. Parser, FEC and routing core
all exist; it needs no new capture and no equipment. Key the XPR8300 and hear it
in Wisconsin.

**Do not build the reverse until it is captured.** Nothing has ever recorded a
master sending voice to a repeater. What QSP would emit is a guess, and a
repeater that receives malformed voice may key its transmitter with it.

## Two hours that should not be spent twice

**A repeater will not register with a master carrying its own radio ID.** Thirty-
nine retries over six minutes against correct replies, and it looks exactly like
a protocol fault. Validation now refuses the collision.

**A Motorola repeater has two port fields.** `Master UDP Port` is what it dials;
`UDP Port` is what it binds. Set to 50000 and 50001, a master that looked correct
served a port nobody was calling and answered with ICMP unreachable *from its own
IP stack*. `nmap -sU` against the repeater ended it after two wrong theories.
**When a device's own stack sends the refusal, ask the device what it bound.**

## The method

**Every byte read by eye today was wrong. Every differential was right.** The
trailer, the master ID, the "timeslot" that was a call counter, the interleave
geometry — each settled by changing one thing and diffing, or by running
candidate readings against thousands of real frames.

## Still open, cheap

Two key-ups on different talkgroups. Every captured transmission read
destination 455, so nothing has ever moved that field.

The console page: a repeater is visible in `/healthz` and the journal but not on
the dashboard.
