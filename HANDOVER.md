# Handover, 2026-09-15

Read `NEW-SESSION.md`, then **§8a** of `PROJECT_MEMORY.md`, then **ADR-0052**,
the frame everything about linking sits inside. For the transcoder work read
**ADR-0062** (as much as possible in QSP), then **0063**, **0064**, **0065** in
order — they build on each other.

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
history from a single commit. Scan again first:
`git grep -nIE '\b(KD9EJA|Wisconsin|Post Falls)\b'` and a search for any public
address that is not a documentation range or a public service.

## Where both servers are

**Both servers run 0.1.233** (the repository's own version is `cat VERSION`,
which a document restating goes stale on the next patch), deployed 2026-09-15 from 0.1.193
(production, systemd, 192.168.1.247) and 0.1.191 (test server, Docker,
192.168.1.27). A forty-version jump, taken because the gap itself had become
the risk.

Both applied **migration 6** and created a credential store. All four peers
returned within twenty seconds on production — KD9BXO, AD0MI, KB9TYC, K9MLS —
and the test server's BCARA upstream logged back in. **No configuration file
changed**, so the rollback path stayed clean.

**The rollback binary is `~/qsp-rollback-0.1.193` on Fedora**, built from commit
`b5af5d3` in a worktree and verified by its own `-version`. Keep it: the
previous binary was installed over and there was no rollback until it was
rebuilt. The schema is now at **6** where 0.1.193 expects **5**, and **whether
it refuses a newer schema or ignores the extra table is not known** — so
`~/qsp.db.before-0.1.233` on production and `~/qsp-data-before-0.1.233.tar.gz`
on the test server are the real safety net.

**The credential key is `/var/lib/qsp/secrets.key`** — 32 bytes, mode 0600,
owned by `qsp`, created on first start. **Losing it loses every stored
credential**, recovered only by entering them again. On the test server it
lives inside the `qsp-data` volume: survives a container replacement, dies with
`docker compose down -v`.

---

## The one thing to read before touching the dongle

**If it stops answering, unplug it physically for ten seconds.** Not a software
reset, not an ESXi detach and re-attach — the device node comes back new and the
chip is still lost. Only removing power clears it.

Learned by wedging the operator's DVstick 30 on 2026-09-14, with
`61 00 02 00 0a 21` — a `PKT_RATEP` carrying one argument byte where it takes
twelve. The recovery path now opens `docs/ZELLO.md`.

**AMBEserver has no unit file and is started by hand**, in the foreground:

```sh
/usr/local/sbin/AMBEserver -x -s 460800 \
  -i /dev/serial/by-id/usb-FTDI_FT230X_Basic_UART_DT04S20J-if00-port0
```

`-x` is its debug flag; `-v` prints a version and exits. It binds
`0.0.0.0:2460` unauthenticated with `ufw` inactive, which is **fine for a
foreground bench session and not fine as a service**. Settle that before it
becomes one.

---

## The Zello path: everything is built, nothing has spoken to Zello

That sentence is the state. Each piece is proved on its own; the remaining risk
is entirely "does Zello behave as its specification says", and only a real
connection answers it.

**Proved on the operator's hardware:**

- `internal/ambe` — the AMBE-3000F field table, the rate table, a builder that
  refuses a malformed packet, a client that brings the chip up, holds one call
  at a time and carries frames both ways. **828 frames of real DMR audio off
  the XPR8300 decoded to speech the operator listened to**: zero rejections,
  zero tone frames, peak 7191. §8r.
- `internal/dmrfec` — Talker Alias PDUs and the embedded Link Control that
  carries them, both **byte-identical to what a MOTOTRBO and a Pi-Star
  transmitted**. The EMB generator's colour-code axis is now observed at code 4
  as well as 11.

**Proved without hardware, and testable by anyone:**

- `internal/audio` — USRP framing, 8↔16 kHz conversion with a measured
  anti-alias filter, and the 3:1 repacketiser. **60 ms of latency toward Zello
  is the cost and it is asserted, not a defect.**
- `internal/opus` — libopus behind the `zello` build tag, held to the same
  level measurement that condemned the pure-Go encoder.
- `internal/zello` — the Channels API wire protocol, a hand-written RFC 6455
  WebSocket client, the session, and the RS256 logon token signer.
- `internal/zellobridge` — the assembly, behind the tag.
- `internal/secrets`, `internal/config/fullbackup.go`, and the console's
  credential and full-backup endpoints.

**Build the tagged half with:**

```sh
CGO_ENABLED=1 go test -tags zello ./internal/opus/ ./internal/zellobridge/
```

That tag is load-bearing. A cgo package without it breaks
`CGO_ENABLED=0 go build ./...` and every gate with it, so a plain-DMR server
would stop compiling because of a connector it does not run.

### What is left to build

This list read "a `transcoders` block in configuration" for a day after that
block existed. **Read it against the code, not the other way round.**

**Built on 2026-09-16: both directions through the chip.**

- **DMR to USRP.** A bridge naming a transcoder delivers to it;
  `internal/vocoderlink` decodes each voice burst and sends USRP keyup, audio
  and release to `usrp_peer`. Before this a transcoder target **reached every
  ready peer, including the sender** — latent, because neither server
  configures one.
- **USRP to DMR.** Audio arriving on `usrp_listen` is encoded and built into a
  whole transmission under the transcoder's `radio_id` — voice LC header,
  superframes with EMB and embedded LC, terminator — and routed through
  `routing.Core.RouteFromTranscoder`, where `permit_peers` decides which
  Homebrew peers hear it. Proved in the real binary against a fake AMBEserver
  answering with the chip's recorded frame: a logged-in hotspot received
  H A B C T from the gateway ID, one stream.
- **Transcoded audio reaches the Motorola repeaters that agreed** (0400). It
  was withheld from all of them until then, because the only IPSC path reached
  every repeater with no per-repeater check; KD9EJA's repeater never keyed up
  for a Zello reply. It now goes through SendVoiceTo to each repeater the
  transcoder's permission covers, and does not start over another
  transmission already on that repeater's timeslot.
- **One chip, one direction at a time.** A Zello keyup over a DMR call is
  refused, and the reverse.
- **No colour code setting, on evidence.** The four production hotspots run
  different colour codes and hear each other through QSP, which forwards
  bursts untouched; Motorola repeaters are stamped per repeater by ADR-0042.
  See PROJECT_MEMORY §7, "Colour code on a generated transmission".

**What is not proved: a chip on the bench in the USRP-to-DMR direction.** The
`encoded_needing_fec` counter on `transcoder-audio:<name>` answers the first
question a silent repeater raises — a non-zero value means the chip's frames
are not in DMR's layout. The one recorded real frame needs no correction.

**Production changes made by hand on 2026-09-16, now in the repository** as
`deploy/systemd/ambeserver.service` (generalised: the device path is set with
`systemctl edit`) and `deploy/udev/99-ambe-dongle-latency.rules`.

- **`/etc/systemd/system/ambeserver.service`**: AMBEserver `-x` in the
  foreground, `StandardOutput=null` (its per-packet dump is hundreds of MB an
  hour), `IPAddressAllow=localhost` / `IPAddressDeny=any` (it binds 0.0.0.0
  unauthenticated), `DynamicUser` with `dialout`, enabled at boot. It exists
  because a foreground AMBEserver died with its terminal, and **QSP's vocoder
  supervisor never noticed**: it opens a channel once and never re-checks it,
  so the AMBEserver started next never received the DMR rate command and every
  call decoded at the wrong rate. Fixed in 0401: every call sets the rate
  again, and a vocoder that stops answering is reopened with the whole
  handshake.
- **`/etc/udev/rules.d/99-ambe-dongle-latency.rules`** sets the FTDI latency
  timer to 1 ms. At the 16 ms default a decode took 26.8 ms (median, from a
  capture) against the 20 ms real time allows — three per 60 ms burst — so QSP
  decoded 36.6 frames a second of the 50 needed, dropped a quarter and
  stretched the rest. At 1 ms: 11.9 ms.
- **What the capture also showed**: the Zello app sends 16 kHz, two 60 ms
  frames per 120 ms packet; all 1,917 voice frames the chip encoded from Zello
  audio passed DMR FEC with no correction; DMR audio reaches Zello about 13 dB
  quieter than Zello audio arrives; and Zello audio went to every Homebrew peer
  the page permitted.

**First connection, 2026-09-16, on production.** QSP 0.1.239 and `qsp-zello`
logged on to channel "QSP Server 1" first time, which settles `azp: dev`. Real
DMR audio from the network decoded through the dongle — AMBEserver's debug log
shows speech waveforms coming back — and went to Zello. **Zello to DMR failed:**
every packet from the app was refused by an Opus decoder sized for one 60 ms
frame (0398 fixes it, `qsp-zello` only). **Still open:** the audio Zello users
hear from DMR was reported as bad, not yet described; TG2 is a busy talkgroup,
so every call on it goes to Zello and holds the one chip. AMBEserver is still
started by hand.

1. **`qsp-zello` is built (2026-09-16) and has now met Zello.** It asks QSP
   for a logon over `zello.logon_socket` and is handed a token, username and
   password, never the key (ADR-0066). Proved against a real logon socket and
   a refused endpoint; `docs/ZELLO.md` "Running the connector" has the steps.
   **Found on the way:** Zello's last audio packet and its stop event reach the
   connector on two channels, and select could take the stop first — a ghost
   keyup after nearly every over, fixed with a test that fails on its first run
   without the fix. And `qsp.service` forbade Unix sockets, so QSP would not
   have started with the socket configured.
2. **The console's Zello page is built (2026-09-16)**: a new user's whole setup
   in order, credentials stored write-only with the key checked as it is
   entered, settings saved and versioned, the connector's file generated, and
   a checklist read from health. The channel now lives in QSP and travels with
   the logon. Driven in jsdom as a new user and its saved configuration
   accepted by the real `qsp -check`, switched on and paused. **Not proved in a
   browser.** Found on the way: the console's element gate reads only
   `getElementById` literals, and the page's first version hid every lookup
   behind a helper; and the credential API had no page at all.
   **Next: the first connection, from that page, with the real credentials
   typed there and nowhere else.**
3. **Undecided by the operator:** whether the transcoder's configured `alias`
   is injected as Talker Alias. The standing rule is "passed through, never
   injected", and a gateway's own alias is a new case. Nothing injects it.

**Deploy order for any configuration naming `usrp_listen` or `usrp_peer`:
binary first, then configuration.** QSP refuses unknown fields, so 0.1.233 or
the 0.1.193 rollback binary refuses a document carrying them — remove them
before rolling back.

### What is needed from the operator

- **The gateway's own DMR ID from RadioID.net.** Not instant, and **3132911 is
  taken** — it is the XPR8300's IPSC master ID. Configuration refuses an
  enabled transcoder with no `radio_id` by design.
- **A Zello auth token, username and password** from the developer portal. The
  first key pair was pasted into a chat on 2026-09-15 and rotated; **the
  private key belongs in the credential store, never in configuration.**
- **The channel joined manually in the Zello app first**, and set to Zelect or
  Zelect+. ADR-0064 rests on that: the licensing judgement sits with whoever
  moderates the channel.

### Two things the specification does not settle

- **`azp` is `dev` in a developer token.** Whether a production gateway needs
  something else is undocumented, so it is configurable with `dev` as the
  default. Guessing would give a logon refused in a way that reads like bad
  credentials.
- **IPSC cannot carry a Talker Alias at all.** An IPSC voice packet carries the
  Link Control spelled out as fields rather than the embedded signalling it
  rode in — the same reason IPSC omits the EMB. So QSP can generate an alias
  toward Homebrew peers and can never receive one from Motorola.

---

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

## Waiting on the operator

- **ADR-0058** — TIA-102.BAHA-A permission. Gates DFSI only.
- **ADR-0059** — second tracker or mode-agnostic key, for P25 in Last heard.
- **Three Quantar parts**, and **12 November** for the Cisco licence.
- **Zello credentials and the channel decision**, as above.
- **Colour codes 1, 2 and 8** — four linearly independent values would span the
  EMB's four bits. Code 4 corroborated the generator's prediction on
  2026-09-15, so this is completeness rather than confidence now. Method in
  `testdata/hbp/EMB-CAPTURE-REQUEST.md`.
- **A capture of OTAP crossing QSP**, if wanted. An attempt on 2026-09-15 saw
  210 packets — keepalives only — which suggests the programming never entered
  the IP network. Not blocking anything.

## Ready with no hardware

- **The `transcoders` config block, the USRP socket, and the companion main.**
  The next real work.
- **Talkgroup routing and contention in `internal/p25link`.** QSP is a flat
  reflector: every gateway hears everything and two simultaneous keyups
  interleave. Self-contained, testable against fixtures already in the tree.
- **A session-lifetime control on Administration**, so it stops being a
  file-only setting. `POST /api/config` already validates, records a version,
  writes and applies, so the gap is the console and not the plumbing.
- **`leading byte 0x81`** from radio 999998, about fifty datagrams of 52–66
  bytes on 2026-09-03 at 22:23:59–22:24:05. An unknown IPSC message type with a
  timestamp and a sender attached.

---

## Working conventions, unchanged

Patches by `git am`, numbered, delivered with md5. Gates before every patch:
`gofmt`, `go vet`, `staticcheck`, `go test ./...`, and
`CGO_ENABLED=1 go test -race ./internal/...`. Never commit `go.mod`/`go.sum`.
Machine labels on every command block: **FEDORA**, **QSP-SERVER**, **TEST
SERVER**, **CISCO-ROUTER**. Deploy is `stop; install; start`, never `restart`.

**Seven tests fail in a container with no SQLite driver** and pass on Fedora.
That is the baseline, not a regression.

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
