# Handover, 2026-09-16

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

**Zello is on the air.** Since 2026-09-16 a Zello channel is linked to TG2 on
production, both ways, heard on the Homebrew hotspots and the Motorola
repeaters whose owners agreed. The whole path — console setup, logon handoff,
vocoder, USRP, `qsp-zello` — has carried real calls with real users.

**Servers.** Production (systemd, 192.168.1.247) runs QSP, `qsp-zello` and
AMBEserver; it was confirmed on 0.1.242 during the day, and the patches after
it were delivered for deployment that evening. The test server (Docker,
192.168.1.27) carries the BCARA upstream and no Zello. **Check what each runs
with `-version`**; a version written here goes stale with the next deploy.

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

1. **Set the level toward Zello by ear.** Built (0407): "Level toward Zello"
   and "Level toward the radios" on the Zello page, in dB, soft-limited. The
   capture measured DMR audio 13 dB under Zello audio; start at +10, restart
   QSP, and ask the Zello users. A synthetic signal at that level reaches
   Zello's level at +13 in the tests; the real voice is the judge.
2. **The dongle panel's buttons work under qsp.service's sandbox** — confirmed
   on production on 2026-09-16 by running `systemctl restart` as `qsp` inside a
   transient unit with every one of qsp.service's restrictions. **Installing
   0408 needs the polkit rule copied again** (it adds `reset-failed`).
   **Never test by restarting AMBEserver repeatedly:** five starts in five
   minutes trips its start limit, which is how that test took Zello off the air.
3. **A Docker compose service for `qsp-zello`**, for the container install.
   Only systemd is written.
4. **Talker Alias for Zello transmissions** — whether the transcoder's
   configured `alias` is sent. The standing rule is "passed through, never
   injected"; the operator decides whether a gateway's own alias is an
   exception.
5. **Talkgroup routing and contention in `internal/p25link`.** QSP is a flat P25
   reflector: every gateway hears everything and two keyups interleave.
6. **A session-lifetime control on Administration**, so it stops being a
   file-only setting.
7. **The Docker image is never published.** `deploy/docker/docker-compose.yml`
   pins `ghcr.io/k9mls/qsp:<version>` and no workflow pushes one, so anyone
   following the Docker instructions from GitHub pulls an image that does not
   exist. Either publish on the release tag or make the build override the
   documented default. Decide before the repository is public.
8. **`leading byte 0x81`** from radio 999998 — an unknown IPSC message type,
   about fifty datagrams on 2026-09-03.

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
That is the baseline, not a regression.

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
