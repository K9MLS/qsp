# Handover, 2026-09-06 evening

Read `NEW-SESSION.md` for the standing brief and **§8k** of `PROJECT_MEMORY.md`
for where to start, then **§8a**, which is the section that matters most. §8b
through §8j are superseded and carry banners saying so. **§0's table is worth
doubting** — it was wrong about access control for weeks because it is the
section everybody reads and nobody re-reads.

## Start here, and it needs a radio rather than a keyboard

**Send one text from the Pi-Star radio** — not the XPR8300 — with `tcpdump`
running unfiltered on the QSP server, then read the journal for `rate34_block`
on a `relaying transmission` line.

That one line settles the only part of the text path that is not measured:
whether a hotspot puts a Rate 3/4 block's serial number and CRC at the front of
the block or the back. IP Site Connect puts them at the back and ETSI figure
8.8 draws them at the front, and which one goes on air decides whether anything
QSP transmits can be read at all. `dmrfec.Rate34AirOrder` is the single
constant that follows from the answer.

**This was the first item in the last handover too, and it did not get done.**
Everything else on the list is worth less than five minutes with a radio.

## The headline

**Text messages carry their content now.** Every block of every text was
dropped in both directions — the preamble crossed, the header crossed, the
message never did. A Rate 3/4 block is eighteen octets and needs a trellis
code; QSP handled only the twelve-octet BPTC ones.

**And the codec written for that last session had all sixteen constellation
entries wrong.** It is a permutation of the four dibit values, so encode and
decode agreed with each other perfectly and every test passed. Wiring it in as
it stood would have transmitted well-formed bursts no radio could read, with a
symptom identical to the one being fixed.

The block is measured now, not assumed: sixteen octets of user data, then a
seven-bit serial and a nine-bit CRC, proved by an IPv4 header that reassembles,
serial numbers that count 0 to 5, and a CRC-9 that verifies on 42 blocks out of
42. See [ADR-0047](docs/adr/ADR-0047-rate-34-text-blocks.md).

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
