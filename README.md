# QSP

**A free, self-hosted DMR and P25 linking server for amateur radio.**

QSP is named for the Q-code meaning *"I will relay your message."*

> **Status: in production on two servers, and not yet used by anybody else.**
> QSP accepts peers, repeats between them, bridges talkgroups on a schedule or
> on demand, links to other QSP servers, and is administered entirely from a
> web console.
>
> **Validated against real hardware.** A WPSD hotspot completed the login
> handshake and held its session; a live transmission reached the codec and
> decoded, with five voice streams and 556 frames, none dropped, every one of
> 576 payloads round-tripping byte-for-byte. The capture is committed at
> [`testdata/hbp/hbp-voice-live.pcap`](testdata/hbp/hbp-voice-live.pcap); see
> [`docs/architecture/hbp-protocol.md`](docs/architecture/hbp-protocol.md) for
> what those runs confirmed and what they did not.
>
> **Motorola repeaters, over IPSC.** A Motorola repeater points at QSP directly,
> with no master repeater alongside it and nothing commercial in the path. Audio
> crosses in both directions on air. See
> [ADR-0036](docs/adr/ADR-0036-ipsc-voice-is-not-a-dmr-burst.md) and
> [ADR-0043](docs/adr/ADR-0043-qsp-is-the-master.md).
>
> **The master repeats.** A group call reaches every other peer on that
> talkgroup, with no bridge and no configuration — the ordinary behaviour of a
> DMR network. Bridges are additional, and move traffic *between* talkgroups.
> See [ADR-0019](docs/adr/ADR-0019-master-repeats.md).
>
> **Linking two QSP servers** is a peer registration rather than a bridge: the
> talkgroup and the timeslot cross unchanged, and a link is offered, accepted,
> refused and readdressed from the console. Confirmed on air in both directions.
> See [ADR-0051](docs/adr/ADR-0051-a-qsp-link-is-a-peer.md) and
> [ADR-0052](docs/adr/ADR-0052-qsp-is-federated.md). OpenBridge remains for
> reaching a network QSP did not build.
>
> **What has not happened**: a third server, so relaying between more than two
> and the deduplication that goes with it are built, unit-tested and never
> exercised. No operator other than the author has run QSP. The two-week
> unattended soak has not started.
>
> **P25 over IP** is built — QSP can serve P25 gateways and hotspots as a
> reflector, carrying IMBE frames untouched exactly as it carries AMBE. Linking
> a Motorola Quantar is a different problem and is not built: a Quantar links
> over a V.24 daughtercard running HDLC rather than over IP.
>
> **Zello** is built and has carried calls both ways on a live network: a
> Zello channel is linked to a talkgroup through an AMBE vocoder dongle (tested
> with the DVstick 30, bought separately), heard
> on Homebrew hotspots and Motorola repeaters whose owners agreed. It is set up
> from the console's Zello page; see [`docs/ZELLO.md`](docs/ZELLO.md). DMR-to-DMR
> still needs no codec at all — only a bridge to a transcoder decodes audio.
>
> **Not yet built:** AllStar and EchoLink. The health endpoint reports each as
> `unavailable`.
>
> See [`docs/CAPABILITIES.md`](docs/CAPABILITIES.md) for what is built and where
> the line is, [`BLUEPRINT.md`](BLUEPRINT.md) for the product specification, and
> [`ARCHITECTURE.md`](ARCHITECTURE.md) for how this is put together.

## Why

Every tool in this space is technically capable and operationally miserable.
The commercial options are proprietary and dealer-quoted. The open alternatives
require hand-editing config files and matching port numbers between INI stanzas
whose field names disagree with each other.

The protocols are solved. The operations are not. QSP is the operations layer.

The feature that justifies its existence is **scheduled and PTT-triggered
bridging** — link a talkgroup for a net every Tuesday at 20:00, or only while
somebody is actually keyed up. No free tool does this today.

## Requirements

- Docker, or Go 1.27 and later to build from source
- Linux, macOS, or Windows for development
- Ubuntu Server 24.04 LTS is the supported deployment target

## Install

**With Docker**, which needs no toolchain and is how the author's own test
server runs:

```sh
cd deploy/docker
cp .env.example .env       # set QSP_PEER_PASSWORD and QSP_ALLOWED_PEERS
docker compose up -d
```

QSP writes its configuration on the first run and never touches that file
again — after it exists, it is yours and the console edits it. See
[ADR-0048](docs/adr/ADR-0048-container-install.md).

**From source:**

```sh
go build ./cmd/qsp
./qsp
```

The console listens on `127.0.0.1:8080` by default.

```sh
./qsp -print-config > qsp.json   # write the effective configuration
./qsp -config qsp.json           # run with it
./qsp -check                     # validate a configuration and exit
./qsp -version
```

## Endpoints

| Path | Purpose |
|---|---|
| `/` | Console |
| `/healthz` | Full health report as JSON |
| `/readyz` | Terse readiness answer for orchestrators |
| `/api/events` | Server-Sent Events stream |
| `/api/peers` | Current peer list |
| `/api/admin` | What this server is, and whether it matches its configuration |

Administrative endpoints are omitted here because they change; `SECURITY.md`
lists every one and what it does, and a test refuses to pass if that list and
the routes disagree.

## Accepting peers

The DMR listener is **off by default**. To enable it:

```sh
printf 'your-shared-password' > /etc/qsp/peer.pass
chmod 600 /etc/qsp/peer.pass
```

Then in the configuration:

```json
"dmr": {
  "enabled": true,
  "listen_address": "0.0.0.0:62031",
  "password_file": "/etc/qsp/peer.pass",
  "access": {
    "registration": {"mode": "deny", "ids": []}
  }
}
```

The password is a **file path, never a value** — configuration is versioned,
exported and diffed, and a secret in it would land in all three. See
[ADR-0012](docs/adr/ADR-0012-peer-password-file.md).

### Access control

**A listener on an address reachable from beyond this host must have an
`access` block, or QSP refuses to start.** The block above is the permissive
one: deny nobody, so everything is permitted. It exists so that permitting
everything is something an operator wrote down rather than something that
happened, and so it appears in a diff.

Four lists decide who is carried. Each has a `mode` of `permit`, which refuses
anything not named, or `deny`, which allows anything not named. An entry is an
ID or an inclusive range.

```json
"access": {
  "registration": {"mode": "permit", "ids": ["312100", "312100101"]},
  "subscribers":  {"mode": "deny",   "ids": []},
  "talkgroups": {
    "timeslot_1": {"mode": "permit", "ids": ["3100-3199"]},
    "timeslot_2": {"mode": "permit", "ids": ["9", "91"]}
  }
}
```

`registration` names repeater IDs permitted to log in — six digits for a
repeater, nine for a hotspot using an operator's ID and a two-digit suffix.
`subscribers` names radio IDs permitted to transmit, and refusing one does not
disconnect the hotspot carrying it. `talkgroups` names what is carried on each
timeslot, checked both when a frame arrives and again for each peer it would
reach, so that traffic from a bridge or a link is subject to the same list.

**QSP ships no network's talkgroup numbers.** They differ between networks, and
a list copied into this repository would be stale within the week. The lists are
yours to write. See [ADR-0020](docs/adr/ADR-0020-access-control.md).

### Bridging talkgroups

```json
"dmr": {
  "forwarding": true,
  "bridges": [{
    "name": "tuesday-net",
    "enabled": true,
    "endpoints": [
      {"peer": 3132910, "talkgroup": 3148, "timeslot": 1},
      {"peer": 0,       "talkgroup": 91,   "timeslot": 2}
    ]
  }]
}
```

`"peer": 0` means every connected peer. Traffic arriving at one endpoint is
relayed to the others with its talkgroup and timeslot translated; the
originating radio ID is preserved, and a call is never sent back to its source.

**`forwarding` is separate from `enabled` and off by default**, so you can run
QSP as a master and watch peers connect before it puts audio on a repeater.

### Scheduling a net

```json
"schedule": [{
  "bridge":   "tuesday-net",
  "days":     [2],
  "start":    "20:00",
  "duration": "1h",
  "timezone": "America/Chicago",
  "enabled":  true
}]
```

Days are 0 for Sunday through 6 for Saturday. **A bridge named by any window is
controlled entirely by the schedule** — its own `enabled` field is ignored — so
there is never a question of which setting won.

Times are local wall-clock times in the named zone, so a net at 20:00 stays at
20:00 all year. Use an IANA name such as `America/Chicago`, not an abbreviation:
`CST` cannot express "20:00 local all year".

QSP logs the next occurrence of every window at startup, and warns about any
that daylight saving will skip.

### Linking on demand

```json
"triggers": [{
  "bridge":    "on-demand",
  "on":        [{"peer": 3132910, "talkgroup": 3148, "timeslot": 1}],
  "hang_time": "3m",
  "enabled":   true
}]
```

Transmitting on a listed endpoint opens the bridge; it closes three minutes
after the last transmission. **Trigger endpoints are separate from the bridge's
endpoints**, so a local repeater can open a link outward without the wider
network opening it inward.

A bridge may be scheduled, triggered, both, or neither. Either mechanism opening
it is enough, and the frame that opens it is itself relayed — no clipped first
syllable.

Point a hotspot at QSP as a custom DMR master. The console shows connected
peers live, and `/healthz` reports the bound address and datagram counters.

## The console

Everything above can be done from a browser instead, and that is the point of
the project: access lists, bridges, the schedule, links, peer credentials, the
call record, backup and restore, and a page saying what this server is and
whether it matches its own configuration.

Administrative endpoints require a session. **The first administrator is made in
the browser**: a fresh server sends every page to a setup form, which asks for a
callsign and a password. Over a network it also asks for a one-time token that
QSP prints once at startup — there is none to type from the machine itself. See
[ADR-0056](docs/adr/ADR-0056-first-administrator-in-a-browser.md).

Every account after the first is added from the administration page. If every
administrator is lost, one can be made on the host:

```sh
qsp -config /path/to/qsp.json adduser YOURCALL
```

## Persistence

QSP persists its configuration history, the call record and the audit trail to
SQLite. **The driver is imported by the binary rather than by the storage
package**, keeping that dependency at the edge of the program — so a build that
omits it runs without persistence and says so in the health report rather than
failing. See [`docs/adr/ADR-0005-sqlite-driver.md`](docs/adr/ADR-0005-sqlite-driver.md).

## Development

```sh
go build ./...
go test ./...
go test -race ./...
go vet ./...
gofmt -l .
```

All of these must pass before a change is merged. `-race` is not optional; it
has already caught one real defect in this codebase.

## Contributing

Read [`CONTRIBUTING.md`](CONTRIBUTING.md) first. It is short and the rules in it
are load-bearing — particularly the ones about fake data and speculative
protocol work.

## Security

Report vulnerabilities privately. See [`SECURITY.md`](SECURITY.md).

## Support

Issues are welcome. There is no SLA.

**QSP is hobbyist software. It carries no warranty and must never be used in
public safety or life safety critical applications.**

## Licence

GPL-3.0. Free for every ham, forever. No paid tier.

## Credit

QSP builds on protocol work by Jonathan Naylor G4KLX, Hans Barthen DL5DI, and
Torsten Schultze DG1HT; server implementations by Cort Buffington N0MJS; P25
clients and reflectors by G4KLX and contributors; V.24/DFSI and FNE work by
DVMProject and W3AXL; and DVSwitch tooling by N4IRS and team.
