# ADR-0048: The container install, and what it has to get right for a stranger

**Status:** Accepted — built, run on a clean machine, and amended six times by what that found
**Date:** 2026-09-06

## Context

QSP is meant to be a free alternative to a commercial DMR server for the amateur community, and
today the only way to run it is to build it from source with a Go toolchain and
write a configuration file by hand. That is a reasonable bar for the three
people on this network and an unreasonable one for the audience the project
exists to serve.

The operator wants a `docker compose up` install that works on first run with
recommended settings, for people who are not necessarily advanced.

### There is already Docker material, and it has never been run

`deploy/docker/Dockerfile` and a compose file existed. Reading them
against how QSP actually works, before writing anything new:

- **The compose file publishes no UDP ports and does not use host networking.**
  It maps `127.0.0.1:8080:8080` and nothing else. Neither 62031 nor 50000 is
  reachable, so a container started from it **cannot receive a single DMR
  packet**. It is a web console in a box.
- **The build stage is `golang:1.22-alpine`.** The module requires 1.27. That
  build fails outright.
- **The volume is `/data` and the config `/data/qsp.json`**, where production,
  the systemd unit and every document say `/var/lib/qsp`. Two layouts, one of
  them undocumented.
- **The healthcheck's comment and its command disagree.** The comment says QSP
  checks its own readiness endpoint; the command is `/qsp -version`, which
  proves the binary can execute and says nothing about whether the server is
  serving.

This is the pattern §8a names, for the twelfth time: something built, wired,
exported and never called. It is worth writing down plainly because it is
otherwise tempting to treat the existing files as a starting point, and they
are not — a stranger who found them would conclude QSP does not work.

## Decision

### 1. Exactly one thing for the operator to fill in

**A DMR server cannot work out of the box, and should not pretend to.** It needs
the operator's own radio ID, and those are individually assigned. Shipping a
plausible default would put two stations on the air announcing the same ID,
which is somebody else's problem rather than merely a bad configuration.

So the target is not zero configuration. It is **one value, asked for in the
most obvious place available**, with everything else correct on first boot:

```sh
cp .env.example .env
nano .env                 # QSP_RADIO_ID=3132910
docker compose up -d
```

**With `QSP_RADIO_ID` unset, QSP refuses to start** and says where to get an ID
and which line to edit. A server that stops and explains itself is kinder than
one that starts and is wrong, and this project has spent a day on the cost of
software that looks like it is working.

### 2. The first-run bootstrap belongs in the binary, not an entrypoint script

On start, if the configuration file does not exist, QSP writes the starter
config and says so in the journal. It is then the operator's file and is never
touched again.

**A shell script would mean abandoning `scratch` for Alpine**, and the logic
would sit where the test suite cannot reach it. In `cmd/qsp` it is tested like
everything else, the image keeps no shell and no operating system, and the
systemd install gets the same behaviour for free. One implementation, two
install methods, one set of tests.

### 3. The starter config

Homebrew enabled on `0.0.0.0:62031`. **IP Site Connect disabled.** Most people
arriving have a hotspot rather than a Motorola repeater, and IPSC needs a
negotiated peer relationship; an idle listener on 50000 is attack surface with
no benefit to the person who just installed it. One local talkgroup, no upstream
links.

### 4. Host networking

`network_mode: host`, not the default bridge.

**Docker's bridge NATs UDP and rewrites source addresses, and QSP tracks peers
by address.** ADR-0011 as amended records that hotspots rebind NAT every eight
to nine minutes and that a rebound keepalive must be answered with MSTNAK. A
second NAT in that path produces intermittent peer-tracking faults, which is the
class of defect this project has spent the most hours on. The cost is no port
isolation, which for a service whose entire job is to be reachable by UDP from
the internet is not much of a cost.

### 5. The console binds to `0.0.0.0:8080`, **after** `/api/peers` is settled

Under host networking `127.0.0.1` means a newcomer sees nothing until they
build an SSH tunnel or a reverse proxy. That is a wall on minute one, and the
console has authentication and is designed to be exposed.

**But `/api/peers` is still unauthenticated** — §8k carries it as an open
decision — and it returns peer addresses. Defaulting to `0.0.0.0` before that is
settled publishes every member's IP address to anyone who finds the port. So
the sequencing is not negotiable: settle `/api/peers`, then ship the friendly
default. If it is not settled, the fallback is `127.0.0.1` and a prominent
paragraph, which is worse for novices and leaks nothing.

### 6. Compose is *the* documented install; systemd is how the author runs it

`deploy/systemd` stays and is labelled as such. **Two supported paths means two
sets of instructions to keep true**, and this project's method is that untested
claims rot — which is exactly what happened to the files this record opens by
describing.

### 7. Pin the version in the example compose file

`ghcr.io/k9mls/qsp:0.1.87`, with a comment on upgrading. `:latest` looks
friendly and turns a routine `docker compose pull` into an unannounced upgrade
of a live repeater network.

## What actually defeats a newcomer, and it is not Docker

**Port forwarding.** A DMR master must be reachable by inbound UDP from the
internet: 62031/udp forwarded at their router, and an ISP that is not using
CGNAT. No container fixes that, and it will defeat more people than any
configuration file.

Two consequences:

- The README covers prerequisites and port forwarding **before** it covers
  installation.
- **QSP should say so itself.** If no peer has ever connected after some
  minutes, the console should say that nothing has reached this server and name
  the port to check, rather than showing an empty table. That is the difference
  between software that works and software somebody can get working, and it is
  the same principle as `rate34_block`: make the system report its own state
  instead of leaving the operator to infer it.

## Consequences

**The existing `deploy/docker` files are replaced rather than edited.** Nothing
in them has run.

**Publishing to `ghcr.io/k9mls/qsp` on `v*` tags** lets people install without a
Go toolchain. While the repository is private the package is private too, which
is the correct state until the conversations about going public have happened.

**The version stamping needs checking.** The Dockerfile passes
`-X main.version=${VERSION}` and `cmd/qsp` does declare `var version`, but
`internal/buildinfo` holds a `Version` constant that a test pins to the
repository's `VERSION` file. Which one `qsp --version` reports in a container
build has not been verified, and a container that misreports its version would
have cost this project an hour today on its own.

## Order of work

1. Settle `/api/peers`, because it gates decision 5 and is a decision rather
   than code.
2. First-run configuration bootstrap in `cmd/qsp`, with tests.
3. Dockerfile, compose file, `.env.example`, replacing what is there.
4. CI publishing to `ghcr.io` on `v*` tags.
5. README: prerequisites and port forwarding first, then install.
6. The "nothing has reached this server" hint in the console.

**Steps 2 and 6 are what make it easy.** The container files are the least
interesting part of this record.

## Amended by building it, 2026-09-07

Three of this record's decisions were wrong, and the code said so.

### The radio ID was never needed

This record said the one value to ask for was the operator's radio ID. **A
Homebrew master has no radio ID of its own** — peers bring theirs — and
`MasterID` belongs to IP Site Connect, which starts disabled. `config.Default()`
validates with no input at all.

What the validator does refuse is a listener with no password and no access
policy, and it names both. So a first run asks for:

- **`QSP_PEER_PASSWORD`**, the shared secret hotspots authenticate with, written
  to a mode-0600 file of its own because configuration gets pasted into forum
  posts;
- **`QSP_ALLOWED_PEERS`**, the repeater IDs permitted to register.

### The empty allow list is not permitted, and that is better

The plan was to write an empty permit list so a fresh instance carried nothing
until its operator filled it in. **The validator refuses that**: a permit list
with no entries refuses every station, and it treats writing one as a mistake
rather than a policy — which it usually is.

Being made to ask is the better outcome. An operator states who may connect
*before* anything is listening, rather than starting a server that is silently
useless and discovering it when a hotspot will not register.

Talkgroups start as a deny list with no entries, the documented way to say
"all": **registration is the safety boundary**, and which talkgroups to carry is
a refinement an operator makes once they know what their members use.

### The container runs as root

The image has no `/etc/passwd`, so an unprivileged user is a bare numeric UID,
and Docker creates the data volume owned by root — so that UID could not write
the configuration it exists to write. The usual answer is an entrypoint script
that chowns and drops privileges, which means abandoning `scratch` for a base
image with a shell, which is the thing the image exists to avoid.

It binds no port below 1024, contains one static binary and no shell, and uses
host networking so no capability is needed for the socket. An operator who wants
it unprivileged sets `user:` and owns the volume themselves. **Named here rather
than left as a silent default.**

## Run on a clean machine, 2026-09-07

A stock Ubuntu 24.04 VM with Docker installed from scratch. The image built,
the volume was created empty, the first run wrote its configuration, the schema
migrated, both sockets bound and `/healthz` reported healthy.

**Six defects, none of them findable by reading the code.**

1. **The database was outside the volume.** `config.Default()` uses the
   relative DSN `qsp.db`, and a `scratch` image has no working directory, so it
   resolved to `/qsp.db` in the container's writable layer. The volume held the
   configuration and the password and nothing else. **Every account and every
   call record would have been discarded on the next rebuild, silently, weeks
   later.** The bootstrap now writes an absolute path beside the configuration.

2. **The example was correct and was broken by "fixing" it.** QSP prints an
   advisory at startup about seven-digit IDs, because the registry issues those
   to operators and some hotspots append a two-digit suffix. That advisory —
   a `WARN`, not an error — was read as ground truth and the example changed to
   `313291001`, which **overflows the 24-bit subscriber field and made the
   first run refuse to start at all.**

   The running network was the evidence the whole time: 3132910, 3155413 and
   3127045 are registered and passing traffic on the author's instance. An
   advisory is a prompt to check something, and the thing to check it against
   is a system that works.

3. **The first-run message repeated it**, in the text somebody reads when
   nothing else has worked yet.

4. **The commented-out `build:` block did not survive contact.** Removing the
   `# ` leaves five spaces where four are needed, and the first command run on
   the machine returned `did not find expected key` from a YAML parser. It is a
   separate override file now, `docker-compose.build.yml`, with nothing to edit.

5. **The guide told the operator to `cat` and `grep` inside a shell-less
   image.** There is no `cat` in `scratch`. It now uses `-print-config` and
   names the host path under `/var/lib/docker/volumes`.

6. **Forwarding is off and the guide never said so.** Peers connect, the
   master repeats between them, and bridges and links do nothing until
   configured — which is correct and which a newcomer would read as a fault.

7. **A refused first run left the password file behind.** It was written
   before the configuration was validated, so an out-of-range ID produced a
   volume holding `peer-password` and nothing else. A half-made state that
   survives a refusal is worse than the refusal, because the next attempt
   starts from somewhere nobody chose. Everything is decided before anything is
   written now.

### And two more, after the volume was fixed

8. **Nobody could create an account.** `adduser` ran `stty -echo` to hide the
   typed password, and a `scratch` image has no stty. The first thing an
   operator does after a successful install is create their account, and it
   failed — a server nobody can sign in to is not installed. Echo is turned off
   with a terminal ioctl now, entirely inside the process, at the cost of no new
   dependency.

9. **The guide never mentioned creating an account at all.**

## What is still not proved

**No peer has ever registered with a containerised instance.** The test machine
had no hotspot to point at it, so the login handshake, the access list and the
NAT-rebind path that made host networking necessary are all still untested in a
container.

Before this run:

**Nothing here had been run.** Docker is not available where the tests run, so
the image has never been built and no container has ever started. That is the
same sentence this record opens by writing about the files it replaced, and it
is only untrue once an operator runs it.

What *is* checked is the part that drifts silently, in `cmd/qsp/deploy_test.go`:
the environment variables the compose file passes against the constants the code
reads, the volume path against the entrypoint, host networking against the
absence of a `ports:` block, the image tag against `VERSION`, and the build
stage's Go version against `go.mod`. The version check caught its own drift the
first time it ran.

The rest is checked by somebody running it on a machine that has never had QSP
on it, which is the only way it ever could be.
