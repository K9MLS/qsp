# Handover, late on 2026-09-01

Read `NEW-SESSION.md` for the standing brief and **§8e** of `PROJECT_MEMORY.md`
for where to start.

## The headline

**IPSC went from an empty fixture directory to a Motorola repeater on the
production server with a proved-lossless audio path to the rest of the network,
in one day.** Nineteen patches, 0.1.11 to 0.1.29. DMRlink and HBlink3 remain
unread, and one implementation that surfaced during research was deliberately
not opened.

## What is built

A listener serving repeaters, live on `qsp-server:50000`. Nine IPSC message
types. Seven fixtures. And the whole audio conversion:

- `internal/dmrfec` — vocoder FEC, burst assembly, EMB, BPTC(196,96)
- `internal/ipscbridge` — a Motorola voice frame becomes a Homebrew burst

**884 real bursts round-tripped bit-exact. 740 burst middles rebuilt from a
position and a colour code. 28 data bursts rebuilt from their own payload. 54
bursts produced from real Motorola audio with every vocoder payload unchanged.**

The strongest single piece of evidence: an XPR8300 over IPSC and an MMDVM
hotspot over Homebrew, captured on different days on different equipment,
produce the identical vocoder frame for silence.

## Do this first

**Wire the converter to routing.** No discovery left, only wiring. Then a
Motorola repeater is audible on a hotspot.

## Three small things, all cheap

- **Which slot bit value is timeslot 1.** One sentence from the operator; it was
  never written down at the radio.
- **One key-up on a different talkgroup.** Every transmission ever captured
  reads destination 455, so nothing has moved those bytes.
- **The Link Control checksum**, for voice headers and terminators. A fitting
  exercise against 28 data bursts already in the repository. No equipment.

## The method

**Every reading taken by eye was wrong. Every differential was right** — seven
times over two days. A wrong hypothesis scores zero; that asymmetry is the
evidence.

## Two traps

**Never count failures.** The container always fails `TestDocumentedPathsExist`,
so a new failure kept the total at eight and was invisible. List them by name.

**The Homebrew captures hold every burst twice**, once arriving and once
relayed. Skipping equal neighbours collapses two genuine positions into one.
Take every second burst.
