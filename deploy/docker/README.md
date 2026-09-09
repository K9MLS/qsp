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

That is a working master. **Repeating between peers on the same talkgroup is
on; forwarding between talkgroups and to other networks is off**, and the
journal says so on every start:

```
forwarding disabled; traffic is observed and not relayed
```

An instance that relays traffic nobody asked it to relay is the one mistake
this software must not make on somebody's behalf, so bridges and links are
things you turn on deliberately.

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

## Building it yourself

Add the override file rather than editing anything:

```sh
docker compose -f docker-compose.yml -f docker-compose.build.yml build
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d
```

Takes two or three minutes; most of it is Go compiling.

## The other way to run it

`deploy/systemd` is how the author runs QSP: a static binary and a unit file,
no container. It is supported but this is the documented path, because two sets
of instructions means two sets to keep true — and the files this replaced had
never been run at all.
