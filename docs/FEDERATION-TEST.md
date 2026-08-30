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

Nothing here touches the production instance. The consoles and the link sockets
bind `127.0.0.1`, and everything is written to `/tmp/qsp-pair`.

**The DMR listeners bind `0.0.0.0`, deliberately.** The first version of this
bound them to loopback like everything else, which is tidy and made the harness
useless: a hotspot on the LAN could not reach either instance, so the only
traffic either ever saw was the console polling itself. A pair with no way to
receive a frame cannot answer the question it exists to answer.

They are reachable from the LAN while running, which is why both configurations
carry an explicit `access` block. It permits everything, which is the deliberate
statement ADR-0020 asks for rather than an oversight — this is a development
machine, and the instances are stopped when the test ends.

| | alpha | bravo |
|---|---|---|
| Console | http://127.0.0.1:8091 | http://127.0.0.1:8092 |
| DMR listener | `0.0.0.0:62041` | `0.0.0.0:62042` |
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

**Two: a frame crosses.** Point a hotspot at this machine's LAN address on port
62041 and transmit on TG 9 TS2. It should appear in bravo's log and on bravo's
console.

A capture from `testdata/hbp` cannot simply be replayed at it: the login
handshake answers a challenge whose salt differs every time, so a recorded
session will not authenticate. Generating traffic without a radio needs a tool
that speaks the client side, which is separate work.

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
