# ADR-0015: The scheduler is level-triggered, and stores wall time

**Status:** Accepted

## Context

QSP exists to link a talkgroup for a net every Tuesday at 20:00 without anyone
remembering to do it. Two decisions determine whether that works, and both have
an obvious answer that is wrong.

## Decision 1 — level-triggered, not edge-triggered

The obvious design fires events at boundaries: at 20:00 enable the bridge, at
21:00 disable it. It then has to answer a list of awkward questions. What if QSP
restarted at 20:30 and the 20:00 event never fired? What if the process was
paused past a boundary? What if NTP stepped the clock over one?

Each has an answer, and each answer is a mechanism that can fail on its own.

**The scheduler instead answers: given an instant, which bridges should be
enabled?** It keeps no memory of past evaluations. The answer for 20:30 is
identical whether QSP has been running a week or four seconds.

Restart recovery, missed ticks, clock steps and NTP corrections stop being
special cases and stop needing code. A net in progress when QSP restarts is
picked up on the first sweep, because nothing was ever "in progress" as far as
the scheduler is concerned.

## Decision 2 — store local wall time, never an instant

A window stores a weekday, a local time of day, and an IANA zone. It resolves to
an instant only at evaluation.

Storing the UTC instant would be simpler and would be wrong. Measured on
`America/Chicago`: a net at 20:00 local is 02:00 UTC in winter and 01:00 UTC in
summer. A stored instant drags the net an hour off twice a year, in opposite
directions.

The zone must be an IANA name, not an offset. An offset cannot express "20:00
local all year", which is the only thing an operator ever means.

## What the empirical work found

Three behaviours were measured rather than assumed, and two changed the design.

**A nonexistent local time resolves backwards.** On a spring-forward day,
`time.Date(..., 2, 30, ...)` in `America/Chicago` returns **01:30**, an hour
*earlier* than asked for. A net configured for 02:30 would fire an hour early,
once a year, in a way almost impossible for an operator to diagnose.

QSP therefore **skips that occurrence** and labels it, rather than running it at
the wrong time. Firing early is worse than not firing: not firing is visible.

**A repeated local time resolves to its first occurrence.** On a fall-back day,
01:30 happens twice; Go returns the first. That is deterministic and matches
what an operator expects — the net starts the first time the clock reads 20:00 —
so it is kept. The window then runs its configured duration in *real* time, so
it does not re-open during the second pass.

**A local day is 23 or 25 hours long.** Candidate days are therefore walked with
calendar arithmetic (`AddDate`), never by adding or subtracting 24 hours.

## Supporting decisions

- **`MaxWindowDuration` is 12 hours.** Constitution §17 requires a failsafe. The
  risk is a typo — hours where minutes were meant — welding a talkgroup open for
  days.
- **`Preview` resolves real instants and flags anomalies**, so an operator sees a
  skipped DST occurrence before relying on a net that will not run. §17 requires
  showing what will actually happen before saving.
- **The IANA database is embedded** via `time/tzdata`. A scratch container has no
  `/usr/share/zoneinfo`, so without it every schedule would fail to load on the
  deployment target and nowhere else. It costs about 450 KB.
- **A bridge named by any window is controlled entirely by the schedule**; its
  own `enabled` field is ignored. A bridge with no window uses that field. One
  mechanism decides each bridge, so an operator never has to work out which
  setting won.

## Consequences

- The whole scheduler is a pure function of a schedule and an instant, so DST
  transitions, midnight crossings and clock jumps are all ordinary table tests
  against real 2026 transition dates.
- The routing table is rebuilt only when the answer changes, so a quiet
  scheduler costs one map comparison per second.
- **Not implemented:** manual override. An operator cannot currently force a
  scheduled bridge open or closed outside its window. That is a real gap for Net
  Control and is deliberately deferred rather than half-built.
