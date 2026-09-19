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
- **A 64-bit machine**: a PC or server (amd64), or an ARM board such as a
  Raspberry Pi 3, 4 or 5 running a **64-bit** operating system (arm64). The
  ARM image is built but has not yet been run on a Pi. There is no 32-bit
  image: Docker no longer packages 32-bit Raspberry Pi OS. The plain binary
  install in the main README runs there instead.

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
QSP_ALLOWED_PEERS=3132910
```

The password is what you put into Pi-Star or WPSD as the master's password.

The IDs are the ones your hotspots register with, shown on their dashboards —
usually your own seven-digit ID. A Motorola repeater uses six digits.

QSP may print this at startup:

```
access list advisory  dmr.access.registration names 3132910, a seven-digit ID
```

**That is a prompt to check, not an error.** Some hotspots append a two-digit
suffix to the operator's ID and some do not; a plain seven-digit ID is
ordinary and registers perfectly well. Note also that the subscriber field
holds a *radio's* ID, which is 24 bits — a nine-digit value will be refused
there.

**QSP will not start an open master for you.** A listener reachable from the
internet that accepts anybody is a problem for the people it relays to as much
as for you, so it asks who may connect before it starts listening. Add IDs to
`dmr.access` later to admit more.

Both values are read **only on the first run**. After that
`/var/lib/qsp/qsp.json` is yours, and nothing here overwrites it.

## Create your administrator account

**A fresh install has no accounts**, and QSP sends every page to a setup form
until it does. Open the console after `docker compose up` and it will ask you
for a callsign and a password.

**Over a network it asks for a setup token as well.** QSP prints one once when
it starts with no administrator:

```sh
docker logs qsp 2>&1 | grep setup_token
```

There is no token to type when you open the console **from the machine QSP is
running on** — a request from loopback is from somebody who could read that log
line anyway. A restart prints a new token, so a missed one costs a
`docker compose restart` rather than anything worse.

Setup runs once. Afterwards the page refuses, and every account after the first
is added from the administration page.

<details>
<summary>If every administrator is lost</summary>

```sh
docker compose exec -it qsp /qsp -config /var/lib/qsp/qsp.json adduser mike
```

The `-it` matters: the password is typed with echo off, and QSP refuses to read
one from a pipe — a password that arrives through a pipe is in a shell history,
a script or a CI log by the time it gets here.

If you forget the password:

```sh
docker compose exec -it qsp /qsp -config /var/lib/qsp/qsp.json unlock mike
```

A password can also be reset from the administration page by any other
administrator, which is the ordinary way — this is for when there is nobody left
to do it.

</details>

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

### Peers connect but hear nothing from each other

**Forwarding is off.** With it off QSP relays nothing at all — not even between
two hotspots on the same talkgroup — and the journal says so on every start:

```
forwarding disabled; traffic is observed and not relayed
```

Turn on **Forwarding** under **Network settings → Hotspots and repeaters**, save,
and restart as the page says. A first boot turns it on for you; an install whose
configuration was written before 0.1.252 may still have it off.

Forwarding alone relays only between stations on the same talkgroup. Bridges
between talkgroups, links to other networks and Zello carry nothing until you
set them up.

## What is on and off to begin with

| | |
|---|---|
| Hotspots and Homebrew repeaters | **on**, 62031/udp |
| Forwarding between stations on a talkgroup | **on** |
| Console | **on**, 8080/tcp, every interface |
| Motorola repeaters (IP Site Connect) | **off** — turn on under Network settings when you have one |
| P25 gateways | **off** — turn on under Network settings |
| Talkgroups | all carried |
| Bridges, links to other networks, Zello | **none** until you set them up |

## Adding Zello

Zello is an add-on with its own compose file, `docker-compose.zello.yml`, and
its own image. An install without a vocoder dongle never needs either.

**Three things come first, and none of them is a container:**

- **The dongle and AMBEserver on this host.** AMBEserver runs as a service
  beside Docker, not in it; docs/ZELLO.md, "Running the dongle as a service",
  has the unit, both udev rules and the device settings. Install all of them:
  the restart rule and `ambeserver-replug.service` are what put AMBEserver back
  on the dongle after it is replugged, and without them Zello goes silent both
  ways until somebody restarts it by hand.
- **Zello set up on the console's Zello page**, steps 1 to 4 of "Setting it up"
  in docs/ZELLO.md: the account, the vocoder, and what Zello carries.
- **The page's generated `qsp-zello.json`**, saved in this directory beside the
  compose files. It holds no secret: the connector asks QSP for a logon every
  time it connects.

Then include the add-on, either by uncommenting `COMPOSE_FILE` at the bottom of
`.env`, which makes every `docker compose` command include it, or by naming both
files each time:

```sh
docker compose -f docker-compose.yml -f docker-compose.zello.yml up -d
docker compose exec qsp-zello /qsp-zello -version
```

**QSP restarts once** when this is first applied: its container gains the shared
`/run/qsp` volume, where it makes the socket the connector asks for a logon on.
Then switch Zello on under the Zello page, save, and restart QSP as the page
says. The page's checklist goes green one line at a time.

The connector's own health report is on the host at
`http://127.0.0.1:18090/healthz`, and names the action any failure needs.

**Both halves have to be the same kind of install.** QSP answers the logon
socket only for its own user, and both images run as UID 65532. A connector in a
container and QSP under systemd, or the other way round, are refused, as they
should be.

Not yet done: Zello on this container install carried on air, and the arm64
connector image run on a Pi. Zello has been on air from the systemd install.

To build the connector from this checkout as well:

```sh
docker compose -f docker-compose.yml -f docker-compose.build.yml \
  -f docker-compose.zello.yml -f docker-compose.zello.build.yml up -d --build
```

## Upgrading

The image tag is pinned rather than `latest`, so an upgrade is a decision you
make:

```sh
nano docker-compose.yml     # change the tag, and in docker-compose.zello.yml if you added Zello
docker compose pull && docker compose up -d
docker compose exec qsp /qsp -version
```

**Check the version.** `docker compose ps` says a container started; only the
version says what.

### Upgrading from 0.1.253 or earlier: one command first

**From 0.1.254 the container runs as an unprivileged user (UID 65532), not
root.** A volume created by an older version is owned by root, and the new
container cannot write to it: it stops at startup unable to write its
configuration or database. Give the volume to the new user once, **before**
starting the new version:

```sh
docker compose down
docker volume ls | grep qsp-data      # the full name, usually docker_qsp-data
docker run --rm -v docker_qsp-data:/data busybox chown -R 65532:65532 /data
docker compose pull && docker compose up -d
```

Nothing in the volume is changed except who owns it. A **new** install needs
none of this: the image carries its data directory already owned by that user,
and Docker gives a fresh volume the same ownership.

## Your data

Everything is in the `qsp-data` volume: the configuration, the peer password,
your accounts and the call history.

```sh
sudo ls -l /var/lib/docker/volumes/docker_qsp-data/_data/
```

You should see `qsp.json`, `peer-password` and `qsp.db`. **If `qsp.db` is
missing, the database is inside the container and will be lost on the next
rebuild** — that was true of versions before 0.1.95, where the default data
source name was relative and a `scratch` image has no working directory to
resolve it against.

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
the configuration without starting anything, and attempts each listen address
it would bind. An address already in use is expected while QSP is running; an
address this host does not hold is the fault, and is reported as one.

### Reading and editing the configuration

**The image has no shell**, so `docker compose exec qsp cat ...` cannot work —
there is no `cat`, no `grep` and no `sh` in it, which is the point. The binary
prints its own:

```sh
docker compose exec qsp /qsp -config /var/lib/qsp/qsp.json -print-config
```

To edit it, go through the volume from the host:

```sh
sudo nano /var/lib/docker/volumes/docker_qsp-data/_data/qsp.json
docker compose restart
```

## About the image

`ghcr.io/k9mls/qsp` is built by this repository's CI from a release tag, only
after every test has passed, for amd64 and arm64. It is a single static binary
on an empty base image — no shell, no package manager — running as UID 65532.
`ghcr.io/k9mls/qsp-zello`, for "Adding Zello", is built the same way beside it:
the connector with libopus linked in statically, on the same empty base, as the
same user.

**Each image carries a record of how it was built** (provenance) and a list of
what is in it (an SBOM). The package page on GitHub lists these as extra
`unknown/unknown` platforms beside amd64 and arm64; that is how GitHub displays
them, not a broken image.

## Building it yourself

Add the override file rather than editing anything:

```sh
docker compose -f docker-compose.yml -f docker-compose.build.yml build
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d
```

Takes two or three minutes; most of it is Go compiling.

**Rebuilding an install that ran 0.1.253 or earlier needs the one `chown` in
"Upgrading" first**, exactly as pulling does: the image you build runs as UID
65532 too.

## The other way to run it

`deploy/systemd` is how the author runs QSP: a static binary and a unit file,
no container. It is supported but this is the documented path, because two sets
of instructions means two sets to keep true — and the files this replaced had
never been run at all.
