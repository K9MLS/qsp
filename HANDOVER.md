# Handover, 2026-09-02

Read `NEW-SESSION.md` for the standing brief and **§8f** of `PROJECT_MEMORY.md`
for where to start.

## The headline

**A Motorola repeater in Idaho was heard on a hotspot in Wisconsin.** Nine
patches, 0.1.30 to 0.1.39. Three IPSC peers across two repeater models. DMRlink
and HBlink3 remain unread.

## What changed

- **The bridge works, Motorola to hotspot.** Voice header, audio, terminator —
  a complete, well-formed DMR transmission, confirmed on air by KB9TYC hearing
  KD9EJA.
- **The Link Control checksum is solved.** Reed-Solomon (12,9) from ETSI
  TS 102 361-1 Annex B.3.7, verified 28 of 28 against real bursts. The data type
  masks were **measured, not read** — `0x969696` and `0x999999` fell out of the
  arithmetic.
- **The transmit path exists**, built from inference under ADR-0041 at the
  operator's direction. **It does not work yet — see below.**
- Two shipped concurrency defects fixed: `routing.Core` and `peers.Master` were
  both reached by two goroutines with no lock (ADR-0038, ADR-0039).

## Do this first

**Fix the outbound frame shape.** The transmit path sends frames that do not
match what a repeater sends, and this is measurable against fixtures already in
the repository — no equipment, no capture session.

| | QSP sends | A real repeater sends |
|---|---|---|
| Header / terminator | **33 bytes** | **54 bytes** |
| Voice frames | **66 bytes, all of them** | **52, 57, 57, 57, 66, 57** cycling |

The header is built with no payload at all; a real one carries a full Link
Control block. And the 14-byte trailer is appended to every frame instead of
varying by superframe position — the same 1:4:1 ratio the existing sync /
fragment / fragment-with-LC tests already measure.

`body[20]` is a marker: `0x67` on headers, `0x07`/`0xe7`/`0x87` on the three
voice shapes. QSP writes zero.

Both shapes are visible in `testdata/ipsc/ipsc-two-peers.pcap` from two repeater
models. Build a frame, require the shape back, same standard as the BPTC and
Slot Type work.

## The method

**Every reading taken by eye was wrong. Every differential was right** — now
nine times. A wrong hypothesis scores zero.

**And a right hypothesis evaluated by broken arithmetic also scores zero.**
Thirty thousand candidate Reed-Solomon constructions were searched and all
scored zero while the correct field polynomial and generator roots sat inside
the search space; the division applying them was wrong. The score cannot tell
the two apart. Where a standard gives both a generator matrix and a polynomial,
use the matrix.

## Three traps

**Never count failures.** The container baseline is seven, by name, listed in
§7.

**Ask the running binary which commit it is.** `qsp --version` prints the commit
it was built from. An afternoon went on debugging a bridge that was not
deployed: the service was active, the deploy commands were right, and the patch
file had never reached the machine.

**Grep for the call site.** `SetIPSCSink` was written, exported, unit-tested and
never called — an edit anchored on the wrong indentation and failed silently. It
built, passed vet, staticcheck, the full suite and the race detector. §8a calls
this "declared and read by nothing" and it has now happened nine times.
