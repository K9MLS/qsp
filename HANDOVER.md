# Handover, 2026-09-03

Read `NEW-SESSION.md` for the standing brief and **§8g** of `PROJECT_MEMORY.md`
for where to start.

## The headline

**IPSC layer 1 is finished.** Audio both directions, the frame shape verified
byte for byte against a real Motorola master, colour code mirroring proved on
air with two codes at once, access control on all three checks, repeaters
manageable from the console, and parrot working on a repeater.

## Previously

**A Pi-Star and a Motorola repeater held a conversation.** The bridge carries
audio both ways, on air. Layer 1 of §0's table is complete for both protocols,
and IPSC is no longer one-way.

## What changed

- **The outbound frame shape was wrong and is now measured**
  ([ADR-0042](docs/adr/ADR-0042-the-outbound-frame-shape-is-measured.md)). QSP
  sent 33-byte headers where a repeater sends 54, and 66 bytes for every voice
  frame where a repeater cycles 52 57 57 57 66 57. Fixed and proved against
  fixtures already held — 326 voice frames, 93 headers and terminators, two
  repeater models, no equipment.
- **A header reproduces bit-exact from its Link Control alone.** Bytes 38 to 49
  are the twelve-octet block BPTC carries on the air, masked `0x969696` for a
  header and `0x999999` for a terminator. Five captured frames from two models
  on two talkgroups agree. The Reed-Solomon work under ADR-0040 supplied it
  unchanged.
- **A repeater is signed with its own colour code, not the network's**, learned
  from the frames it sends. `ipsc-two-peers.pcap` has two repeaters keyed at
  once on colour codes 1 and 4, so one number for the whole network is the one
  choice that cannot be right.
- **`PROJECT_MEMORY.md` held six copies of §8f in five versions.** Collapsed by
  content hash; every unique fact folded into §8g. See §8g's closing note for
  what the duplication actually cost.

## Do this first

**Put IPSC repeaters on the dashboard.** §8h has the design and the two
decisions inside it. The listener already holds every field the panel renders
and nothing reads them — the ninth-and-tenth pattern in a different dress.

Then **confirm the learned colour code on air**: KD9EJA's repeater may share
`ipsc.colour_code`, in which case the mirroring is untested and the right
answer arrived for the wrong reason.

## The method

**Every reading taken by eye was wrong. Every differential was right** — now
ten times. The tenth was `body[20]`, recorded as a frame marker and in fact the
low byte of the timestamp: a field invented out of a sampling window one
superframe wide.

## Three traps

**Never count failures.** The container baseline is seven, by name, in §7.

**Ask the running binary which commit it is.** `qsp --version`.

**Read the newest section, and check there is only one of it.** A session read
the first copy of §8f, treated it as current, and drew two conclusions that
were already correctly recorded further down the same file.
