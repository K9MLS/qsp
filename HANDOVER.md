## Before this repository is made public

**1. The captures are clean as of 0418 (0.1.261).** The three P25 captures held
mDNS, Syncthing and SSDP traffic from the operator's own network — device
names, service types, a Syncthing device ID, router UUIDs and the operator's
public address — because they were taken with no capture filter. Filtered with
`scripts/filter-capture.py`; two tests in `internal/p25link` now fail on any
capture carrying non-radio traffic or an identity string. **The originals are
still in history**, so the rewrite below has to handle the old capture blobs as
well as the text.

**2. The history still holds what the working tree no longer does.** The tree
was scrubbed on 2026-09-16: other operators' first names, towns and home public
addresses were replaced — a first name by the callsign, a town by its state, an
address by `203.0.113.x` — in text, tests and two HBP captures, payloads
byte-identical. **Every earlier commit still contains the originals.** A scan on
2026-09-19 found five strings absent from HEAD and present in history: two
public addresses and three names or towns, reaching 155 to 292 commits each
across 2 to 9 paths. Three other candidates were common words still in the tree,
so not sensitive. Emails are clean: example.com/org samples, the operator's own
address, `mike@192.168.1.x` from scp lines, and `pi-star.local`.

**3. Then rewrite, with `scripts/scrub-history.py`.** It derives the list
itself, so nothing sensitive is written down. Run it in the real repository
after tagging and bundling a backup:

```sh
git tag pre-rewrite-backup && git bundle create ~/Documents/QSP/qsp-pre-rewrite.bundle --all
scripts/scrub-history.py                 # reports; changes nothing
scripts/scrub-history.py --show          # the mapping, which names people
scripts/scrub-history.py --apply         # rewrites, then verifies
./scripts/check.sh                       # the gates, on the rewritten tree
```

Verified on a clone of this repository on 2026-09-19: 428 commits preserved,
three names or towns and ten addresses replaced, three capture blobs filtered,
and afterwards none of it in any object, any commit message or any capture.
`git filter-repo` is needed (`pip install git-filter-repo`). The push is
`--force-with-lease`, and every hash changes, so anyone else with a clone has
to re-clone.

**4. Then the packages.** `qsp` and `qsp-zello` are private by default and are
switched separately under Package settings; a stranger's `docker compose up`
fails until that is done. The final push needs its tag, or the compose files pin
an image that was never built.
# Handover, 2026-09-17

Read `NEW-SESSION.md`, then **§8a** and **§8s** of `PROJECT_MEMORY.md`, then
**ADR-0052**, the frame everything about linking sits inside. For the Zello and
transcoder work read **ADR-0062**, **0063**, **0064**, **0065** and **0066** in
order — they build on each other — and `docs/ZELLO.md`.

**The history was rewritten on 2026-09-09** to remove a product name from every
commit, message and path, and force-pushed. **Every commit hash predating that
no longer resolves** — they are a record of what happened, not something to
look up.

---

## Before this repository is made public

**The working tree was scrubbed on 2026-09-16; the history was not.** Other
operators' first names, towns and home public addresses were replaced — a first
name by the callsign, a town by its state, an address by a reserved
documentation address (`203.0.113.x`) in text, tests and the two HBP captures
that carried them, with the captures' payloads byte-identical. **Every earlier
commit still contains the originals.** The repository is private
(PROJECT_MEMORY §1), so that costs nothing today; before it is made public,
either rewrite history (`git filter-repo --replace-text`) or publish a fresh
history from a single commit. Scan again first: the scrub commit's diff
(`git show` on "Other operators' names, towns and home addresses are out of the
working tree") lists every kind of detail removed, so search the tree and the
history for those, and for any public address that is not a documentation
range or a public service.

---

## Where things stand

**Zello is on the air, both ways, on real radios.** Since 2026-09-16 a Zello
channel is linked to TG2 on production, heard on the Homebrew hotspots and on
the Motorola repeaters whose owners agreed — IPSC included, since 0400. The
whole path has carried real calls with real users: console setup, logon
handoff, vocoder through a DVstick 30, USRP, `qsp-zello`.

**Versions, as confirmed at the end of 2026-09-16** (check with `-version`
before trusting these; they go stale with the next deploy):

| Where | Runs | Confirmed how |
|---|---|---|
| **GitHub** `main` | 0.1.254, tagged `v0.1.254` (this handover adds 0.1.255) | pushed |
| **Production** (systemd, 192.168.1.247) | **QSP 0.1.253**, `qsp-zello` 0.1.240, AMBEserver as `ambeserver.service` | `qsp -version` |
| **Test server** (Docker, 192.168.1.27) | **0.1.254 built from source, running as UID 65532**; BCARA link connected | `ps` on the container, its log |

Production's differences from 0.1.254 are Docker-only (the published image and
the unprivileged container), so it needs nothing until the next code change.

**Proven on hardware on 2026-09-16:**
- Zello both ways on Homebrew and Motorola repeaters; all 1,917 captured voice
  frames the chip encoded passed DMR FEC uncorrected.
- A vocoder restarted underneath QSP is set up again: every call sent its rate
  and init packets as a pair (5 and 5 in a minute's capture).
- The dongle panel's `systemctl` control works under qsp.service's full
  sandbox, and the polkit rule grants exactly start, stop, restart and
  reset-failed on `ambeserver.service` to `qsp`.
- The unprivileged container: an existing volume after its one `chown`, and a
  brand-new volume whose every file was created owned by 65532.

**Not yet confirmed:**
- **The first CI run of the image job** (`Publish image`, triggered by the
  `v0.1.254` tag). If it failed, fixing it is the first job.
- **The Actions runs for `v0.1.246`, `v0.1.249` and `v0.1.254`** as a whole,
  including the zello-tagged step's first runs on GitHub.
- **The arm64 image on a Raspberry Pi.**
- **The Zello level toward Zello set by ear** — it is at 0 dB.

**On production, set by hand and also in the repository:** AMBEserver as
`/etc/systemd/system/ambeserver.service`, the FTDI latency rule in
`/etc/udev/rules.d/`, and the polkit rule in `/etc/polkit-1/rules.d/` (the 0408
version, with reset-failed).

**Rollback binaries** are kept beside each install as `~/qsp-previous-<version>`
on production, and `~/qsp-zello-previous-<version>` for the connector.

**The credential key is `/var/lib/qsp/secrets.key`** — 32 bytes, mode 0600,
owned by `qsp`. **Losing it loses every stored credential**, including the
Zello key, recovered only by entering them again. On the test server it lives
in the `qsp-data` volume and dies with `docker compose down -v`.

---

## The one thing to read before touching the dongle

**If it stops answering, unplug it physically for ten seconds.** Not a software
reset, not an ESXi detach and re-attach — the device node comes back new and the
chip is still lost. Only removing power clears it.

Learned by wedging the operator's DVstick 30 on 2026-09-14, with
`61 00 02 00 0a 21` — a `PKT_RATEP` carrying one argument byte where it takes
twelve. The recovery path now opens `docs/ZELLO.md`.

**AMBEserver runs as a systemd unit on production** (`deploy/systemd/ambeserver.service`,
admitting only local traffic) with the FTDI latency timer at 1 ms
(`deploy/udev/`). Its command line, for a bench session:

```sh
/usr/local/sbin/AMBEserver -x -s 460800 \
  -i /dev/serial/by-id/usb-FTDI_FT230X_Basic_UART_DT04S20J-if00-port0
```

`-x` keeps it in the foreground and prints every packet; `-v` prints a version
and exits. Run by hand, it dies with its terminal — which is how production
learned to run it as a service.

---

## Open, in the order to take them

**The goal before the P25 push, set by K9MLS:** QSP at a clean point for going
public — the console looks right and works, the code is solid, and a stranger
can install it and be on the air. Items 1–4 are that goal.

1. **Confirm CI published the image.** Actions tab: `Check`, then `Publish
   image`, both green for `v0.1.254`; the `qsp` package under the K9MLS
   profile. It stays private until switched under Package settings → Change
   visibility, and a private package needs `docker login ghcr.io` with a
   `read:packages` token to pull. The publish job has never run before.
2. **A README for an operator arriving cold — done in 0414 (0.1.256).** It
   leads with what QSP does, what you need and four steps to be on the air; the
   status report moved below them, and the configuration reference moved to
   docs/CONFIGURATION.md. A test ties its ports, console addresses and `.env`
   variables to the code. **Tag `v0.1.256` and push it**, or the compose file
   pins an image that was never published.
3. **Test the replug recovery on hardware — 0416 (0.1.258) is unproven.** The
   rule and unit are written and the guard was exercised against a stub
   systemctl, but there is no udev or systemd in the build container, so
   nothing has fired for real. On QSP SERVER, after installing both rules and
   the unit: unplug the dongle, plug it into another port, and watch
   `journalctl -u ambeserver -u ambeserver-replug` and QSP's log for `vocoder
   ready`. Costs a few seconds of Zello. If it works, no human is needed for
   the failure that cost three days on 2026-09-16 to 09-19.
3. **Zello on the Docker install, as an add-on — built in 0415 (0.1.257), not
   yet on air.** `docker-compose.zello.yml`, `Dockerfile.zello` (static libopus
   on scratch, UID 65532), a shared `qsp-run` volume for the logon socket, and a
   second image in the publish job. Compose resolved every file combination;
   the amd64 static link ran from an empty root. **Unproven until run:** the
   arm64 cross-compile (first CI run on a tag), and the logon and audio across
   two containers, which needs the DVstick on QSP SERVER — production, so only
   in a window K9MLS chooses.
4. **Rewrite history before switching the repository to public** — see "Before
   this repository is made public" above.
5. **Set the level toward Zello by ear** (0407): start "Level toward Zello" at
   +10, restart QSP, ask the Zello users. DMR audio measured 13 dB under Zello.
6. **Talker Alias for Zello transmissions — done in 0422 (0.1.264), not seen
   on a radio.** The configured alias is transmitted: Link Control first, then
   one PDU per superframe, cycling. Decoded back out of the built burst stream
   in tests, and the PDU bytes match `testdata/hbp/hbp-talker-alias.pcap`,
   where a MOTOTRBO sent one. **Set on the Zello page from 0424** (section 2,
   "Name radios show"). **Verified on the wire, 2026-09-21**: a capture of
   production's traffic to the WPSD hotspot decoded 11 alias PDUs reading
   `KD9BXO`, and MMDVMHost logged the same text itself. Not yet seen on a
   radio's display; the gateway ID 9898 is unregistered, and a registered one
   has been requested, which will name Zello calls on dashboards and in
   contact lists regardless. From 0425 Motorola repeaters get the voice Link
   Control rather than the alias, and Last heard shows the alias.
7. **Talkgroup routing and contention in `internal/p25link`**, the start of
   making P25 a network rather than a flat reflector: voice reads the talkgroup
   and relays to every registered gateway regardless. **Now testable rather
   than theoretical** — K9MLS's Pi-Star registered on production 2026-09-19, so
   a second gateway on another talkgroup would demonstrate the defect. See
   P25-NETWORK.md §2 and §6, and P25-GATEWAY.md for the operator view.
8. **A session-lifetime control on Administration — done in 0423 (0.1.265).**
   Reported and editable under Administrators: the value in force, whether it
   is the default, the active count, and the reader's own expiry. Not seen in a
   browser yet — the tests cover the report and the endpoint, not the page.
9. **`leading byte 0x81` — closed as unreproducible.** About fifty datagrams
   from radio 999998 on 2026-09-03 and **none since 2026-09-10**, checked on
   production on 2026-09-21. Nothing to reproduce and nothing to fix; if it
   returns, the datagrams are the evidence to keep.

**Two operating rules learned the hard way on 2026-09-16:**
- **Never test by restarting a service in a loop.** AMBEserver's unit allows
  five starts in five minutes; a diagnostic script restarting it repeatedly
  tripped that and took Zello off the air. The panel now pauses after each
  press and explains a tripped limit, but a script has no such guard.
- **No command that restarts or stops something on production unless the
  operator asked for it.** Build and verify in the container or on the test
  server; the operator installs when it suits them.

## Waiting on the operator

- **ADR-0058** — TIA-102.BAHA-A permission. Gates DFSI only.
- **ADR-0059** — second tracker or mode-agnostic key, for P25 in Last heard.
- **Three Quantar parts**, and **12 November** for the Cisco licence.
- **Colour codes 1, 2 and 8** EMB captures — completeness, not confidence;
  method in `testdata/hbp/EMB-CAPTURE-REQUEST.md`.

## What is still unproven in `internal/ambe`, and marked as such

The parity byte's composition (no worked example prints one and this board has
parity disabled), `PKT_RATEP`'s twelve-versus-eleven data bytes, and the
`CHAND4` soft-decision field. Everything else is backed by the manufacturer's
printed bytes or this board's own.

**Read the F manual, not the R.** The board is an **AMBE3000F** and the manuals
differ where this work touches: `SK_ENABLE` and `TX_RQST` exist only on the F,
the RESET pin is an I/O there, and the echo canceller and suppressor are
**unsupported in packet mode** on the F. The copy in use is **version 3.7,
October 2016**, `md5 f20fd488960efdfbf2ba51165c6c7718`, 111 pages, and printed
page *N* is PDF page *N*+10. The material is printed pages 58 to 92.

**It cannot be fetched, only attached.** Three attempts across two documents and
three hosts all truncated at the same point, and the token limit made no
difference. The operator downloads it on Fedora and attaches it.

**The manual contradicts itself in four places**, all recorded in
`internal/ambe` rather than resolved silently: `PKT_RTSTHRESH`'s length column
is a total and not a data length; `SAMPLES` is `0x30` in one table and `0x03` in
another, which Channel Packet Example 2 settles; `PKT_CHANNEL0` is given no data
bytes, one, and two in three places, and only a bare identifier makes the
examples add up; and the prose under two examples disagrees with the table above
it by one byte. **Prefer the tables** — all four compute exactly.

**ETSI has its own trap.** `TS 102 361-2` downloaded cleanly and the next
request for part 1 returned 35 KB of HTML titled "Web Application Firewall"
with a different md5 each time. The path was never wrong; a browser user agent
is enough. **Check `file` on every spec download** — a WAF page named `.pdf` is
exactly what that looks like.

---

---

## Working conventions

Patches by `git am`, numbered, delivered with md5. Gates before every patch:
`./scripts/check.sh` on Fedora — `gofmt`, `go vet`, `staticcheck`, tests, the
race detector, documentation accuracy, and the `zello`-tagged connector, which
needs `opus-devel`. Never commit `go.mod`/`go.sum`. Machine labels on every
command block: **FEDORA**, **QSP SERVER**, **TEST SERVER**.

**Seven tests fail in a container with no SQLite driver** and pass on Fedora.
That is the baseline, not a regression — **but compare the failure messages,
not the test names.** One of the seven checks health wording too, and 0410
broke that assertion inside a test already expected to fail; it reached Fedora
before it was seen. The container's baseline is six messages, every one about
the SQLite driver.

**`qsp-zello` is built in a Debian container on Fedora**, not on Fedora itself:
Fedora's glibc is newer than the servers', and a cgo binary built against it
will not start on Ubuntu 24.04. `docs/ZELLO.md` has the `podman` command; check
`objdump -T | grep GLIBC_` is at most 2.39.

### Deploy, as it is actually done

**FEDORA** builds and bundles; production takes a binary, the test server takes
the bundle and builds its own image.

```sh
cd ~/Documents/QSP/qsp
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(cat VERSION)" \
  -o /tmp/qsp-$(cat VERSION) ./cmd/qsp
/tmp/qsp-$(cat VERSION) -version
scp /tmp/qsp-$(cat VERSION) mike@192.168.1.247:/tmp/
git bundle create /tmp/qsp-NNNN.bundle main
scp /tmp/qsp-NNNN.bundle mike@192.168.1.27:/tmp/
```

**Copy the old binary aside before installing over it**, which was learned by
not doing it:

```sh
sudo cp -a /usr/local/bin/qsp ~/qsp-previous-$(sudo /usr/local/bin/qsp -version | cut -d' ' -f1)
```

`-X main.version` **is not vestigial.** `main.version` is a deliberate override
hook that `buildVersion()` prefers, falling back to the `internal/buildinfo`
constant — which is why a build without it still reports correctly. Keep it.

**Traps, all found the hard way.** A bare `/tmp/qsp` from three days earlier was
installed over the server binary — use versioned names, and a name that lies is
worse than none. A config can outrun a binary: QSP rejects unknown fields, so an
older binary meeting a new one exits rather than warns, five times, until
systemd gives up. **You cannot roll back across a config change without removing
the new field first** — which is why the 0.1.233 deploy deliberately changed no
configuration.
