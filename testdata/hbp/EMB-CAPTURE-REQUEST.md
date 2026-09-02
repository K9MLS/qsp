# What one more capture would unlock: the EMB for every colour code

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

`internal/dmrfec.EMBFor` therefore serves colour code 11 and refuses the rest.
That refusal is deliberate: a wrong EMB produces a burst a radio **silently
drops**. Audio that goes nowhere with nothing in a log is the worst failure
available here, and much worse than an error naming the fix.

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
