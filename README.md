# QSP

**A free, self-hosted DMR and P25 linking server for amateur radio.**

QSP is named for the Q-code meaning *"I will relay your message."*

Run it on a machine you own, point your hotspots and repeaters at it, and run
your club's network from a web browser. No dealer, no licence fee, no INI files.

## What it does

- **Hotspots and repeaters connect to it.** Pi-Star and WPSD hotspots, and
  Homebrew repeaters, log in as they would to any DMR master. Stations on the
  same talkgroup hear each other with no configuration at all.
- **Motorola repeaters connect directly**, over IP Site Connect, with nothing
  commercial in the path.
- **Talkgroups are bridged on a schedule or on demand.** Link a talkgroup for a
  net every Tuesday at 20:00, or only while somebody is keyed up. No other free
  tool does this, and it is why QSP exists.
- **Two QSP servers link** as peers, agreed from each console.
- **P25 gateways and hotspots** connect to it as a reflector.
- **Zello channels** are linked to a talkgroup through an AMBE vocoder dongle,
  bought separately. See [`docs/ZELLO.md`](docs/ZELLO.md).
- **Everything is set up from a web console**: who may connect, bridges, the
  schedule, links, accounts, the call record, backup and restore.

Not built: AllStar and EchoLink.

## What you need

- **A 64-bit Linux machine** that stays on: a PC, a server, a VPS, or a
  Raspberry Pi 3, 4 or 5 running a 64-bit operating system.
- **Docker** with the compose plugin.
- **Your DMR ID**, and the ID each hotspot registers with, shown on its
  dashboard.
- **62031/udp reachable from wherever your hotspots are.** On a home connection
  that means a port forward at your router, and it means **not being behind
  CGNAT**: if your router's WAN address starts 100.64 to 100.127, no port
  forward is possible and you need a VPS instead. This defeats more people than
  anything else. If hotspots cannot reach the port, QSP starts, looks healthy,
  and receives nothing.

## On the air in minutes

**1. Get the files and set two values.**

```sh
git clone https://github.com/K9MLS/qsp.git
cd qsp/deploy/docker
cp .env.example .env
nano .env
```

`QSP_PEER_PASSWORD` is a password you choose; your hotspots will use it.
`QSP_ALLOWED_PEERS` is the IDs allowed to connect, comma-separated. QSP will
not start a master that accepts anybody, so it asks who first.

**2. Start it.**

```sh
docker compose up -d
docker logs qsp 2>&1 | grep setup_token
```

The setup token is printed once per start. If you miss it,
`docker compose restart` prints a new one.

**3. Make your administrator account.** Open `http://<this machine>:8080` in a
browser. The console is reachable from your whole network, so keep 8080/tcp off
the internet unless you mean it to be there. The first page asks for a callsign,
a password and the setup token. From a browser on the QSP machine itself, no
token is needed.

**4. Point a hotspot at it.** In Pi-Star or WPSD, add a custom DMR master with
this machine's address, port **62031**, and your `QSP_PEER_PASSWORD`. The
hotspot appears in the console once it logs in, and stations on the same
talkgroup hear each other.

Bridges, the schedule, links and Zello are all on the console's pages.
[`deploy/docker/README.md`](deploy/docker/README.md) has the rest: what is on
and off to begin with, upgrading, where your data lives, and troubleshooting.

## Project status

**In production on its author's two servers. Not yet run by other operators
long enough to call it proven.** Issues from a first install are
exactly what is wanted.

What has been confirmed on air: Homebrew hotspots, including a live capture
committed at [`testdata/hbp/hbp-voice-live.pcap`](testdata/hbp/hbp-voice-live.pcap);
Motorola repeaters over IPSC in both directions; two QSP servers linked in both
directions; and Zello both ways, heard on hotspots and Motorola repeaters.

What has not happened yet: a third linked server, so relaying between more than
two is unit-tested and never exercised; the arm64 image run on a Raspberry Pi;
and the two-week unattended soak. A Motorola Quantar needs a V.24 interface
rather than IP, and linking one is not something QSP does.

[`docs/CAPABILITIES.md`](docs/CAPABILITIES.md) is the full list of what is
built and where the line is. [`BLUEPRINT.md`](BLUEPRINT.md) is the product
specification and [`ARCHITECTURE.md`](ARCHITECTURE.md) is how it is put
together.

## Accounts

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

## Without Docker

For a 32-bit Raspberry Pi, where Docker is no longer packaged, or if you would
rather run a plain binary. You need Go 1.27 or later:

```sh
go build ./cmd/qsp
./qsp -print-config > qsp.json   # write the effective configuration
./qsp -config qsp.json           # run with it
./qsp -check                     # validate a configuration and exit
./qsp -version
```

**Built this way, QSP starts with the DMR listener off and the console on
`127.0.0.1:8080`**, reachable only from the machine itself. That is the built-in
default, which is more cautious than the Docker install's first run.
[`docs/CONFIGURATION.md`](docs/CONFIGURATION.md) covers turning on the
listener, access control, bridges and the schedule by hand. `deploy/systemd`
holds the unit file the author runs QSP with.

## Persistence

QSP persists its configuration history, the call record and the audit trail to
SQLite. **The driver is imported by the binary rather than by the storage
package**, keeping that dependency at the edge of the program — so a build that
omits it runs without persistence and says so in the health report rather than
failing. See [`docs/adr/ADR-0005-sqlite-driver.md`](docs/adr/ADR-0005-sqlite-driver.md).

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
