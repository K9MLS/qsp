# Running QSP in a container

**Before anything here, read the next section.** The container is the easy
part.

## What has to be true first

QSP is a DMR master. Hotspots and repeaters connect *to* it over UDP, so a
port on this machine has to be reachable from wherever they are.

- **62031/udp forwarded** to this machine at your router, if it is on a home
  connection.
- **Not behind CGNAT.** Some internet providers put whole neighbourhoods behind
  one address and no port forward is possible. If your router's WAN address
  starts 100.64–100.127, that is what is happening, and you need a VPS or your
  provider's cooperation.
- **8080/tcp** for the console, which you may prefer to keep on your own
  network.

**No container fixes this**, and it defeats more people than any configuration
file. If hotspots cannot reach the port, QSP will start, look healthy, and
receive nothing.

## Install

```sh
cp .env.example .env
nano .env
docker compose up -d
docker compose logs -f
```

`.env` needs two things:

```
QSP_PEER_PASSWORD=choose-something-long
QSP_ALLOWED_PEERS=3132910,3155413
```

The password is what you put into Pi-Star or WPSD as the master's password.
The IDs are your own hotspots and repeaters, from <https://radioid.net>.

**QSP will not start an open master for you.** A listener reachable from the
internet that accepts anybody is a problem for the people it relays to as much
as for you, so it asks who may connect before it starts listening. Add IDs to
`dmr.access` later to admit more.

Both values are read **only on the first run**. After that
`/var/lib/qsp/qsp.json` is yours, and nothing here overwrites it.

## Point a hotspot at it

In Pi-Star or WPSD, add a DMR master:

| | |
|---|---|
| Address | this machine's address |
| Port | 62031 |
| Password | your `QSP_PEER_PASSWORD` |

Then open `http://<this machine>:8080`. A peer that has connected but not
transmitted shows "not heard yet" for its colour code and talkgroups — those
are learned from traffic, and that is the peer working rather than failing.

## What is on and off to begin with

| | |
|---|---|
| Homebrew (hotspots) | **on**, 62031/udp |
| Console | **on**, 8080/tcp, every interface |
| IP Site Connect (Motorola) | **off** — turn on in `qsp.json` when you have a repeater |
| Talkgroups | all carried |
| Forwarding to other networks | **off** |

## Upgrading

The image tag is pinned rather than `latest`, so an upgrade is a decision you
make:

```sh
nano docker-compose.yml     # change the tag
docker compose pull && docker compose up -d
docker compose exec qsp /qsp -version
```

**Check the version.** `docker compose ps` says a container started; only the
version says what.

## Your data

Everything is in the `qsp-data` volume: the configuration, the peer password
and the call history.

```sh
docker compose down          # keeps it
docker compose down -v       # deletes it, including your access list
```

To back up:

```sh
docker compose exec qsp /qsp -print-config > qsp-backup.json
```

## Troubleshooting

**Nothing connects.** Almost always the port. From another machine:
`nc -zvu <address> 62031`. Then check `QSP_PEER_PASSWORD` matches what the
hotspot sends, and that the hotspot's DMR ID is in `dmr.access.registration`.

**The console shows peers but no voice frames.** The peers are keepaliving and
nobody has transmitted, or the sending side is not routing a talkgroup here.
The traffic panel says which.

**It refuses to start.** Read the message; it names the file and the setting.
`docker compose exec qsp /qsp -config /var/lib/qsp/qsp.json -check` validates
the configuration without starting anything.

## Building it yourself

Uncomment the `build:` block in `docker-compose.yml` and:

```sh
docker compose build --build-arg VERSION=$(cat ../../VERSION)
docker compose up -d
```

## The other way to run it

`deploy/systemd` is how the author runs QSP: a static binary and a unit file,
no container. It is supported but this is the documented path, because two sets
of instructions means two sets to keep true — and the files this replaced had
never been run at all.
