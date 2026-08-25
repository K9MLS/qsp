# QSP — Project Blueprint v0.4 (DRAFT FOR COMMENT)

<!-- doc-accuracy: frozen — v0.4 records what was believed before any code existed; rewriting it would destroy the record rather than correct it. Current state lives in PROJECT_MEMORY.md and ARCHITECTURE.md. -->

> **This document is frozen at v0.4 and is not a description of the current
> build.** It is the product specification as it stood before implementation
> began, kept for the record. Every statement about what exists was true in
> 2026-08 and much of it no longer is — phases 1 and 3 are built. For what QSP
> does today see [`PROJECT_MEMORY.md`](PROJECT_MEMORY.md) and
> [`ARCHITECTURE.md`](ARCHITECTURE.md).


**The most premium DMR bridge amateur radio has ever had. Free forever.**

> **QSP** — the Q-code for *"I will relay your message."* Three letters. Exactly what it does.
> *Name still provisional pending final sign-off.*

**Status:** Pre-build. No code written.
**Author:** Mike, K9MLS
**License:** GPL-3.0 — free forever, every ham, no paid tier, ever.
**Ask:** Tell us what's wrong with this. Especially Section 14.

**Changes from v0.3:**
- **P25 position revised.** We don't rebuild the wire protocol, but we *do* own the setup experience. DVM being technically excellent and painful to configure is our thesis, not a reason to walk away (§5).
- **AllStar and Zello are now full bidirectional v1 features.** Not listen-only, not deferred (§6).
- **Vocoder pool architecture added** — the real engineering problem, with hard capacity limits (§7).
- **Transcoding policy is now an admin decision, exposed in the UI** (§8).
- **Engineering standards section added** (§13).

---

## 1. The Problem

Every tool in this space is technically capable and operationally miserable.

| Tool | Technically | Operationally |
|---|---|---|
| **a commercial DMR server** | Excellent | Proprietary, dealer-quoted, 128-page manual, hostile UI |
| HBlink3/4 | Correct, solid | Hand-edit `.cfg` and `rules.py`. README says "for tinkerers" |
| FreeDMR | Works | Docker + 5 containers. Breaks → debug container networking |
| DMRlink | Real IPSC implementation | No scheduling, no triggering, all rules static (author's own FAQ) |
| P25Reflector | Works, widely used | Compile from source, edit INI, request numbers by hand |
| **DVMProject** | **Outstanding** — production P25, Quantar V.24, FNE core | **Genuinely painful to configure.** The provisioning webtool for dvmfne is archived |
| DVSwitch | Bridges everything | Two INI files with port fields whose *names don't agree*, and you must match them by hand |

That last row deserves its own callout, because it's the clearest evidence the gap is real:

```
DVSwitch.ini          Analog_Bridge.ini
[DMR]                 [AMBE_AUDIO]
TXPort  = 31100   →   fromDMRPort = 31100
RXPort  = 31103   →   toDMRPort   = 31103
```

Published guides warn about this specific footgun. That is not a documentation problem. That is a **missing product**.

**Thesis:** the protocols are solved. The operations are not. **We are building the operations layer.**

---

## 2. Non-Goals

- ❌ **No IPSC.** Proprietary Motorola. Repeaters join via N0MJS's `ipsc2hbp` and appear as normal HBP peers.
- ❌ **No XNL/XCMP.** Motorola's *encrypted radio-control* protocol — programming and diagnostics, not voice. DMRlink's FAQ notes it involves encrypted keys and that DMRlink simply cannot speak it. Implementing it means defeating an authentication mechanism, which is a different legal category from implementing an open voice protocol. **Permanent no**, and we don't need it: connection state, ping RTT, packet loss, last heard, and options string all come free over the Homebrew Protocol.
- ❌ **No V.24/DFSI hardware layer.** DVMProject owns this and does it well (§5).
- ❌ **We don't ship a vocoder.** We orchestrate the admin's (§7).
- ❌ **We are not a network.** We're software.
- ❌ **No paid tier. Ever.**

---

## 3. Architecture

**One binary. Two digital protocols. One routing engine. One USRP bus. One console.**

```
 DMR peers                    P25 peers                  DVM networks
 (MMDVM, hotspots,            (P25Gateway,               (Quantar / GTR
  DMRGateway, Pi-Star)         MMDVM)                     via dvmfne)
      │                            │                            │
      │ HBP UDP 62031              │ P25 UDP 41000              │ FNE peer
      │ AMBE+2 passthrough         │ IMBE passthrough           │
      ▼                            ▼                            ▼
 ┌──────────────────────────────────────────────────────────────────┐
 │                     UNIFIED ROUTING ENGINE                        │
 │  talkgroup × timeslot × peer  •  always-on / scheduled / PTT      │
 │  ACLs  •  hang time  •  priority  •  failsafe unlink              │
 └───────────────┬──────────────────────────────────┬───────────────┘
                 │                                  │
        OpenBridge peering              ┌───────────▼───────────┐
        (UDP 62035)                     │   VOCODER POOL (§7)   │
        → QSP instances                 │  AMBE ⇄ PCM, N chans  │
        → BrandMeister                  │  allocate / queue     │
        → a commercial DMR server via cc2obp           └───────────┬───────────┘
                                                    │
                                        ┌───────────▼───────────┐
                                        │     USRP AUDIO BUS    │
                                        │   8 kHz S16 PCM/UDP   │
                                        └──┬─────────┬──────────┘
                                           │         │
                                      AllStar     Zello
                                     (chan_usrp)  (Channels API)
                                           │
                                      EchoLink
                                    (via ASL3 chan_echolink)
```

### The headline feature

**Scheduled and PTT-triggered bridging.** a commercial DMR server offers talkgroups on always-on, scheduled, or on-demand (PTT) basis. Every free alternative is static-only. A club wanting a net linked Tuesdays 20:00–21:30, or a talkgroup that bridges only when someone keys up, currently has to buy a a commercial DMR server.

---

## 4. Locked Decisions

| Decision | Choice |
|---|---|
| **Audience** | Clubs first, hotspots second |
| **Language** | **Go — final.** Core is pure Go, statically linked. cgo isolated to the optional Opus module (§6) |
| **Host OS** | Ubuntu Server 24.04 LTS |
| **Install** | Docker Compose (primary) + raw binary + `.deb` |
| **Config** | Web UI → versioned JSON. **The user never edits a config file** |
| **Storage** | SQLite, single file |
| **Console** | Static HTML/CSS/JS, `go:embed`, no build step |
| **Live updates** | Server-Sent Events |
| **License** | GPL-3.0 |

---

## 5. P25 & DVM — Revised Position

**v0.3 said "peer, don't rebuild." That was half right, and the half it got wrong matters.**

DVMProject genuinely solves the hard part. `dvmhost` provides a P25 TIA/V.24 interface for commercial hardware via V.24 DFSI modem hardware or UDP, plus `dvmfne` as the network core. It's in production: the **Indiana P25 Network** runs on DVMProject with the converged FNE as its core and DVMDFSI at each site, most sites running **Motorola Quantar** base stations. W3AXL sells a DVM-V24 USB converter — synchronous 9600-baud RS232 HDLC from V.24 RJ45 into USB-C — **confirmed working with Quantar/Quantro**.

**But it is a pain to set up, and that is exactly the problem we exist to solve.** DVMProject's own provisioning webtool for dvmfne is archived. The configuration burden is real and unaddressed.

**So the position is:**

| Layer | Who owns it |
|---|---|
| V.24 / DFSI wire protocol and hardware | **DVMProject.** We don't touch it. Deep, hardware-adjacent, solved well |
| FNE network peering | **Shared.** A DVM network is a peer type in our routing matrix |
| **Configuration, provisioning, monitoring** | **Us.** Guided setup, generated `dvmhost` config per site, live health in our console |

We become the thing that makes DVM approachable, without rebuilding a byte of it. That respects their work and serves the operator.

**Action item before building:** talk to DVMProject. We want their blessing and ideally their input on the config schema. Ask, don't assume.

**Hardware honesty:**

| Hardware | Status |
|---|---|
| Quantar / Quantro | **Proven.** Production today, off-the-shelf V.24 adapters |
| GTR 8000 | **Plausible, unvalidated.** DFSI is IP-based over 100Base-T, UDP for control and RTP for voice with IMBE audio; GTR supports V.24. But GTRs in ham hands are rare and generally expect ASTRO 25 infrastructure. **Nobody should assume this works until someone here proves it** |

---

## 6. Analog Connectors — Full Bidirectional, in v1

**v0.3 deferred these. Reversed. They ship in v1, full duplex, no listen-only compromise.**

The connectors are genuinely cheap. USRP includes signaling (TX/RX transitions) and audio transfer over UDP, with audio as **8 kHz signed 16-bit PCM**. That's a few hundred lines of Go with zero dependencies. And the ecosystem already points at it — the reference Zello bridge is tested against AllStarLink `chan_usrp`, DVSwitch `Analog_Bridge`, MMDVM_CM `USRP2DMR`/`USRP2YSF`, and SvxLink.

| Connector | Difficulty | Notes |
|---|---|---|
| **USRP bus** | Easy | ~200 lines. UDP, PCM, TX/RX flags |
| **AllStar** | Easy | ASL speaks USRP natively. **We write zero Asterisk code** — USRP endpoint plus a *generated* `rpt.conf` stanza the admin copies |
| **Zello** | Medium | WebSocket + JSON + Opus. Works with **Zello Free and Zello Work** |
| **EchoLink** | Medium-Hard | Protocol is fine; the policy is the friction |

### Zello specifics

Setup is documented and achievable: create a dedicated account for the bridge, give it talk and listen permission on the channel, convert it to a developer account at the Zello Developers Console, and add a key to obtain an **Issuer** and **Private Key**. Then WebSocket to `wss://zello.io/ws`. The reference bridge runs on **1 vCPU / 1 GB RAM on both AMD64 and ARM**.

**Architectural cost, stated plainly:** Opus in Go requires cgo, which breaks the pure-static-binary property that justified choosing Go. **Resolution:** the Zello connector ships as a *separate optional binary* in the same repo and container. Core stays pure Go and statically linked. Clubs that don't use Zello never see it.

### EchoLink specifics

Protocol is UDP 5198/5199 with GSM 6.10 and TCP 5200 for directory; open implementations exist (Qtel, SvxLink, ASL3's `chan_echolink`). The friction is community policy: EchoLink **requires positive proof of license and identity before a callsign is added to the validated user list**, and there is long-standing sensitivity about interconnection — the IRLP community has held that EchoLink's callsign validation is not accepted as adequate by all parties.

**Approach:** v1 routes EchoLink through **ASL3's `chan_echolink`** rather than implementing the protocol ourselves — we generate the config, ASL does the talking. Native implementation only after a conversation with the EchoLink folks. This is a *courtesy* decision, not a capability limit.

---

## 7. The Vocoder Pool — The Real Engineering Problem

**Full bidirectional analog bridging is the hard part of this project.** Not the connectors. This.

### The asymmetry

| Direction | Status |
|---|---|
| AMBE **decode** (DMR → PCM) | **Software available.** Analog_Bridge's `decoderFallBack` uses a software MBE decoder / OP25 IMBE-AMBE vocoder |
| AMBE **encode** (PCM → DMR) | **No open software implementation.** Requires a hardware vocoder or `md380-emu` |

Bidirectional therefore requires a vocoder resource. This is a real, permanent hardware/software requirement — it is not something clever code eliminates.

### Capacity is finite and small

This is the constraint nobody documents and everyone discovers the hard way:

| Device | Simultaneous streams |
|---|---|
| AMBE-3000 (ThumbDV, DVstick 30) | **1** |
| AMBE-3003 (DVstick 33) | **3** — explicitly sold to transcode three streams at once |
| `md380-emu` | CPU-bound, N instances per core, quality/legal caveats |

**A club bridge with four simultaneous transcoded talkgroups needs four vocoder channels.** A single dongle serves one call at a time. This must be a first-class concept in the software, not an afterthought.

### Pool design

- **Resource registry.** Enumerate available channels: hardware devices (with channel count), `md380-emu` instances, software decoders.
- **Allocation on stream start, release on stream end.** Sub-frame latency budget.
- **Live capacity in the console:** *"3 of 4 vocoder channels in use."* Visible on the home screen, always.
- **Overflow policy — admin's choice:** queue the call, drop it with a log entry, or degrade to decode-only for that stream.
- **Health checks.** Dongle disconnect detection with a clear alarm, not silent failure.
- **Counterfeit chip warning.** The community is explicit that only genuine DVSI chips work correctly and unlicensed clones may not. The UI surfaces device identity so an operator can tell what they actually bought.

### Our policy, unchanged

**We ship no vocoder code.** The admin supplies hardware or their own `md380-emu`. We detect it, pool it, allocate it, monitor it, and show its capacity. Nothing DVSI-derived ever enters this repository.

---

## 8. Transcoding Policy — The Admin Decides

**Tandem coding degrades audio.** AMBE → PCM → Opus → PCM → AMBE is lossy at every hop, and it's why some clubs refuse bridges on their repeaters.

**v0.3 treated that as a reason to avoid the feature. Wrong call. It's a reason to give the operator control.**

| Control | Scope | Default |
|---|---|---|
| Transcoding allowed | Per bridge | Off — opt in deliberately |
| **Accept transcoded audio** | **Per repeater** | **Off** — a repeater owner can refuse transcoded traffic outright, and the routing engine enforces it |
| RX gain / AGC | Per bridge | Unity |
| TX gain | Per bridge | Unity |
| Overflow behavior | Per bridge | Queue |
| Quality warning banner | Per bridge | Shown |

**Every call that crossed a vocoder is badged `TRANSCODED` in the live feed and the last-heard log.** If a club is debating audio quality, they can see exactly which traffic is which instead of arguing from memory.

Per-repeater refusal is the important one. It means a repeater owner who joins a QSP network is never surprised by transcoded audio landing on their machine — they opt in, in writing, in the UI.

---

## 9. Administration

### Roles — four

| Role | Can | Cannot |
|---|---|---|
| **Owner** | Everything; can't be locked out | — |
| **Admin** | Config, routing, peers, users, vocoder pool | Remove Owner |
| **Net Control** | Link/unlink, start/stop/extend scheduled events | Change any configuration |
| **Viewer** | Dashboard, last-heard | Anything — safe to share publicly |

**Net Control screen:** one page, big touch targets, phone-friendly. Tonight's net, its state, **LINK / UNLINK / EXTEND 15 MIN**, live talker list. No configuration reachable.

### Scheduler

Calendar with recurrence plus one-offs. Non-negotiables:

- **Timezone and DST handled correctly and shown explicitly.** Store UTC, display local, print the **next three fire times in plain English**. This is where schedulers die.
- **Conflict detection before save.**
- **Dry run** — "here's exactly what links and unlinks over the next 7 days."
- **★ Failsafe unlink.** Every scheduled link carries a hard maximum duration. A server restart mid-net must **never** leave a bridge welded open. Impossible by construction, not by discipline.
- **Manual override that survives the schedule.**
- **"What's happening tonight"** always visible on the home screen.

### Config versioning and rollback

*Straight from operator feedback: "backing up my configuration has saved me a lot of problems."*

- Every save writes a snapshot. Timeline: *"Changed TS2 ACL — 3 days ago — by K9MLS."*
- One click to diff. One click to roll back.
- Export the whole config as a single file. Restore onto a fresh install in one step.
- **A dead server becomes a ten-minute recovery.**

### Every field shows its recommended value

*"I have not known the defaults or recommendations, so the wrong value is my mistake."*

Inline, visible, next to the input, with one-click restore-to-default. Nobody should ever have to guess what a sane value is.

### Audit log

Who changed what, when, from where.

---

## 10. Console Design

*Generated with the UI/UX Pro Max design intelligence system. Product type: real-time operations console. Dials — variance 7, motion 4, density 8.*

**Style:** Modern Dark — dark-primary, cinematic, layered, premium. **Listed anti-patterns to avoid: cheap visuals, and animations that are too fast.** Premium reads as deliberate.

### Tokens

| Role | Hex |
|---|---|
| Background | `#0F172A` |
| Muted surface | `#1F1E27` |
| Foreground | `#FFFFFF` |
| Primary | `#D97706` amber |
| Secondary | `#F59E0B` |
| Accent | `#6366F1` indigo |
| Destructive | `#DC2626` |
| Border | `rgba(255,255,255,0.08)` |

Status semantics on top: green healthy, amber degraded/pending, red down. **Never color alone** — always paired with shape or label.

**Type:** Inter (UI/body, 300–700) + Fira Code (IDs, callsigns, talkgroups — tabular figures, unambiguous `0`/`O`).

**Motion:** `Expo.out` — `cubic-bezier(0.16, 1, 0.3, 1)`, 300–450 ms. Stagger 60 ms, `opacity 0→1`, `scale 0.92→1`, `y +16→0`. **No overshoot easing on data tables** — it reads as sloppy on informational UI. Press feedback `scale 0.97→1.0`. **Motion depicts something real.** `prefers-reduced-motion` freezes rather than degrades.

### Screens

1. **Home** — oversized live-call banner; **Tonight** panel; **vocoder capacity gauge**; peer grid (connected / ping RTT / last heard / packet loss); streaming activity chart
2. **Routing Matrix** — talkgroup × timeslot × peer with toggles. Badge per cell: always-on / scheduled / PTT. Inline conflict warnings before save
3. **Scheduler** — month + week views, next-three-fire-times in plain English, dry-run preview
4. **Net Control** — deliberately spartan (§9)
5. **Peers** — add/edit with full field-help
6. **Transcoding** — vocoder pool, per-bridge and per-repeater policy, live capacity
7. **Health** — port reachability, Docker networking check, clock sync, disk, vocoder device status
8. **History** — searchable, exportable last-heard with `TRANSCODED` badges
9. **Config Versions** — timeline, diff, rollback

### Charts

From the chart intelligence database, for real-time ops data:

- **Live traffic:** Streaming Area Chart, Canvas. Buffer 60–300 s, downsample older data. **Pause/resume control mandatory**; current value must also appear as a large text KPI.
- **Packet loss / RTT anomalies:** Line Chart with Highlights. Anomalies marked by **shape, not color alone**, plus text annotation per event and a summary list beside the chart.
- **Talkgroup usage:** Line Chart. Series differentiated by **line style**, not color. Under 6 series or it's noise.

### Field-level help — five layers, every input

Tooltips alone fail: hover-dependent (dead on mobile) and they hide what the user needs *before* acting.

1. **Plain-language label.** Not `TGID_TS2_ACL` — "Which talkgroups are allowed on Timeslot 2."
2. **Always-visible helper line.** One sentence.
3. **Example value**, greyed.
4. **`ⓘ` expanding inline** (not hover) — "why this matters and what breaks if you get it wrong."
5. **Live validation on blur**, error beside the field, explaining the fix. *Never validate only on submit.*

**Plus:** every field states **what happens if you leave it alone.**

### Checklist — every screen

- [ ] Custom SVG icons only — no icon fonts, no emoji-as-icon
- [ ] Contrast ≥ 4.5:1
- [ ] Visible focus rings, full keyboard nav
- [ ] `cursor-pointer` on everything clickable
- [ ] Hover transitions 150–300 ms
- [ ] `prefers-reduced-motion` respected
- [ ] Responsive 375 / 768 / 1024 / 1440
- [ ] No pure `#000000`
- [ ] Charts have pause controls and text-KPI fallbacks
- [ ] No status conveyed by color alone

---

## 11. Deployment

**Ubuntu Server 24.04 LTS + Docker Compose.** 2 vCPU / 2 GB / 20 GB runs a large club system. Add CPU headroom if running `md380-emu` instances.

### The Docker trap — handled in code

> DMR and P25 are UDP, and HBP identifies peers partly by source IP. Docker's default bridge networking NATs source addresses — **every repeater arrives looking like `172.17.0.1`.** Peer identification breaks, keepalive tracking breaks, and connections look fine while audio doesn't flow.

- **Linux** → `network_mode: host`.
- **macOS / Windows** → native binary. Docker Desktop has no host networking.
- **USB passthrough** for vocoder dongles must be declared in Compose — another silent-failure source we detect explicitly.
- **The health page says all of this in English**, with the fix.

### Why this beats a a commercial DMR server

| | a commercial DMR server | QSP |
|---|---|---|
| Repeater count | Licensed tier | Unlimited |
| Cost to add a peer | Purchase | Free |
| Config backup | Manual export | Automatic, versioned, one-click rollback |
| Move / rebuild | Support call | Compose up elsewhere, restore snapshot |
| Redundancy | Buy a second box | Another container anywhere |
| **Staging instance** | Buy another | `docker compose -f test.yml up` |

**Nobody stands up a test a commercial DMR server.** Every QSP club gets one free — so changes get tested before they hit the repeaters.

---

## 12. Defaults & Ports

Simple/Advanced progressive disclosure. Everything adjustable; every field shows its recommended value.

| Setting | Default | Source |
|---|---|---|
| Group hang time | 5 sec | HBlink convention |
| HBP master | UDP 62031 | Standard |
| OpenBridge | UDP 62035 | Standard |
| OpenBridge timeslot | TS1 only | Proper OpenBridge behavior |
| P25 | UDP 41000 | Standard reflector port |
| USRP | UDP 32001 / 34001 | ASL convention |
| Console | TCP 8080 | Behind reverse proxy for TLS |
| DMR ID | 7-digit; +2-digit SSID for bridges | Documented convention |
| Registration ACL | Valid ham IDs only | Safe default |
| Transcoding | **Off** | Opt in deliberately |

---

## 13. Engineering Standards

**A note on "perfect code."** Perfect isn't a thing anyone can promise, and claiming it would be the first shortcut. What *is* promisable: no placeholders, no TODOs, no truncated blocks, no "this should work" — and a test regime brutal enough that the bugs surface here instead of on someone's repeater.

**Code**
- Zero placeholders. Every function complete. Every error path handled.
- No panics in the hot path. Malformed UDP from the internet must never take down the server.
- Race detector clean under load. `go vet` and `staticcheck` clean.
- Core is pure Go, statically linked. cgo isolated to the optional Opus module.

**Protocol correctness**
- **Golden-frame fixtures.** Captured real HBP and P25 frames, replayed in unit tests, byte-compared.
- Explicit tests for the known killers: stream ID collisions, group hang time expiry, late entry, missing terminator frames, NAT rebind mid-call, duplicate peer IDs, simultaneous keyup on one talkgroup, malformed/truncated packets, replayed packets.
- Fuzz the packet parsers. Every one.

**Operational**
- **72-hour soak test** before any release tag, with synthetic traffic and induced failures — network drop, dongle unplug, disk full, clock jump.
- **Scheduled-net soak:** two weeks unattended, links and unlinks verified in the audit log.
- Every release ships a reproducible build.

**Verification discipline**
- `node --check` equivalents catch syntax, not undefined references. Every referenced identifier and element ID gets grepped against the source.
- Real hardware validation at every phase gate. No phase advances on a passing test suite alone.

---

## 14. Open Questions

1. **Name — QSP.** Yes or no? Runners-up: TALKPATH, PATCH, FEEDLINE.
2. **P25 operators: is talkgroup routing actually wanted?** Ham P25 is reflector-culture. Real problem, or DMR thinking imposed on a mode that's fine as it is?
3. **Has anyone linked a GTR 8000 outside an ASTRO 25 core?** §5 lists it unvalidated. We need ground truth.
4. **Vocoder hardware — what does everyone have?** Single-channel ThumbDV/DVstick 30, or three-channel DVstick 33? This sets our default pool assumptions.
5. **Per-repeater transcoded-audio refusal (§8) — is that the right default?** We default to *refuse*. Too conservative?
6. **Scheduled bridging — what's the real use case beyond nets?** And what should the calendar look like to someone running one?
7. **PTT-triggered bridging — right idle timeout, per-talkgroup or global?**
8. **What's the worst config mistake you've personally made** on HBlink / a commercial DMR server / DVM / DVSwitch? We want each one *structurally impossible*, not documented. **If you answer one question, make it this one.**
9. **Test hardware.** DMR hotspots, MMDVM repeaters, P25 gear, Quantars, vocoder dongles — who has what?
10. **Public hosted instance?** Reputation and traffic play, but a permanent support and moderation commitment.

---

## 15. Risks

| Risk | Severity | Mitigation |
|---|---|---|
| **Scope — this is now a large build** | **High** | Strict phase gates. Nothing starts before the prior gate passes. §16 |
| **Maintenance burden** | High | Small surface, brutal tests, honest README |
| **Vocoder capacity surprises operators** | High | Capacity gauge on the home screen from day one. Never a silent drop |
| **Employer IP assignment** | Medium | Written clearance before the repo goes public. No Motorola-adjacent naming |
| **Protocol edge cases** | Medium | §13 test regime |
| **Scheduler welds a bridge open** | Medium | Failsafe max-duration by construction |
| **Docker UDP NAT / USB passthrough** | Medium | Detect and explain |
| **Zello platform risk** | Medium | Separate optional binary. An API change can't break the core |
| **EchoLink community friction** | Medium | Route via ASL3 `chan_echolink` in v1. Ask before implementing natively |
| **DVMProject friction** | Medium | Talk to them *before* building. Ask, don't assume |
| **Vocoder IP** | Low | Pass-through core; admin supplies vocoder; no DVSI-derived code in repo |

---

## 16. Phases

Each phase ends at an **approval gate** with **real hardware validation**. A passing test suite is not a gate.

| Phase | Deliverable | Gate |
|---|---|---|
| **0** | This document, revised with group input | Name locked; §14 answered |
| **1** | DMR HBP master core | A real hotspot keys up and hears itself through parrot |
| **2** | Console: wizard, routing matrix, live feed, health, full field-help | A ham who's never seen it is running in under 10 minutes, unassisted |
| **3** | Scheduler + PTT-triggered bridging + config versioning | A scheduled net links and unlinks unattended for **two weeks** |
| **4** | P25 peer + DVM network peering + guided DVM provisioning | P25 and DMR live on one instance; a Quantar site configured from our UI |
| **5** | Vocoder pool + USRP bus + AllStar connector (full duplex) | Bidirectional DMR ⇄ AllStar QSO on real hardware, with capacity gauge accurate under load |
| **6** | Zello connector (full duplex) + EchoLink via ASL3 | Bidirectional QSO through both |
| **7** | Closed beta with the group | 5+ independent operators running it |
| **8** | Public repo, docs, Docker image | v1.0 |

**Nothing in Phase 1 begins until §14 is answered.**

---

## 17. Support Posture

**Use at your own risk.** Hobbyist software. No warranty. **Never for public safety or life safety critical applications** — the same framing DVMProject uses, and the right one.

"At your own risk" does not mean untested. §13 is the standard. Closed beta before public. Real hardware at every gate. No v1.0 until a scheduled net has run unattended for two weeks.

Issues welcome. No SLA. In the README on day one.

---

## 18. Feedback

Reply with numbers from §14. **Blunt beats polite** — cheap to change now, expensive after code exists.

Highest value: anything factually wrong; a feature that sounds great but is a support nightmare; anything already solved we'd duplicate; whether P25 wants talkgroup routing (Q2); GTR reality check (Q3); your worst config disaster (Q8).

---

*Credit where due: protocol work by Jonathan Naylor G4KLX, Hans Barthen DL5DI, and Torsten Schultze DG1HT; server implementations by Cort Buffington N0MJS; P25 clients and reflectors by G4KLX and contributors; V.24/DFSI and FNE work by DVMProject and W3AXL; DVSwitch tooling by N4IRS and team; the Zello bridge by Matt G4IYT, building on Rob G4ZWH. We're building a front porch on their house.*
