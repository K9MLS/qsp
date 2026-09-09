# QSP

**A free, self-hosted DMR and P25 linking server for amateur radio.**

QSP is named for the Q-code meaning *"I will relay your message."*

> **Status: v1.0 feature-complete for DMR.** QSP accepts peers, relays audio
> between bridged talkgroups, and opens those bridges on a schedule or on
> demand when somebody keys up. Forwarding is off by default.
>
> **Validated against real hardware.** On 2026-08-23 a WPSD hotspot
> (MMDVMHost + DMRGateway) completed the login handshake and held its session.
> On 2026-08-25 a live transmission reached the codec and decoded: five voice
> streams, 556 frames, none dropped, every one of 576 payloads round-tripping
> byte-for-byte. Frame rates landed within 1.5 % of DMR's 16.67/s across
> durations from 3.8 s to 14.6 s. The capture is committed at
> [`testdata/hbp/hbp-voice-live.pcap`](testdata/hbp/hbp-voice-live.pcap); see
> [`docs/architecture/hbp-protocol.md`](docs/architecture/hbp-protocol.md) for
> what those runs confirmed and what they did not.
>
> **The master repeats.** A group call on a talkgroup reaches every other peer
> on that talkgroup, with no bridge and no configuration — the ordinary
> behaviour of a DMR network. Bridges are additional, and move traffic between
> talkgroups. This was built on 2026-08-27; before that, QSP had bridging and
> no repeat. See [ADR-0019](docs/adr/ADR-0019-master-repeats.md).
>
> **No access control yet.** Every connected peer receives every talkgroup any
> peer transmits on. That suits a club whose members know each other and does
> not suit an instance facing the internet.
>
> **Linking to other networks** is implemented over OpenBridge, which is what
> BrandMeister requires for interconnecting a network. No link has yet run
> against a real far end — that needs a bridge granted by the network being
> joined.
>
> **Relay is tested; two radios are not.** Audio crossing between bridged
> talkgroups is verified over real sockets, both with constructed frames
> (`internal/peers/forward_test.go`) and by replaying a captured transmission
> from a hotspot (`internal/peers/fanout_test.go`), and the fan-out holds at a
> hundred peers. What has never happened is two *physical* hotspots connected to
> one instance — the gap is hardware, not code.
>
> **Not yet run unattended.** The scheduler and PTT triggers are built and
> tested, but the two-week soak that BLUEPRINT-v1 requires has not started.
>
> **Not yet built:** P25 and the analog connectors
> (AllStar, Zello, EchoLink). The health endpoint reports each as
> `unavailable`.
> See [`BLUEPRINT.md`](BLUEPRINT.md) for the product specification and
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

- Go 1.22 or later
- Linux, macOS, or Windows for development
- Ubuntu Server 24.04 LTS is the supported deployment target

## Build and run

```sh
go build ./cmd/qsp
./qsp
```

The console listens on `127.0.0.1:8080` by default.

```sh
./qsp -print-config > qsp.json   # write the effective configuration
./qsp -config qsp.json           # run with it
./qsp -version
```

## Endpoints

| Path | Purpose |
|---|---|
| `/` | Console |
| `/healthz` | Full health report as JSON |
| `/readyz` | Terse readiness answer for orchestrators |
| `/api/events` | Server-Sent Events stream |
| `/api/peers` | Current peer list (read-only) |

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

Every endpoint is **read-only**. QSP exposes nothing that changes state until
authorisation is designed.

## Persistence

**This build has no SQL driver registered**, so it runs without persistence and
says so in the health report. This is deliberate: the driver is imported by the
binary rather than by the storage package, keeping that dependency at the edge
of the program. See [`docs/adr/ADR-0005-sqlite-driver.md`](docs/adr/ADR-0005-sqlite-driver.md).

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
