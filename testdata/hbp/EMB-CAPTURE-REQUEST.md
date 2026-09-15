# What one more capture would unlock: the EMB for every colour code

**Partly answered on 2026-09-15.** A second repeater transmitting at **colour
code 4** was captured, and `EMBFor` reproduced all four of its EMBs on the
first attempt — see `hbp-emb-colourcode-4.md`. The colour code axis is
therefore observed rather than predicted, and the same capture also promoted
the embedded Link Control carriage from a reading of annex B to a recording.

**What remains is completeness, not confidence.** 11 is `1011` and 4 is
`0100`: two vectors where spanning four bits needs four, so colour codes 1, 2
and 8 would finish the job. The method below is unchanged and still takes two
minutes.

---

**Two minutes at a radio, and it completes the last unknown in the audio path
between Motorola and this network.**

## What is known

The 48 bits in the middle of a DMR voice burst are, on every burst but the first
of a superframe, an 8-bit EMB, a 32-bit embedded Link Control fragment, and
another 8-bit EMB. IP Site Connect sends the fragment and omits the EMB, because
a Motorola repeater knows its own colour code and rebuilds it. QSP has to supply
one.

The EMB is seven information bits — colour code (4), a pre-emption flag (1) and
LCSS (2) — under nine parity bits. **The parity is provably linear**:
`parity(a^b) == parity(a)^parity(b)` holds exactly across every pair in
`hbp-voice-session.pcap`.

## What is not known, and why

**Every burst in every capture this project holds carries colour code 11.**

So the four observed values differ only in their two LCSS bits, and only the
contribution of those two bits can be derived. Nothing has ever moved the colour
code bits, so nothing about them can be honestly inferred.

The `EMBFor` function in [`internal/dmrfec`](../../internal/dmrfec) serves
every colour code from the recovered generator, and refuses only values above
15. **An earlier version of this document said it served 11 and refused the
rest, which was never true of the code** — the refusal described here was
considered and not taken, because a bridge serving one colour code would be
useless. The stakes named below are why the prediction was worth corroborating
rather than assuming: a wrong EMB produces a burst a radio **silently drops**,
and audio that goes nowhere with nothing in a log is the worst failure
available here.

## The capture

The same differential that has settled every other question in this project:
change exactly one thing and record the result.

**On the hotspot**, change the colour code, key up for five seconds, change it
back. Repeat for a handful of values. Capture throughout:

```sh
sudo tcpdump -i ens192 -n -s0 -w ~/emb-cc.pcap host 192.168.1.247 and udp port 62031
```

Note which colour code was set for each transmission — that is what makes it a
differential rather than a pile of bytes.

**Four values are enough** to determine the whole code, because the parity is
linear and the colour code is four bits. Any four whose binary representations
are linearly independent will do; 1, 2, 4 and 8 are the obvious choice, and
together with the colour code 11 already captured they span the field.

The pre-emption bit is 0 in everything captured and is expected to stay 0 on an
unencrypted network. If a capture ever shows it set, the same method applies.

## What comes back

The `.pcap`, and **a note of which colour code was set for each transmission**.
The bytes alone cannot say, and a fixture that records an inference as an
observation is the thing this project takes most care to avoid.
