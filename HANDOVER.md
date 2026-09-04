# Handover, 2026-09-04

Read `NEW-SESSION.md` for the standing brief and **§8i** of `PROJECT_MEMORY.md`
for where to start. §8b through §8h are superseded and carry banners saying so;
**§0's table is worth doubting** — it was wrong about access control for weeks
because it is the section everybody reads and nobody re-reads.

## The headline

**One whole timeslot of audio had never crossed the bridge.**

Byte 30 of an IPSC voice frame carries the timeslot in its high bit: `0x8a` on
one slot, `0x0a` on the other. `FrameVoice` was recorded as `0x8a` from captures
that were all on one timeslot, so every voice frame on the other slot was
refused and the converter produced nothing. Broken since the IPSC listener was
written, hidden because this network runs on TG 2 timeslot 2, and **102 of the
refused frames were sitting in `ipsc-slot-tg.pcap` the whole time**.

Fixed in 0220. TG 11 on timeslot 1 now works on air, both directions.

## How it was found, which matters more than the fix

The operator keyed up on an untried timeslot and read the journal. The IPSC
listener logged `call started`; the DMR side logged nothing. **That gap was the
entire diagnosis** — `AsVoice` reads the flags and succeeds, `Payload` reads the
marker and refuses.

Three bug hunts, four captures and a green test suite found nothing. Running the
system found it in one key-up.

## Also done

- **Text messages, both directions** ([ADR-0045](docs/adr/ADR-0045-ipsc-text-messages.md)).
  Hotspot to repeater works on air. Repeater to hotspot does not, and QSP is
  verified not to be the reason — the remaining hops are MMDVMHost and the radio.
- **Access control covers IPSC** ([ADR-0044](docs/adr/ADR-0044-access-control-covers-ipsc.md)),
  with no new configuration. A banned radio was banned on hotspots and carried
  by repeaters.
- **QSP is the master, always** ([ADR-0043](docs/adr/ADR-0043-qsp-is-the-master.md)).
- **Motorola repeaters on the dashboard**, Traffic cut from ten metrics to four,
  member passwords cut from 43 characters to ten, and `VERSION` is read by
  something at last.
- **A routing refusal is logged at info**, once per destination and reason. It
  was at debug while production runs at info, so a refusal was countable and
  never explainable. That cost an evening.

## Do this first

**Settle repeater-to-hotspot text.** Everything QSP sends is verified correct, so
the answer is in `/var/log/pi-star/MMDVM-*.log` on the Pi-Star. If Motorola's
TMS simply cannot reach an MMDVM radio, that is worth knowing and recording
rather than chasing.

## The method

**Every significant defect has been found by running the system** — four for
four on 2026-09-04. Two diagnostics did the work: *log the same fact at two
layers and read the gap*, and *read Last-heard first when two stations cannot
hear each other*.

**Every reading taken by eye has been wrong. Every differential has been right**,
now eleven times.

## Traps

**Never count test failures.** The container baseline is seven, by name, in §7.
A pipeline ending in `-c`, `wc -l` or `/dev/null` breaks the rule while obeying
its letter, and a run that reports nothing is not a run that passed.

**staticcheck cannot run in the container.** Expect a patch to fail as a gate
chain that stops before the tests, rather than as a test that fails. Never begin
a comment line with `go:`, `line:`, `export:` or `extern:`.

**Ask the running binary which commit it is.** `qsp --version` reports the
release and the commit.

**Never use `git checkout` to undo a deliberate break**; it reverts the whole
uncommitted file. Copy the file first.
