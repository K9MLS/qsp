# Handover, 2026-09-15

Read `NEW-SESSION.md`, then **§8a** of `PROJECT_MEMORY.md`, then **ADR-0052**,
the frame everything about linking sits inside. **ADR-0062** is the newest and
governs the transcoder work: as much as possible in QSP.

**The history was rewritten on 2026-09-09** to remove a product name from every
commit, message and path, and force-pushed. **Every commit hash predating that
no longer resolves** — they are a record of what happened, not something to look
up.

**VERSION 0.1.233, and both servers are on it.** Production (192.168.1.247,
systemd) and the test server (192.168.1.27, Docker) were deployed on
2026-09-15 from 0.1.193 and 0.1.191 respectively — a forty-version jump, taken
because the gap itself had become the risk.

Both applied **migration 6** and created a credential store. All four peers
returned within twenty seconds on production; the test server's BCARA upstream
logged back in. Nothing in the deploy changed a configuration file.

**Two things that deploy left behind.** `~/qsp-rollback-0.1.193` on Fedora is a
real 0.1.193 binary, built from commit `b5af5d3` in a worktree and verified by
its own `-version` — keep it, because the previous binary was installed over
and there was no rollback until it was rebuilt. And the schema is now at 6
where 0.1.193 expects 5; **whether it refuses a newer schema or ignores the
extra table is not known**, so `~/qsp.db.before-0.1.233` on the server and
`~/qsp-data-before-0.1.233.tar.gz` on the test server are the real safety net.

**The credential store's key is `/var/lib/qsp/secrets.key`** — 32 bytes, mode
0600, owned by `qsp`, created on first start. **Losing it loses every stored
credential**, and the recovery is re-entering them. On the test server it lives
inside the `qsp-data` volume, which survives a container replacement and dies
with `docker compose down -v`.

**`-X main.version` in the Dockerfile is not vestigial**, whatever a passing
remark on 2026-09-15 said. `main.version` is a deliberate override hook that
`buildVersion()` prefers, falling back to the `internal/buildinfo` constant —
which is why a build without the flag still reports the right number. Keep it
in the deploy process.

---

## The one thing to read before touching the dongle

**If it stops answering, unplug it physically for ten seconds.** Not a software
reset, not an ESXi detach and re-attach — the device node comes back new and the
chip is still lost. Only removing power clears it.

This was learned by wedging the operator's DVstick 30 on 2026-09-14, and the
recovery path now opens `docs/ZELLO.md`.

---

## What was settled today

**The router question, closed with evidence.** A CISCO2921/K9 on 15.4(3)M3
`universalk9` runs STUN: `stun peer-name` was accepted at the console after
self-activating the `datak9` licence, which was `RightToUse yes` all along. The
shopping list for the Quantar is now three items — **V.24 daughtercard,
HWIC-1T, CAB-SS-232FC** — and slots 0/2 and 0/3 are free.

**Sixty days from 2026-09-13**, around **12 November 2026**, is when that
evaluation licence expires. A surplus 1841 with 12.4 `adventerprisek9` has the
feature in the image with no licence and no timer.

**A correction worth keeping**: this document previously said the data licence
"gates STUN on ISR G2" and offered the 1841 as the fallback. The citation was
true and the conclusion was wrong.

**The IPSC log flood, fixed across all four sites.** A refused peer wrote a
warning per datagram — thousands of identical lines from one sender across 2–3
September. 0347 fixed one site and walked past `unrecognised datagram`, the line
directly above it in the same function, flooding harder. 0348 fixed all four
through one helper, with a gate that reads the source and requires a rate limit
around every warning a peer can provoke at the frame rate.

**`server.session_lifetime`**, defaulting to the twelve hours it always was. The
operator said he was being logged out on leaving the browser; the first
diagnosis said it could not be the lifetime. The database settled it in one row:
created 00:30, expired 12:30. Twelve hours is a good default that happens to
span a night. It had never been reachable — all three places building an auth
policy constructed `auth.Policy{}`.

**ADR-0062: as much as possible in QSP.** A thing leaves QSP only when keeping
it would break a property QSP has decided to hold. The documented four-process
Zello chain was refused; two of the four were pure overhead. Two things stay
outside and both reasons were checked: the vocoder (ADR-0034) and Opus (Zello's
API requires it, and every Go binding is cgo, which costs `CGO_ENABLED=0`, the
ARM cross-build and a dependency-free install).

---

## The dongle, proven — and a voice through it

**Real DMR audio decoded to speech the operator heard**, 2026-09-14 night. 828
vocoder frames from `testdata/ipsc/ipsc-master-voice.pcap` — the XPR8300's own
RF through a Motorola master — through the dongle at rate index 33: **zero
rejections, zero tone frames, peak 7191**, and forty comfort-noise frames that
are pauses in the transmission rather than errors. See §8r.

```sh
go run ./cmd/ambe-probe -server 192.168.1.247:2460 \
  -capture testdata/ipsc/ipsc-master-voice.pcap -wav /tmp/onair.wav
paplay /tmp/onair.wav
```

**The frame form is settled: ETSI's on-air 72 bits**, the 49 parameter bits
with ETSI's 23 correction bits behind them, which is the reason rate index 33
exists. `-frame-form params` was built as the other candidate and never needed.

**Three synthetic runs could not answer this and the fourth signal did.** A
sine proves the round trip and then the tone path, because `TD_ENABLE` is 1 at
reset whatever the pins say; clearing it gives the voice path at a third of the
input amplitude, which is a speech model fitting something that is not speech.
A model of human speech has to be tested with human speech.

## The dongle, proven — and the audio path with it

**A speech packet in produces a channel frame out.** On the evening of
2026-09-14: 160 linear samples as type `0x02`, and back came
`61 00 0b 01 01 48` with nine bytes — a channel packet, 72 bits, 3600 bps, the
DMR rate. Every packet of that run is in
`testdata/ambe/observed-exchanges.hex`, rebuilt and decoded by tests in
`internal/ambe`. **ADR-0061's boundary now has an exchange behind it rather
than a reading.** See §8q.

**Three things that were assumptions are now measurements**, all from one
zero-argument `PKT_GETCFG` that the probe sends on every run. CFG0 `0x05` is
packet mode over the UART with companding disabled, so 16-bit linear samples.
CFG2 `0xec` has PARITY_ENABLE low, so **parity is off** — and that pin has an
internal pullup, so DVMEGA ties it low deliberately, which is why stock
AMBEserver works on this board. CFG1 `0x00` means every RATE pin is low, so
**the board does not boot at the DMR rate and setting it is a precondition for
audio, not a refinement.**

**Stock AMBEserver drives the DVMEGA.** The research said it needs the
`RESETSOFTCFG` fork. `/usr/local/sbin/AMBEserver` has none of it and carried
six exchanges. **The fork exists only at `/tmp/ambefork` on the production
server and `/tmp` does not survive a reboot** — move it somewhere durable with
a version in its name.

**`AMBEserver -x` is the debug flag. `-v` prints a version and exits**, which
cost two runs that looked identical to a dead dongle. `-d` is daemonise. `-r`
sets a reset flag nobody has read yet, and the probe already sends its own
`PKT_RESET`, so two resets would be two variables. To start it for a bench
session, in the foreground:

```sh
/usr/local/sbin/AMBEserver -x -s 460800 \
  -i /dev/serial/by-id/usb-FTDI_FT230X_Basic_UART_DT04S20J-if00-port0
```

**There is no unit and nothing starts it at boot**, which is deliberate: it
binds `0.0.0.0:2460`, has no bind-address flag, and `ufw` is inactive on that
box. An unauthenticated vocoder on every interface is not something to leave
listening, and that has to be dealt with before it becomes a service.

## The dongle, proven

A **DVMEGA DVstick 30** is passed through ESXi to the production server.

- **AMBE3000F**, version `V121.E100.XXXX.C110.G514.R014.A0030608.C0020208` —
  **not the R variant** every published example shows. DVSwitch ships separate
  images per variant.
- **FTDI FT230X**, `0403:6015`, serial `DT04S20J`, bound by `ftdi_sio` first
  attempt. Use the by-id path, never `ttyUSB0`:
  `/dev/serial/by-id/usb-FTDI_FT230X_Basic_UART_DT04S20J-if00-port0`
- **460800 baud.** AMBEserver's own default is 230400.
- Stock AMBEserver from `nwdigitalradio/ambeserver-install` compiles and runs.
  **Do not run its `install.sh`** on production: it restarts udev, copies trees
  into `/`, and enables a service for a ThumbDV you do not have.
- **It binds `0.0.0.0:2460` and has no bind-address flag.** An unauthenticated
  vocoder on every interface, on a box with `ufw` inactive.

---

## What broke, and why it matters

**The probe wedged the chip by sending `61 00 02 00 0a 21`** — field `0x0a`
(`PKT_RATEP`, **twelve** bytes) with one argument byte. The chip waited for
eleven more, consumed the head of the next packet, and sat mid-field
permanently. `PKT_RATET` (`0x09`) is the one-byte index, and rate index 33 is
the DMR one — so the `21` in that packet was right all along and only the field
was wrong.

**Two wrong readings of the same bytes in one evening, one shipped as a
correction.** 0351 swapped the speech and channel types on a hypothesis; the
manual says speech is `0x02` and channel is `0x01`, which is what the code had.
0352 reverted it and found the real cause.

**The rule this leaves**: the recovery path for a device that can be wedged by a
malformed packet must be known before the first experiment.

---

## Where the next session starts

**1. The manual is read, the field table is in the tree, and the dongle has
answered.** Done in 0355 and 0356. The audio path is proved end to end — see
§8q and the section on the dongle above — so the next work here is the
transcoder link proper, not more reading.

**What is still unproven in `internal/ambe`, and marked as such**: the parity
byte's composition (the manual prints no worked example with parity and this
board has it disabled), `PKT_RATEP`'s twelve-versus-eleven, and the `CHAND4`
soft-decision field. Everything else is now backed by either the
manufacturer's printed bytes or this board's own.

`internal/ambe` holds every control, speech and channel field with its data
length, and `testdata/ambe/manual-examples.hex` holds the manufacturer's four
worked example packets. `cmd/ambe-probe` no longer builds a packet by hand.

**Read the F manual, not the R.** This document previously pointed at
`AMBE-3000R_manual.pdf` and pages 52 to 85. The board is an **AMBE3000F** and
the manuals differ where this work touches: `SK_ENABLE` and `TX_RQST` exist only
on the F, the RESET pin is an I/O on the F so a soft-reset packet pulls it low
for about 20 µs, and on the F the echo canceller and echo suppressor are
documented as **unsupported in packet mode**. The copy in use is
**version 3.7, October 2016**, `md5 f20fd488960efdfbf2ba51165c6c7718`, 111
pages, and in it printed page *N* is PDF page *N*+10. The material is on printed
pages 58 to 92, not 52 to 85.

**It cannot be fetched, only attached.** Three attempts across two documents and
three hosts all truncated at the same point — about 55 KB of extracted text,
ending mid-sentence in §5.1.5 — and the token limit made no difference. §6.6 is
not reachable from a container by fetching. The operator downloaded it on Fedora
and attached it, which is the only path that works. `doc.platan.ru` does not
answer from Denton at all; `datasheet.datasheetarchive.com` does.

**What the manual settled**, beyond the field table: parity is the exclusive-or
of every byte except the start byte and the parity byte, the identifier `0x2f`
is part of that sum, and the two parity bytes **count toward the length**
(§6.5.2). `PKT_GETCFG` (`0x36`, no arguments) returns the configuration pins as
latched at boot and **CFG2 bit 4 is `PARITY_ENABLE`** (Table 74), so parity is
now measurable rather than inferred — the probe asks on every run. Rate index
**33** is 3600/2450/1150 and Table 115's note says it is the rate interoperable
with DMR and APCO P25 half rate. Reset release to `PKT_READY` is 20 ms maximum
and 17 ms typical; a soft reset is about 7 ms; `TX_RDY` reads high for about
1 ms after a reset and must be ignored. And §4.4: send `PKT_INIT` between
unrelated audio streams to clear vocoder state, which matters for one dongle
serving consecutive transmissions from different radios.

**The manual contradicts itself in four places**, all recorded in
`internal/ambe` rather than resolved silently. `PKT_RTSTHRESH` is the one row in
Table 32 whose length column is a total and not a data length. `SAMPLES` is
`0x30` in Table 106 and `0x03` in Table 109 — Channel Packet Example 2 prints
`03 A1`, which settles it. `PKT_CHANNEL0` is given no data bytes, one, and two
in three different places; the examples' lengths only add up if it is a bare
identifier. And the prose beneath two of the four examples disagrees with the
table above it by exactly one byte in each case — `0x0144` against `0x0143` and
`0x0010` against `0x000F` — where the tables are right and all four compute
exactly. Prefer the tables.

**2. Confirmed on the wire, keep these.** Reset `61 00 01 00 33` → `...39`.
Product `61 00 01 00 30` → `0AMBE3000F`. Version `61 00 01 00 31` → the string
above. The length counts the payload, not the type byte.

**3. Packet mode is request/response**: a speech packet in produces a channel
packet out. Silence means the packet was not accepted.

---

## Waiting on the operator

- **ADR-0058** — TIA-102.BAHA-A permission. Gates DFSI only.
- **ADR-0059** — second tracker or mode-agnostic key, for P25 in Last heard.
- **Three Quantar parts**, and **12 November** for the Cisco licence.
- A **Zello account and API keys**, and the channel decision: BrandMeister
  requires moderated channels because a Zello user is not necessarily licensed
  and their audio reaches RF.

## Ready with no hardware

- **Talkgroup routing and contention in `internal/p25link`.** QSP is a flat
  reflector: every gateway hears everything and two simultaneous keyups
  interleave. Self-contained, testable against fixtures already in the tree.
- **A session-lifetime control on Administration**, so it stops being a
  file-only setting.
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

**Two traps found today.** A bare `/tmp/qsp` from three days earlier was
installed over the server binary — use versioned names. And a config can outrun
a binary: QSP rejects unknown fields, so an older binary meeting
`session_lifetime` exits rather than warns, five times, until systemd gives up.
**You cannot roll back across a config change without removing the new field
first.**
