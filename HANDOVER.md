# Handover, 2026-09-05

Read `NEW-SESSION.md` for the standing brief and **§8j** of `PROJECT_MEMORY.md`
for where to start, then **§8a**, which is the section that matters most. §8b
through §8i are superseded and carry banners saying so. **§0's table is worth
doubting** — it was wrong about access control for weeks because it is the
section everybody reads and nobody re-reads.

## The headline

**A whole message type had never been accepted, and a call record never ended.**

`0x81` is a private voice call. QSP had refused it since the IPSC listener was
written, so **no private call from a Motorola repeater had ever crossed the
bridge**. It is `0x80` with a radio ID where the talkgroup goes, and nothing
else about the frame changes — proved by two private calls in opposite
directions between the same two radios, with group calls either side, so source
and destination move in opposite ways and neither can be confused for the other.
Byte 38 is the DMR Full Link Control opcode and agrees with the leading byte on
all 32 header and terminator frames in the capture.

Separately, the console reported a transmission **running for 7h14m18s** while
the radios were silent. An IPSC call ended on its last-frame flag and on nothing
else, and a peer that keeps keepaliving is never dropped, so a transmission
whose terminator never arrived stayed open for as long as the repeater stayed
up.

## How both were found

The first by reading a journal full of `unrecognised datagram` and noticing the
lengths were voice's. The second by an operator looking at a dashboard and
saying *I hear nothing on the radios*.

**Neither could have been found any other way**, because in both cases the code
was doing exactly what it was written to do.

## Also done

- **Three counts per transmission** — received, converted, delivered — because
  the console said 45 frames and the DMR side said 22 for the same stream in the
  same second and nothing said where the rest went. Conversion is ruled out by
  measurement; the gap is now instrumented rather than argued about.
- **One `call started` per run of data bursts** instead of seventeen, matching
  the rule the history has used since the text work.
- **A finished data burst is no longer reported as an abandoned transmission.**
  Four false warnings per text message. That warning is how a peer that lost
  power mid-over is noticed.

## Do this first

**Private calls on air.** 0.1.67 is deployed and no radio has tested `0x81`.
KD9EJA's private call to K9MLS should log `"private":true` and a `relaying
transmission` line. **Whether the far end rings is a separate question**, and it
is the same one Paul's private calls have been posing for a week.

## Decide this

**Which source owns Last-heard.** `DeliverFromIPSC` already observes into the
shared tracker, and `/api/peers` appends the IPSC listener's own call views on
top with no dedup, so every Motorola over should be appearing twice. This is a
choice about what members see, not a defect to fix quietly. §8j item 2.

## The method

**Every reading taken by eye has been wrong. Every differential has been right**,
now twelve times. On 2026-09-05 an eye reading put 220 vocoder frames in a
capture that holds 228; the test caught it before the patch shipped.

## Traps

**A test that asserts an absence can arrange for it.** The routing-reaper test
passed with the fix removed, because with no peers ready the burst reserved
nothing and there was nothing to warn about. Fourth time this has happened.
Break the code and watch the test fail, or it is not a test.

**Never count test failures.** The container baseline is seven, by name, in §7.

**staticcheck cannot run in the container.** Expect a patch to fail as a gate
chain that stops before the tests. Never begin a comment line with `go:`,
`line:`, `export:` or `extern:`.

**Ask the running binary which commit it is.** `qsp --version`. `systemctl
is-active` says something started; only the version says what.

**Never use `git checkout` to undo a deliberate break**; it reverts the whole
uncommitted file. Copy the file first.
