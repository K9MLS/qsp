# Handover, 2026-09-27

Read `NEW-SESSION.md`, then **§8a** of `PROJECT_MEMORY.md` — how this project
finds its defects, and the thing the last two days proved again. Then **§8s**,
then **ADR-0052**, the frame everything about linking sits inside. For the text
service read **ADR-0067** and **ADR-0068** in order; for Zello and the
transcoder, **ADR-0062** through **0066** and `docs/ZELLO.md`.

## The two live threads

**1. The Quantar link waits on one part.** A replacement V.24 card is inbound
from KD9EJA. The station's **AUX LED has never lit**, which is the board's own
report that its V.24 section is not running, and it is the first thing to check
on this path — ahead of anything the router can tell you. Everything else is
proven: the router configuration matches the published build line for line, and
the cable, two separate adapters, the 9600 clock and the codeplug each have an
instrument behind them. The router is parked with `shutdown` saved to startup
and is one `no shutdown` from live. See `docs/P25-PLANNING.md`.

**2. The text service can send, and waits on a radio's display.** An
administrator composes a group text on the **Administration** page —
talkgroup, timeslot, sender ID, message — and QSP sends it to **everybody on
the talkgroup** (0442): every hotspot on it, every Motorola repeater, and
wherever the bridges carry it. An optional hotspot ID sends to that one
hotspot only, for testing. The composed frames reproduce the
operator's captured "K9MLS" burst for burst, all twenty-two, before any radio
is involved. **The instrument is the display**: send to a talkgroup the radio
listens to (TG2 on timeslot 2 is what it sent on), from an ID that is not the
radio's own, and watch. Group only: a private text is a confirmed packet whose
acknowledgement is phase 3. The send is refused, with the reason, while any
call is active on the timeslot, while the hotspot is hearing a parrot or
another text, or for a hotspot not registered. After phase 2 the order is
phase 3 on a Motorola repeater — the relay path already turns Homebrew text
into IPSC — then `TIME`, `WX` and the alert poller per ADR-0068.

The phase found two things before a radio was involved. The preamble CSBK's
CRC is CRC-CCITT with mask `0x5A5A` (0438), and both CRC-16 masks turned out to
be ETSI's once the standard's inversion is folded in. And **a Golay (20,8) row
was wrong** (0439), so every data header and Rate 1/2 block QSP has ever relayed
to a hotspot carried two wrong slot-type bits; MMDVMHost corrected them, which is
why nobody saw it.

**Texts on the air, 2026-09-28.** A composed text to TG2 **displayed on a
radio on the Motorola repeater**, across production and the linked test server
— the first text QSP ever wrote to reach a screen. **Nothing displayed on the
R7 through the Pi-Star**, from any source, and the cause was QSP's (0443):
MMDVMHost turns each network preamble into fifteen on the air, and QSP was
sending sixteen, so the hotspot transmitted about fourteen seconds of
preambles and the radio gave up. Hotspots now get one preamble per text.
**Test it first**: a console text to TG2, hotspot field empty, watched on the
R7. Note that **the operator's R7 and the radio on the repeater both transmit
as 3132910**, so a text between those two can never display on either —
a radio discards a text from its own ID. A second radio with its own ID is
being programmed for that test.

**What to deploy, and why.** Production runs 0.1.284 from 2026-09-28: the
Golay fix and the network-wide send. **0.1.285 is the preamble fix, and every
hotspot radio needs it** — relayed texts included, not only composed ones. Everything else since
0.1.269 is documents, and code nothing calls unless an administrator presses
Send. Deploy when it suits, by the usual path below; nothing here restarts
anything on its own.

**A crash risk found on the way, not yet fixed** — see item 0 of the open list.

## Three readings that wasted a day between them

All three are now in the documents, and all three will mislead again if they are
not believed:

- **`up/up` on a STUN interface means nothing.** STUN runs no keepalives, so the
  interface reads up with a dead far end, an unplugged cable or no radio at all.
  It read `up/up` through an entire afternoon of a completely silent link. Under
  HDLC with a keepalive it only comes up if frames genuinely return, which is
  why the loopback test in `docs/P25-PLANNING.md` works and is the best
  instrument this project has for a synchronous serial link.
- **`show stun` reading `closed` carries no information.** Cisco's own guide
  prints a `closed` circuit alongside 5,729 received packets. The counters carry
  everything; the word carries nothing.
- **A station says what is wrong with it, on a screen nobody had opened.**
  `Station is Currently ACCESS DISABLED` prints at the foot of every RSS
  Alignment screen, and six rounds of counter tests were run at the router
  before anyone looked. The Service screens — Status Report, Status Panel,
  Version — are the first place to go when a radio appears silent.

**History has been rewritten twice**, and every hash from before a rewrite no
longer resolves. On 2026-09-09, to remove a product name. On **2026-09-19**, to
remove other operators' names, towns and home addresses, the household traffic
in three P25 captures, and every public address, with `scripts/scrub-history.py`
deriving what to replace rather than storing it. The GitHub repository was then
deleted and recreated so nothing unscrubbed stayed reachable by hash. Old hashes
are a record of what happened, not something to look up.

---

## The repository is public

**Public since 2026-09-20**, with the `qsp` and `qsp-zello` packages public too.
What went before it, in order, so the reasons survive:

- **The captures carried a household** (0419): mDNS, Syncthing and SSDP from the
  operator's network in three P25 captures. Filtered to radio ports; two tests in
  `internal/p25link` refuse any capture with non-radio traffic or an identity.
- **No public address anywhere** (0420): another operator's home address was
  still in ten files. The rule is now none at all, enforced by
  `cmd/qsp/addresses_test.go`, including public infrastructure.
- **The history rewrite** (0421, run 2026-09-19): 428 commits preserved,
  verified clean in every object, commit message and capture.
- **The pipeline** was first seen to work on `v0.1.263`, after the recreated
  repository's Actions token was given write access and the old package, built
  from the unscrubbed tree, was deleted.

**On Fedora in `~/Documents/QSP`:** `qsp-0.1.263-rewritten.bundle` is the
scrubbed history, also copied to the test server. `qsp-pre-scrub.bundle` is the
**unscrubbed** one and still holds the data removed from the repository; delete
it once nothing needs it.

---

## Where things stand

**Zello is on the air, both ways, on real radios**, linked to TG2 on production
since 2026-09-16, heard on the Homebrew hotspots and on the Motorola repeaters
whose owners agreed. **On Motorola repeaters it now sounds right**: 0.1.268
paces Zello audio at 60 ms, which removed an echo on every call, and 0.1.269
fills a stall Zello makes with silence rather than leaving the repeater to
repeat audio. Both confirmed by ear on 2026-09-21.

**Versions, as of 2026-09-28** (check with `-version` before trusting these;
they go stale with the next deploy):

| Where | Runs | Confirmed how |
|---|---|---|
| **Fedora working tree** | 0.1.285 | `cat VERSION` |
| **GitHub** `main` | 0.1.279, tagged `v0.1.279`; images published for it | Actions green, then an anonymous `podman pull` on Fedora reporting `0.1.279 (v0.1.279)`, 2026-09-28 |
| **Production** (systemd, 192.168.1.247) | **QSP 0.1.284** from 2026-09-28, `qsp-zello` 0.1.240, AMBEserver as `ambeserver.service` | `qsp -version` |
| **Test server** (Docker, 192.168.1.27) | 0.1.257 built from source | the container |

**The compose files pin the working tree's version, enforced by
`TestThePublishedImageIsPinnedToThisVersion`**, so between tags `main` names an
image that is not published. The documented install builds from source rather
than pulling, so this only bites somebody who pulls — but it is the reason to
tag when you push, and the coupling is deliberate: a routine `docker compose
pull` must not become an unannounced upgrade of a live repeater network.

**Proven on hardware:**
- Zello both ways on Homebrew and Motorola repeaters (2026-09-16).
- **A replugged dongle recovers with nobody touching it** (2026-09-20): the
  udev rule fired, `ambeserver-replug.service` restarted AMBEserver in the same
  second, and Zello audio followed 26 seconds later.
- **Zello across two containers** (2026-09-19, test server): the logon crossed
  the shared socket and the connector reached Zello. USRP audio between the two
  containers was not exercised.
- **The Talker Alias on the wire** (2026-09-21): MMDVMHost decoded `KD9BXO`
  from production's own transmissions.

**Not yet confirmed:**
- **The arm64 images on a Raspberry Pi** — the operator is setting one up.
- **The Talker Alias on a radio's display** — waiting on a registered DMR ID for
  the gateway; 9898 is nobody's.
- **The Zello level toward Zello set by ear** — it is at 0 dB.

**On production, set by hand and also in the repository:** AMBEserver as
`/etc/systemd/system/ambeserver.service`, the FTDI latency and replug rules in
`/etc/udev/rules.d/`, `ambeserver-replug.service`, and the polkit rule in
`/etc/polkit-1/rules.d/`.

**The rollback binary** is `~/qsp-rollback-<version>` in the operator's home on
production, the version it replaced — `~/qsp-rollback-0.1.268` today.

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

0. **A data race in the call tracker that can crash QSP.**
   `peers.Listener.ObserveFromIPSC` runs on the IPSC listener's goroutine and
   calls `calls.Tracker.Update` directly, while the serve loop calls `Update`
   and `Expire` on its own; the tracker is documented as not safe for
   concurrent use, and Go treats concurrent map writes as fatal rather than
   recoverable. So a Motorola repeater transmitting while a hotspot does, or as
   the sweep runs, can end the process. The race detector finds it in seconds
   when a test calls `ObserveFromIPSC` beside a running listener; the existing
   tests happen not to. Found while writing 0440's tests, which were changed to
   avoid it rather than fixing it in passing. **Propose a design before
   touching it** — a lock around the tracker, or handing IPSC frames to the
   serve loop, which ADR-0002 would prefer — and check the production journal
   for `concurrent map writes` first, because an unexplained restart would be
   this.
1. **Set the level toward Zello by ear** (0407): start "Level toward Zello" at
   +10, restart QSP, ask the Zello users. DMR audio measured 13 dB under Zello.
2. **See the Talker Alias on a radio**, once the gateway has a registered DMR
   ID. A registered ID also names Zello calls on dashboards and in contact
   lists, which an alias cannot. Set both on the Zello page.
3. **The Quantar link, ADR-0060. Waiting on a replacement card from KD9EJA.**
   **The station's V.24 interface has never been alive: its AUX LED has never
   lit**, which is the board's own report and was identified on 2026-09-27. A
   card is on the way; which one was not known when this was written, and what
   to re-check depends on it — see `docs/P25-PLANNING.md`. An earlier reading
   that the TTN4010 was simply *not fitted* was wrong: both front-panel RJ-45
   jacks belong to that card, so its presence was never in doubt.

   **Check the AUX LED before anything else** on this path. Everything else was
   proven, each with its own instrument:

   | Proven | How |
   |---|---|
   | Router configuration | matches the published build line for line |
   | Serial path, cable, hood, 9600 clock | HDLC loopback: `up (looped)`, 552 bytes, **zero errors** |
   | Codeplug | all five ASTRO settings read back from the station |
   | DIP switches | S101 switch 1 on, rest off, the published Motorola setting |

   Against that, nothing ever reached the router and **with zero framing
   errors** — two ends that cannot hear each other, not one that is mis-set. The
   dark AUX LED is what explains it: the TTN4010 is fitted, and its V.24 section
   has never run. `RT/RT Configuration` was also found disabled and corrected on
   the way.

   Also found on 2026-09-27: **`Station is Currently ACCESS DISABLED`**, printed
   at the foot of every RSS Alignment screen and unnoticed through six rounds of
   counter tests at the router. The Service screens — Status Report, Status
   Panel, Version — are where a silent station explains itself, and they are now
   the first place to look rather than the last.

   **The router is parked and correct**: `clock rate 9600`, `stun group 1` and
   `stun route all tcp 192.168.1.247`, with `shutdown` saved to startup so a
   reload parks it safely. One `no shutdown` from live.

   When the card arrives: the **bottom** V.24 port, the AUX LED, then the
   bring-up steps in `docs/P25-PLANNING.md`. The capture rig is already written
   and tested — `scripts/stun-capture.py`, which records and deliberately does
   not answer. **The Quantar link's code waits for captured bytes**, per
   ADR-0060.
4. **Talkgroup routing and contention in `internal/p25link`**: voice reads the
   talkgroup and relays to every registered gateway regardless. Waiting on a
   second gateway to demonstrate it. P25-NETWORK.md §2 and §6.
5. **The arm64 images on a Pi.** `docker pull ghcr.io/k9mls/qsp` with no login,
   then `-version`. The first time either arm64 image has run anywhere.
6. **USRP audio between two containers**, the one part of the Docker Zello
   add-on not yet exercised. Needs the DVstick moved to the test server again.

**Done since the last handover, kept here so the reasons are findable:**
- **Zello echo on Motorola repeaters** — 0426 (0.1.268). QSP sent clean single
  frames at bursty timing; a repeater that ran dry repeated audio. Paced at 60 ms.
- **Zello stalls** — 0427 (0.1.269). Filled with the measured silence frame
  rather than left to the repeater, which fills a gap by repeating audio.
- **A Motorola repeater was sent contradictory signalling** — 0425. Alias
  fragments beside a voice Link Control; the IPSC path now sends only the voice
  Link Control, which is what every captured Motorola superframe carries.
- **Talker Alias** transmitted (0422) and set on the Zello page (0424);
  **session lifetime** on Administration (0423); the **replug recovery** (0416)
  proven on hardware; the **0x81 byte** closed as unreproducible, none since
  2026-09-10.

**Three more, from 2026-09-26 and 27:**
- **Never background a `sudo` command.** `sudo tcpdump ... &` prints a job
  number and then sits suspended on `SIGTTIN` at the password prompt, forever.
  An empty capture directory then reads as "the capture caught nothing" rather
  than "the capture never existed", and `jobs` showing `Stopped` was the only
  evidence, three hours later.
- **A quoted configuration excerpt is not a command block.** One was handed over
  as evidence in a fenced block shaped exactly like a paste block, and it was
  pasted. Nothing was harmed only because the router was not in configuration
  mode.
- **A revert is a destructive edit.** `git checkout -- <file>` was used to undo
  a deliberate test breakage on a file holding uncommitted work, and silently
  destroyed it. Copy the file aside instead.

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
- **One Quantar part: a replacement V.24 card, inbound from KD9EJA.** The
  station's AUX LED has never lit. The `CAB-SS-232FC` is
  in hand and proven — `show controllers` reads the cable's own identification
  as DCE RS-232, and the HDLC loopback carried frames through it with zero
  errors. The router needs nothing more: its HWIC-2A/S carries STUN, configured
  and saved 2026-09-21, clock corrected to 9600 on 2026-09-26. **12 November**
  is when the router's evaluation licence ends; see `docs/P25-PLANNING.md` for
  the fallback, and note the card is now the only thing between here and a
  first capture.
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
