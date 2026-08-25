# ADR-0016: PTT-triggered bridging, and how it merges with the schedule

**Status:** Accepted

## Context

The blueprint names **scheduled and PTT-triggered bridging** as the feature that
justifies QSP's existence — the thing no free tool in this space does. The
scheduler covered half of it. This is the other half.

A scheduled bridge is open because the calendar says so. A triggered bridge is
open because somebody is using it, and closes itself when they stop. Clubs want
both, and often for the same bridge: a net at a fixed time, plus an on-demand
link the rest of the week.

## Decision

`routing.Triggers` opens a bridge when a transmission arrives at one of its
declared endpoints, and closes it after a hang time.

**Trigger endpoints are separate from bridge endpoints.** A club can let their
local repeater open a link outward without letting the wider network open it
inward — the asymmetry an operator actually wants, and impossible to express if
the trigger were simply "any endpoint of this bridge".

**Either mechanism opening a bridge is enough.** `bridgeState` merges the
schedule and the triggers with a logical OR. A net that starts early because
somebody keyed up is what an operator wants; so is a triggered link staying open
through a scheduled window. Neither mechanism can close a bridge the other has
opened.

A bridge controlled by *either* mechanism ignores its own `enabled` field. A
bridge controlled by neither uses it. So exactly one thing decides each bridge
and there is never a question of which setting won.

## The subtlety worth recording

**The frame that opens a bridge must itself be relayed.**

The obvious implementation observes the transmission, opens the bridge, and lets
the *next* sweep apply it — by which time the first frame is gone. That clips the
first syllable of every on-demand transmission, which is precisely the complaint
operators make about systems that get this wrong.

`Listener.trigger` therefore runs before `Listener.forward` and applies the new
table immediately rather than waiting for the sweep.
`TestPTTOpensTheBridgeAndCarriesTheOpeningFrame` asserts that the relayed frame
is sequence 0.

## Hang time

**Default three minutes.** It has to outlast the pauses in a conversation: a
bridge that closed the instant somebody unkeyed would drop the reply.

**Maximum thirty minutes**, for the same reason the scheduler caps window
duration. A mistyped hang time is a talkgroup welded open with nobody watching,
and that failure is worth a hard limit rather than a warning.

`Expire` forgets closed bridges as well as reporting them. Without that,
`lastUsed` grows for every bridge ever triggered and a bridge removed from the
configuration keeps an entry forever.

## Consequences

- Triggers are level-triggered like the scheduler: asked what should be open now
  rather than remembering what they opened. The only state kept is the last-used
  time each hang timer requires.
- `OpenFor` reports the remaining hang time, so the console can show a countdown
  rather than a bare open/closed flag.
- **Not implemented:** a manual override for Net Control to force a bridge open
  or closed outside both mechanisms. Still a real gap, and still deferred rather
  than half-built.
