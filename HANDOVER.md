# Handover, 2026-09-27

Read `NEW-SESSION.md`, then **§8a** of `PROJECT_MEMORY.md` — how this project
finds its defects, and the thing the last two days proved again. Then **§8s**,
then **ADR-0052**, the frame everything about linking sits inside. For the text
service read **ADR-0067** and **ADR-0068** in order; for Zello and the
transcoder, **ADR-0062** through **0066** and `docs/ZELLO.md`.

## The two live threads

**1. A Quantar is linked, and QSP reads its calls (0.1.306).** On 2026-10-04
the station's **wireline board was replaced** and frames reached the router for
the first time: the Status Report screen had read `Station Wireline FW:
NOT_PRESENT`, and a V.24 card on a board nobody is talking to sends nothing.
**Read the Service screens first.** Four patches later the link is open and
stays open, and three transmissions from a handheld were captured and are the
fixture (`testdata/quantar/stun-voice-three-calls.bin`). `internal/v24link`
opens the link from both ends, keeps it alive, and reads each call as far as
talkgroup and radio; the Overview's P25 row shows it. **Nothing is relayed
yet** — that is ADR-0060 phase 4, a repeater's call reaching P25 gateways and
back, and it needs a frame QSP has never sent: voice *to* a repeater. See
`docs/P25-PLANNING.md` and ADR-0060.

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
preambles and the radio gave up. Hotspots now get one preamble per text, and
**with 0.1.285 on production the text displayed on the R7** — Pi-Star's log
showing one preamble, "7 to follow", the header with six blocks and a clean
end. So a text QSP composes now reaches both kinds of radio. Note that **the operator's R7 and the radio on the repeater both transmit
as 3132910**, so a text between those two can never display on either —
a radio discards a text from its own ID. A second radio with its own ID is
being programmed for that test.

**Private calls to hotspot radios were broken by 0443, and 0.1.287 fixes
it** (0445). The preamble gate dropped the preambles that wake a radio for a
private call's signalling, because only a data header released one; it now
holds only preambles that announce data, which is what MMDVMHost multiplies.
Confirm with one private call from the new radio (its own ID) to the R7.

**Private calls and texts across the link, 0.1.288** (0446, ADR-0069). The new
radio, 3132911, is on the Motorola repeater behind the **test server**. The R7
is on production. Private calls and texts between them went nowhere, in both
directions: routing offered a private call only to the peer the radio was
heard through, and a radio on the other server is behind none of them. A
private call to a radio not heard here is now offered to every linked QSP
server, both the links a server dialled and the servers that dialled it.
**Both servers need 0.1.288**: production to send the R7's calls out to the
test server, and the test server to send 3132911's calls up to production.
Confirm with a private call each way, then a private text each way. A private
text *composed on the Administration page* is still group-only (phase 3).

**Tested 2026-09-29 on 0.1.288:** the R7 to 3132911 crossed and was heard.
3132911 to the R7 did not, because the **test server refused the radio**:
its Docker first run had written `QSP_ALLOWED_PEERS` (3132910) as the
subscriber list too. 0.1.289 (0447) stops new installs doing that, and
**0.1.290 (0448) makes the subscriber list a ban list only**: QSP never
restricts which radios may transmit (K9MLS's decision), and an existing
allow-only list — the test server's — is opened when QSP loads it, with a
startup advisory. The test server needs nothing by hand once it is on 0.1.290.

**Confirmed on air 2026-09-29, 0.1.290 on both servers:** private calls work
both ways between the R7 and 3132911, and a private text to each radio
displayed. So the network carries group voice, group texts, private voice and
private texts between a hotspot on one server and a Motorola repeater on
another. Composing a *private* text on the Administration page (phase 3) is
**not wanted** — K9MLS, 2026-09-29: the server's texts are announcements to
everybody, which group texts already do.

**Weather alerts (0.1.292 preview, 0.1.293 transmit; ADR-0068 as amended).**
A Weather page in the console, off by default: NWS county and zone codes
(checked against NWS, with the name shown), alert types ticked by name, a
talkgroup, timeslot and sender, and a contact email for NWS. Watching, QSP
reads NWS once a minute and shows every alert with what it decided and how it
reads on a radio. **Put alerts on the air** sends them as group texts — **to
this server's own hotspots and repeaters only, never to linked servers or
bridged networks** (K9MLS: weather is local) — paced at most six in ten
minutes, warnings first. **Send test** puts one marked test on the air. On
0.1.292 production read the real feed correctly on its first poll (a Flood
Watch for Denton, held as not chosen). On 0.1.293 Send test reached the R7,
and then two days of flooding sent nothing: exact alert names, a silent
baseline on switching on, and every update held. **0.1.294 opens it up**
(K9MLS: it has to work): every warning and every watch by default, what is
switched on at the page goes out within a minute, an extended warning is sent
again, and only a restart is silent. **Not yet seen on air: a real alert.**

**P25 from a hotspot on the LAN** broke on 2026-09-29 when 10297 appeared in
the downloaded P25Hosts.txt as `qsp.hopto.me` and overrode the local entry.
Fixed on the Pi-Star with one `/etc/hosts` line; `docs/P25-GATEWAY.md` has it.

**What to deploy, and why.** Production runs 0.1.293 and the test server
0.1.290. **0.1.294 makes weather alerts actually go out**, and production
needs it. **0.1.295 is the first patch from the 2026-10-03 bug hunt and both
servers need it**: one IPSC datagram could stop the server and one login
request could take any hotspot off the air. The hunt's full list, and what is
still open, is in the CHANGELOG under Security and in the session that
produced it. **0.1.296 is the weather findings** (a watch upgraded to a
warning, a warning extended a little at a time, a warning issued in the first
minute after a restart). **0.1.297 is the routing findings**: a text reached
a link as its first burst only, and a talkgroup allow list refused every
private call. **0.1.298 is the rest of the security findings**: the setup
token behind a proxy, addresses on the event stream, and bans that did not
apply to a connected repeater or over a link.

**0.1.299 is the long-uptime findings**: links look their far end up again
(and a name that does not resolve no longer stops QSP starting), SQLite's
settings reach every pooled connection, validation refuses what the server
would not start with, and a save that could not be applied live is reported
as saved. **The pragma change could only be tested here against a recording
driver; Fedora's check.sh is the first run against real SQLite.**

**0.1.300 is most of the rest**, built by four agents in parallel and merged:
the configuration is cloned wherever it is handed out; the full backup
carries the password files (format 2); console logins are throttled by
source address rather than by account (ADR-0026 amended); the three causes of
the Zello "USRP audio stopped without a release" line; weather alerts with no
set end read "until further notice" (from the live feed); retention zero
prunes. **Not run here, and Fedora's check.sh is the first real run:** the
zello-tagged connector against real libopus, everything SQLite-backed, and
Go 1.27 itself.

**0.1.301 came from production's own log**, not the review: a call that a
hotspot restarts mid-transmission without a header is carried as the call it
belongs to (it had been arriving as radio 5002016 on talkgroup 4929869, the
Talker Alias read as IDs, from peer 3132913), and `DMRA` Talker Alias
datagrams are accepted rather than counted as ignored. **To confirm on air:**
the line "a transmission restarted without a header" should appear where
"4929869" used to, and the overview's ignored count should stay at zero.

**0.1.308 sends a gateway's call to the repeaters**: start marker, the
gateway's frames behind `07 03`, end marker twice; ended after a second of
silence if no terminator comes. **Never accepted by a repeater yet.** To
confirm on air: key a P25 radio into a hotspot linked to port 41000 and hear
it from the Quantar; the Overview's repeater line counts "sent to it". If the
Quantar stays silent, turn on **Send a call header to repeaters** and restart;
if it then transmits, the header is required and a true one must be computed
(it names talkgroup 1 today). **Pacing is not built**: fine on a LAN, expected
to matter across the internet.

**The network this is for**: repeaters in Idaho, Wisconsin and Texas on one
QSP, linked like IPSC. Needs, in order: this patch proven; pacing; a second
repeater for the first repeater-to-repeater call; the tunnel inside a VPN,
because STUN has no authentication or encryption; a site guide.

**0.1.307 carries a repeater's calls out** to every P25 gateway and every
other repeater, one call at a time (`p25link.Floor`, shared by both
listeners). Tunnels are per connection now, so one router can link several
repeaters. **To confirm on air**: key through the repeater with a P25 hotspot
linked to port 41000 and hear it there; the Overview's repeater line should
count "carried" alongside "heard". **Not built**: a gateway's call to the
repeater. **Never run**: repeater to repeater.

**0.1.306 reads a repeater's calls and renames the link**: Motorola P25
repeaters, `p25_repeaters`, `internal/v24link`; the old `quantar` section
still loads. The Overview's P25 row and the health page show the repeater.
**To confirm on air**: the Overview line, and "a transmission ended" in the
log with the talkgroup, radio and seconds.

**0.1.305 opens the link from both ends**: QSP sends its own link request
until the station accepts it, and introduces itself only then; 0.1.304's
introduction, sent on a link open one way, was ignored 65 times. **Present
as** (`quantar.present_as`) switches QSP between the repeater form and the
published console form without a build. **Proven on the Quantar the same day**:
the link came up in the repeater form and stayed up.

**0.1.304 opens the Quantar's link**: the station's introduction is answered
(`quantar.site`, 2 unless set) and Receive Ready goes out every two seconds.
**Both are reasoned and unproven on the station**; the log line "the Quantar's
link is up" is the proof, and "the link dropped" the disproof.

**0.1.303 answers a Quantar** (ADR-0060 phase 2): `internal/v24link` listens
for a router's serial tunnel on TCP, accepts the station's link request and
records every frame. Off by default; the **Network** page has the switch.
**Unproven on the station** until it is deployed and the log is read.

**0.1.302 changes no code a station runs**: the README gains a picture of
the Overview, made from invented stations by
`scripts/overview-screenshot/run.sh` (regenerate it when the page changes;
it shows the version), and `build/`, where the Zello connector is compiled,
is ignored by git and by the address scan, which a leftover binary there had
failed.

**Still open from the hunt**: a hotspot can claim to be a linked QSP server
and is then offered private calls for radios not yet located (needs linked
servers to prove who they are — K9MLS to decide); the connector's own
QSP-to-Zello queue can still drop a release when full; a callsign is cached
for ever once known; two administrators saving stale forms overwrite each
other silently; no console page calls full restore, so its new report of
written and missing password files is API-only. The reviewers' full reports are in the 2026-10-03 session.
For the record, 0.1.287 is the hotspot private-call fix and 0.1.286
closed a crash risk that has been live since Motorola repeaters were first observed:
the call tracker was written from several goroutines at once, and Go treats
that as fatal. The journal showed no `concurrent map writes` since
2026-09-16, so it had not fired, but it could at any moment a repeater and a
hotspot transmitted together. Deploy when it suits, by the usual path below.

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

**Versions, as of 2026-09-29** (check with `-version` before trusting these;
they go stale with the next deploy):

| Where | Runs | Confirmed how |
|---|---|---|
| **Fedora working tree** | 0.1.308 | `cat VERSION` |
| **GitHub** `main` | 0.1.302, tagged `v0.1.302`, pushed 2026-10-03 | K9MLS's report; Actions green for v0.1.301, not confirmed for v0.1.302 |
| **Production** (systemd, 192.168.1.247) | **QSP 0.1.305** from 2026-10-04, Motorola repeater link on, `qsp-zello` 0.1.300, AMBEserver as `ambeserver.service` | `qsp -version` over ssh; all three services active; Zello tested both ways |
| **Test server** (Docker, 192.168.1.27) | 0.1.301 built from source, 2026-10-03, checkout at `~/qsp` reset to the bundle | `docker exec qsp /qsp -version`. Its log is text, not JSON: grep `msg=starting`, not `"starting"` |

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
production, the version it replaced — `~/qsp-rollback-0.1.288` as of 2026-09-29,
with `~/qsp-rollback-0.1.287`, `~/qsp-rollback-0.1.284` and `~/qsp-rollback-0.1.269`
still there from earlier installs.

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

1. **Set the level toward Zello by ear** (0407): start "Level toward Zello" at
   +10, restart QSP, ask the Zello users. DMR audio measured 13 dB under Zello.
2. **See the Talker Alias on a radio**, once the gateway has a registered DMR
   ID. A registered ID also names Zello calls on dashboards and in contact
   lists, which an alias cannot. Set both on the Zello page.
3. **Motorola P25 repeaters, ADR-0060: phases 1 to 3 are done; phase 4 is
   next.** The link opened on 0.1.305 at the first try of the both-ends
   handshake: request and acceptance each way, an introduction each way, then
   Receive Ready (the repeater's every 5.01 s, QSP's every 2 s). 0.1.306 reads
   the calls. **To confirm on the console**: key a P25 radio and the Overview's
   P25 row should count voice frames and say "Motorola repeater (Quantar), site
   1, link up, TG …, last heard …". **To confirm with the operator**: the
   capture reads talkgroup **1** and radio **8080303**; he was asked what the
   radio was set to and had not answered when this was written.

   **Phase 4, relay**: (a) a repeater's voice to every registered P25 gateway
   and every other repeater — **built in 0.1.307**; (b) a gateway's voice to
   the repeater, which needs the start marker, header and end marker QSP has only
   ever received — reuse the captured forms, and expect one round of the
   repeater refusing them. **The IMBE crosses untouched both ways.** Talkgroup
   contention between the two is item 4 below.

   **Not in Last heard**, and not by oversight: ADR-0059 is the operator's
   decision.

   What 2026-10-04 proved, each with its instrument:

   | Proven | How |
   |---|---|
   | Router, 9600 clock, cable, adapter data path | HDLC loopback: `up (looped)`, 92 packets, **zero errors** |
   | Adapter clock wires | `External Transmit Clock: ENABLED`, 49 packets, zero errors |
   | V.24 card (TTN4010D) | -9.46 V idle on DB-25 pin 2; S101 and S102 switch 1 on, rest off; **bottom** jack |
   | Codeplug | all five ASTRO settings read back; `ASTRO CAI CAPABLE`, `Astro To Wireline: ENABLED` |
   | Wireline board | replaced; before, `Station Wireline FW: NOT_PRESENT` |
   | Tunnel framing | `testdata/quantar/stun-link-request.bin` |

   **The router** is `Router1`, a Cisco 2921, reached with `ssh router` from the
   test server as user `mike`. **Its address is from DHCP and has changed once**
   (now 192.168.1.44); `stun peer-name` must equal it, so give it a reservation.
   Its running configuration has `stun peer-name 192.168.1.44` and
   `stun route all tcp 192.168.1.247`, **not saved**: a reload returns it to a
   peer name of .45 and the tunnel will not open. `write memory` once the link
   is proven. Changing `encapsulation` on the serial interface drops the two
   `stun` lines; re-enter them.

   Next: phase 3, voice captured and parsed, from QSP's own record.
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
- **What the P25 radio was set to on 2026-10-04**, to confirm the talkgroup (1)
  and radio ID (8080303) QSP reads from the repeater. And **12 November** is
  when the router's evaluation licence ends; see `docs/P25-PLANNING.md`.
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
the bundle and builds its own image. On the **TEST SERVER**, in its checkout:
`git pull /tmp/qsp-NNNN.bundle main`, then from `deploy/docker` rebuild with
the same compose files it was started with, adding `--build` to `up -d`
(`docker-compose.build.yml`, plus the Zello pair if the connector runs there).

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
