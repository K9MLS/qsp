# Handover, 2026-09-06

Read `NEW-SESSION.md` for the standing brief and **§8j** of `PROJECT_MEMORY.md`
for where to start, then **§8a**, which is the section that matters most. §8b
through §8i are superseded and carry banners saying so. **§0's table is worth
doubting** — it was wrong about access control for weeks because it is the
section everybody reads and nobody re-reads.

## Start here

**Wire the Rate 3/4 Trellis codec into the text path.** `internal/dmrfec` gained
`trellis.go` and nothing calls it. `Encoder.text` and `Converter.ConvertText`
still handle only BPTC, so every data block of every text is still dropped.

That is the whole of what stands between this network and working text
messages, and the diagnosis is finished: see §8j.

## The headline

**Private text has never worked because a codec was missing**, and the codec now
exists. Every data block of a text is refused: the preamble crosses the bridge,
the header crosses, and the content does not, so a radio at the far end sees a
header promising blocks that never arrive.

ADR-0045 wrote the gap down when the text work was done — *Rate 3/4 bursts are
refused rather than truncated* — and nobody connected it to the symptom for
eighteen patches. It explains every report: KD9EJA's texts arrive, K9MLS's never
do, neither radio acknowledges, and group text on the local repeater is fine.

**Private calls now work in both directions**, proved on air and in a capture,
which also settles the inferred half of ADR-0046.

## Do this when you next have a radio, it takes five minutes

**Send one text from the Pi-Star radio** — not the XPR8300 — with `tcpdump`
running unfiltered. That puts MMDVMHost in the path, which produces Rate 3/4
**coded** bursts. No capture anywhere contains one: the Homebrew fixtures are
voice only, and the IPSC captures carry decoded blocks.

Decoding one into the message that was typed proves the trellis tables, which
are currently transcribed from the standard and guarded only by shape tests.

## The method

**Every reading taken by eye has been wrong. Every differential has been right**,
now thirteen times. Six defects on 2026-09-06 were found by reading captures
byte by byte after a regression test that sounded perfect; three of them were in
files open on the screen at the time.

## Traps

**A change made late in a session gets the same confidence as one made early,
and should not.** Four patches written on the evening of 2026-09-06 were
corrected the same evening, one of them reintroducing within the hour a defect
fixed earlier the same afternoon.

**A test that asserts an absence can arrange for the absence.** Three did that
evening. Break the code and watch the test fail, or it is not a test.

**Never count test failures.** The container baseline is seven, by name, in §7.

**staticcheck cannot run in the container.** Expect a patch to fail as a gate
chain that stops before the tests.

**Ask the running binary which commit it is.** `qsp --version`. `systemctl
is-active` says something started; only the version says what.

**A failed `git am` leaves its rebase directory behind** and the next one fails
with "previous rebase directory still exists". `git am --abort` first, then
`git log --oneline -3` before assuming anything about what is applied. This
happened four times on 2026-09-06, every time because a patch was already
applied and the operator ran the block anyway.

**Never use `git checkout` to undo a deliberate break**; it reverts the whole
uncommitted file. Copy the file first.
