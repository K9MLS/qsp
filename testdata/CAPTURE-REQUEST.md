# HBP Capture Request

**For: a ham with a working DMR hotspot or repeater**
**Time needed: ~20 minutes**
**What this is for: building a free, open-source DMR linking server**

Thank you for doing this. What you capture here becomes the reference these
parsers are tested against, which means it directly determines whether this
software is correct or merely confident.

---

## What we need, in one sentence

A packet capture of your hotspot's Homebrew Protocol conversation with its
master — from cold start, through a voice transmission, to clean shutdown.

---

## Before you start

**Find your port.** The Homebrew/MMDVM protocol commonly uses UDP **62031**, but
62030 and 62032 are also in use, and BrandMeister and private masters vary.
Check your MMDVMHost or DMRGateway config for the master's port and use that
number below.

**On Pi-Star, make the filesystem writable first:**

```sh
rpi-rw
```

**Stop the service before capturing.** The login handshake is the single most
important part of this capture, and it only happens at connect. If the hotspot
is already connected, we miss it.

```sh
sudo systemctl stop mmdvmhost
```

(Or `dmrgateway`, depending on your setup.)

---

## The capture

**1. Start the capture first.**

```sh
sudo tcpdump -i any -s 0 -w qsp-hbp-01.pcap 'udp port 62031'
```

Replace 62031 if yours differs. Leave this running for everything below.

**2. In a second session, start the service.**

```sh
sudo systemctl start mmdvmhost
```

**3. Let it sit idle for 3 minutes.** Don't touch it. This gives us the
keepalive rhythm.

**4. Key up and talk for about 10 seconds**, then unkey cleanly. Please actually
talk — a silent carrier doesn't produce the same voice frames. Count to ten if
nothing comes to mind.

**5. Wait 30 seconds. Key up again for about 5 seconds and unkey.** Two
transmissions tell us more than one.

**6. If it's easy, do one more on a different talkgroup or timeslot.** Skip this
if it's a hassle.

**7. Stop the service cleanly.**

```sh
sudo systemctl stop mmdvmhost
```

**8. Wait 10 seconds, then stop the capture** with Ctrl-C.

---

## Please also send a few notes

Anything you can tell us. A rough version is far better than nothing:

- Which software and version (Pi-Star 4.x, MMDVMHost, DMRGateway, etc.)
- Which master or network you connected to
- Your callsign and DMR ID
- Talkgroup and timeslot used
- Color code
- Roughly when you did each step, if you happened to note it
- **Anything that looked unusual or didn't go as expected** — a failed first
  connect, a dropped transmission, an error in the log. Those are valuable, not
  a spoiled capture.

---

## A second capture, if you're willing

Different software talking to the same protocol is where interoperability bugs
hide. If you have access to a second setup — a different hotspot, a real
repeater, a different master — the same procedure again as `qsp-hbp-02.pcap`
would be genuinely useful.

Don't go out of your way for it.

---

## What's in the file, and what isn't

The filter captures **only** Homebrew Protocol traffic on that one UDP port.
Nothing else on your network is recorded — no web browsing, no email, no other
services.

What it does contain: your callsign, your DMR ID, and your hotspot's
configuration announcement (location, description, URL — whatever you have set).
All of that is already public by the nature of the hobby.

Your master password is **not** sent in the clear by this protocol — the
authentication step sends a hash, not the passphrase itself. That said, if
you'd rather change your master password afterward, nobody would think it odd.

If anything in the capture makes you uncomfortable, say so and we'll work
without it. This is a favor, not an obligation.

---

## Sending it

The file should be small — tens to a few hundred kilobytes. Any method is fine.

If it's unexpectedly large, something other than HBP traffic got captured; let
us know rather than sending it.

---

## What happens to it

It goes into a public open-source repository as a test fixture, under
`testdata/hbp/`, with your callsign credited in the notes alongside it unless
you'd prefer otherwise. Every future change to the DMR parser gets tested
against your capture.

If you'd rather not be credited, or would rather it not be public, tell us —
we'll respect either, and a private-use-only capture is still enormously useful.
