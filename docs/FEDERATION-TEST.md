# Two instances, peered

Two QSP servers on one machine, linked to each other over OpenBridge.

**Every upstream path in this project is code that has never met a far end.**
OpenBridge was written from its specification and outbound peer mode from
ADR-0024, and both have been exercised only by tests that supply their own other
side. A test that provides both halves of a conversation proves the halves
agree with each other, which is not the same as proving either is right.

This is the cheapest way to find out. It is not a soak, and it is not the air
test — it answers one question those cannot: does a frame leave one instance and
arrive at the other, over a real socket, authenticated by a real passphrase.

## Running it

On the development machine:

```sh
./scripts/pair.sh          # build and start both
./scripts/pair.sh stop     # stop them
./scripts/pair.sh clean    # stop them and remove /tmp/qsp-pair
```

Nothing here touches the production instance. Both processes bind `127.0.0.1`
only, on ports nothing else in this project uses, and write to `/tmp/qsp-pair`.

| | alpha | bravo |
|---|---|---|
| Console | http://127.0.0.1:8091 | http://127.0.0.1:8092 |
| DMR listener | `127.0.0.1:62041` | `127.0.0.1:62042` |
| Link listener | `127.0.0.1:62045` | `127.0.0.1:62046` |
| Network ID | 3132910 | 3199001 |

The two configurations only make sense read together: OpenBridge has no
connection establishment, so each sends to an address agreed in advance rather
than one discovered from the other's packets. `TestThePairFacesItself` checks
they still face each other, because a mismatched port pair produces a link that
reports itself healthy while carrying nothing in one direction.

## What to watch, in order

**One: the link comes up at all.** Both instances should report the `pair` link
healthy. Nothing has ever done this against a real far end, so a failure here is
the most valuable result this harness can produce.

```sh
curl -s http://127.0.0.1:8091/healthz
curl -s http://127.0.0.1:8092/healthz
```

**Two: a frame crosses.** Point a hotspot at `127.0.0.1:62041`, or replay a
capture from `testdata/hbp` into it, and transmit on TG 9 TS2. It should appear
in bravo's log and on bravo's console.

**Three: it does not come back.** The pair exports and imports the same
talkgroup deliberately — the ordinary club configuration, and the one that would
loop. `routing.Core.route` refuses to send a frame that arrived on a link to any
link, so alpha's frame should reach bravo and stop. Bravo's log should record a
drop naming the reason rather than a delivery.

**This is the assertion the harness exists for.** The rule is tested in
`internal/routing`, and it has never been tested with a second process on the
other end of a socket.

## What it does not prove

- **That audio is intelligible.** Frames crossing is not the same as a radio
  opening its squelch on the far side. That is the air test.
- **That it works across the internet.** Both ends are on loopback: no NAT, no
  MTU limit, no jitter, no packet loss. A link that works here can still fail
  between two clubs.
- **Anything about outbound peer mode.** This pair uses OpenBridge. Homebrew is
  a separate link type and needs its own run.

## Before the first real peering

Take one instance off this machine. The failure modes that matter between two
clubs — NAT, an address that changes, a far end that restarts — are exactly the
ones loopback cannot produce, and this project has already spent a day on a
hotspot losing its session behind a rebinding router (ADR-0011).
