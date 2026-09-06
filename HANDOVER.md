# Handover, 2026-09-06 night

Read `NEW-SESSION.md` for the standing brief and **§8k** of `PROJECT_MEMORY.md`
for where to start, then **§8a**, which is the section that matters most. §8b
through §8j are superseded and carry banners saying so. **§0's table is worth
doubting** — it was wrong about access control for weeks because it is the
section everybody reads and nobody re-reads.

## Start here, and it needs a radio rather than a keyboard

**Send a text both ways — repeater to hotspot and hotspot to repeater — and say
whether it appeared on the screen.**

Everything else about the text path is measured now. Fifty-four real bursts
from a hotspot decode with these tables and none decode with the ones that
shipped in 0242; QSP's encoder reproduces those bursts byte-for-byte; QSP put
twelve Rate 3/4 datagrams on the wire in production where it had put none.
**What nobody has confirmed is that a handheld displays the result**, and no
capture can answer that.

If it does not display, the next thing to look at is item 2 below rather than
the codec.

## The headline

**Text messages carry their content now**, and the whole path is measured
against traffic from somebody else's equipment. Every block of every text used
to be dropped in both directions — the preamble crossed, the header crossed,
the message never did.

**The codec written the session before had all sixteen constellation entries
wrong**, and 54 real bursts prove it: 54 of 54 decode with the corrected
tables, 0 of 54 with the old ones. It is a permutation of the four dibit
values, so encode and decode agreed with each other perfectly and every test
passed. Wiring it in as it stood would have transmitted well-formed bursts no
radio could read, with a symptom identical to the one being fixed.

See [ADR-0047](docs/adr/ADR-0047-rate-34-text-blocks.md).

## The method

**Every reading taken by eye has been wrong. Every differential has been
right**, now fourteen times. Two more this session, both caught by a test
rather than by review.

And one worth adding: **a constant that round-trips is not a constant that is
correct.** A round trip through your own tables proves the wiring and nothing
else. The tests that carry weight end at a fact outside this repository — an
IPv4 header, a CRC over somebody else's bytes, a sentence you typed.

## Traps

**A measurement filed as an exception is a defect you have already found.**
ADR-0045 wrote down that a documented offset failed on exactly the nine Rate
3/4 frames in its capture, three days before anybody worked out that this was
why no text had ever arrived.

**A file that documents why it cannot be trusted has not been checked.**
`trellis.go` said in its own header that its tables were checked only by the
package agreeing with itself. A session read that and wrote a handover saying
"wire the codec in".

**A test that asserts an absence can arrange for the absence.** Still true.
`TestARateThreeQuarterBurstIsRefusedRatherThanTruncated` went further: it
asserted the wrong thing entirely and passed for eighteen patches while the
network could not send a text.

**A test written from what you expect a capture to contain is the same defect
as a constant read off the hex by eye.** The outbound serials were asserted to
run 0, 1, 2, 3 three times over because the message was sent three times. They
run 0, 1, 2, 3 and then the last block eight more times.

**Read the capture when it arrives.** The one that settled the whole trellis
question sat on the server for eight hours while a stale binary was chased, and
was asked for three more times after it already existed.

**Never count test failures.** The container baseline is seven, by name, in §7.

**staticcheck does run in the container** — §7 and the last handover both say
it cannot. Built through the full 1.22 → 1.23 → 1.24.6 → 1.27 chain it runs
clean over the whole tree in about a minute.

**Ask the running binary which commit it is.** `qsp --version`. `systemctl
is-active` says something started; only the version says what.

**A failed `git am` leaves its rebase directory behind.** `git am --abort`
first, then `git log --oneline -3` before assuming anything about what is
applied.

**Never use `git checkout` to undo a deliberate break**; it reverts the whole
uncommitted file. Copy the file first.
