# Phase 3 soak: fourteen days unattended

**BLUEPRINT §16 gate: "A scheduled net links and unlinks unattended for two
weeks."**

Fourteen days of wall-clock time is the only constraint in this project that
cannot be compressed by working harder, so this starts as early as it can and
runs while other work continues.

---

## What this proves, and what it does not

**Proves:** the scheduler opens and closes a bridge at the right local times,
across a fortnight, without supervision — including that it survives restarts,
and that DST-correct wall-clock scheduling behaves over a long span.

**Does not prove:** that audio relays correctly between two peers. That needs a
second peer and is a separate test. This soak runs with one hotspot, so the
bridge opens and closes with nothing crossing it. That is still the gate as
written, but it is worth being clear about which half is being tested.

---

## Read this first: the journal is the evidence

**Nothing writes to the database.** Registering a driver made the migrations
run and created `configuration_versions` and `audit_events`, but no code inserts
into either. `grep -rn 'INSERT INTO' --include='*.go'` finds only the migration
runner's own bookkeeping.

QSP's audit trail goes to the structured log. So the journal is the entire
record of whether this soak passed, and it must survive a reboot:

```sh
sudo mkdir -p /etc/systemd/journald.conf.d
printf '[Journal]\nStorage=persistent\nSystemMaxUse=500M\n' | \
  sudo tee /etc/systemd/journald.conf.d/qsp-soak.conf
sudo systemctl restart systemd-journald
```

Without this, a reboot on day nine takes the first nine days with it. Do it
before starting, not after.

---

## Setup

### 1. Build for the target

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -ldflags "-s -w -X main.version=$(cat VERSION)" -o qsp-arm64 ./cmd/qsp
```

Use `GOARCH=arm GOARM=7` for a 32-bit Pi, or `amd64` for a VM. The armv7 binary
is about 12 MB.

### 2. Install

```sh
sudo useradd --system --no-create-home --shell /usr/sbin/nologin qsp
sudo install -m 0755 qsp-arm64 /usr/local/bin/qsp
sudo install -d -o qsp -g qsp -m 0750 /var/lib/qsp
sudo install -o qsp -g qsp -m 0640 deploy/soak/qsp.json /var/lib/qsp/qsp.json
sudo install -m 0644 deploy/systemd/qsp.service /etc/systemd/system/
```

Edit `/var/lib/qsp/qsp.json` for your timezone if you are not in
`America/Chicago`. The scheduler rejects abbreviations like `CST`, because those
cannot express "20:00 local all year".

### 3. The password file

```sh
printf 'your-soak-password' | sudo tee /var/lib/qsp/peer.pass > /dev/null
sudo chown qsp:qsp /var/lib/qsp/peer.pass
sudo chmod 600 /var/lib/qsp/peer.pass
```

`0600` is enforced — QSP refuses to start otherwise. Use a password dedicated to
this soak, not one in use elsewhere.

### 4. Firewall

```sh
sudo firewall-cmd --add-port=62031/udp --permanent && sudo firewall-cmd --reload
```

Only UDP 62031 faces the network. The console stays on `127.0.0.1`; reach it
with `ssh -L 8080:127.0.0.1:8080 user@host`. There is no authentication on any
endpoint.

### 5. Start

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now qsp
sudo systemctl status qsp
journalctl -u qsp -n 30
```

### 6. Point the hotspot at it

Follow `docs/HARDWARE-TEST.md` steps 2 to 7, using this host's address. Note the
soak config has `forwarding: true`, unlike the bench test.

**Record the start date.** The fortnight runs from the first successful
scheduler transition, not from installation.

---

## What the schedule does

Four windows a day, every day: 06:00, 12:00 and 18:00 for thirty minutes, and
23:30 for an hour. That is **eight transitions a day, 112 over fourteen days**.

Frequency is deliberate. A single daily window gives 28 transitions and a
fortnight to discover a fault; eight a day surfaces one within hours. The 23:30
window crosses midnight on purpose — a day boundary is where date arithmetic
tends to be wrong.

---

## Checking in

Roughly weekly. The point of an unattended test is not watching it.

```sh
# Has it restarted?
systemctl show qsp -p NRestarts

# Every open and close so far
journalctl -u qsp --since "14 days ago" | grep -Ei 'bridge|window|schedul'

# Anything at warning or above
journalctl -u qsp -p warning --since "14 days ago"

# Memory, against the same figure a week earlier
systemctl show qsp -p MemoryCurrent
```

Log the memory figure each time you check. A slow climb over a fortnight is
exactly the class of fault a soak exists to find and a test suite cannot.

---

## Pass criteria

All four, at day fourteen:

1. **`NRestarts` is 0.** Any restart needs explaining before the run counts. A
   restart QSP recovered from cleanly may still be acceptable — but that is a
   judgement to make with the journal open, not a box to tick.
2. **Every scheduled transition happened, at the right local time.** 112 of
   them. Spot-check across the fortnight rather than reading all of them, and
   check the ones nearest midnight most carefully.
3. **The peer stayed connected**, or every disconnect has a cause in the log.
4. **Memory is flat**, not trending upward.

If a DST change falls inside the window, that is a bonus rather than a
requirement: it is the single best test of wall-clock scheduling, and worth
noting in the result either way.

---

## Recording the result

Append to `PROJECT_MEMORY.md` §6 beside the hardware validations, with dates,
restart count, transitions observed, and memory at start and end. A gate closed
without evidence is a gate nobody can check later.

---

## Stopping

```sh
sudo systemctl disable --now qsp
```

The database and journal remain. Restore the hotspot to its normal network per
`docs/HARDWARE-TEST.md`.
