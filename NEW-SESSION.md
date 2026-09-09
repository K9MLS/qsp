# Starting a new QSP session

Paste the text below into a new chat and attach `qsp-handoff.bundle`.

---

I'm Mike, K9MLS. I'm building **QSP**, a free open-source DMR network routing
and linking server in pure Go — a community alternative to commercial a commercial DMR server
software. The repository is `github.com/K9MLS/qsp`.

**It carries a real network.** Three stations use it: mine in Denton, Texas,
KB9TYC's in Wisconsin, Wisconsin, and AD0MI's in Post Falls, Idaho. Voice, private
calls, text messages and parrot all work on air.

**And three Motorola repeaters, on two models.** Mine in Denton, KD9EJA's
SLR5700, and KB9TYC's. **A hotspot user has heard a Motorola repeater across the
bridge** — voice header, audio and terminator, a complete DMR transmission.

**And the other direction now works too.** A repeater keying on network audio
transmitted silence on the morning of 2026-09-08; it was the timeslot, and
0271 and ADR-0051 settled it. Audio crosses a QSP-to-QSP link in both
directions with the talkgroup and the slot intact.

**It is measurable, not inferential.** `testdata/ipsc/ipsc-master-voice.pcap`
is a real Motorola master sending voice — 347 packets, 288 of them voice, from
the XPR8300's own RF — and four tests already read it. Compare what QSP sends
against it and diff. See §8m and `HANDOVER.md`.

A repeater registering with a containerised instance is now on record, and
**repeater to hotspot carries across an OpenBridge link between two QSP
instances** — a Motorola repeater heard by a hotspot user, which had never
worked before 2026-09-08.

**A link can now be agreed entirely from a console** — offered, accepted,
refused, readdressed and restarted — which until 2026-09-08 needed hand-edited
JSON on a server. It has been done on air, both directions, on TG2 TS2.

The administration page (ADR-0055) and backup and restore (ADR-0054) are built
and deployed, and `/server` is where an operator asks what this server is and
whether it matches its configuration. What is left before a second operator runs
a server is nothing; `HANDOVER.md` opens on getting one running.

**The console has about 1,500 lines of JavaScript and one check on any of it.**
Three defects shipped there on 2026-09-09 through a clean `gofmt`, `vet`,
`staticcheck` and `go test` — the gate chain is Go and reads none of it. Treat
anything an operator says looks wrong on a page as real, immediately: every one
of those three was found that way and none by reading code.

Relaying and deduplication are built, unit-tested and unexercised: two servers
give nothing to relay to. A third instance on this LAN was built and rejected in
favour of AD0MI installing QSP on a cloud server, which tests relay, dedup and a
link across the internet at once.

Attached is a git bundle of the whole repository. Please start by reading
`PROJECT_MEMORY.md` — particularly:

- **§0**, which sets out how I want this project approached
- **§6a**, what two members on a real network taught us
- **§6b**, what a second day taught us — it supersedes parts of §6a, especially
  about talkgroup rewriting
- **§8o**, the most recent session: the evening two QSP servers heard each
  other, and what was settled and should not be reopened. §8m is the night the
  linking system was first used in anger and is still worth reading, after it
- **§7**, working conventions — the section on working on my machines is all
  learned from things that went wrong
- **§8a**, how this project finds its defects
- **§8**, where a new session should start

Then read `CHANGELOG.md` for recent work and `docs/adr/README.md` for the
decision records.


## Which terminal

The operator works with several terminals open at once, and a command pasted
into the wrong one has cost this project time repeatedly — a `git` command on
the server, an `install` chained onto an `scp`, a stale binary deployed and
diagnosed for twenty minutes. **Every command block gets a header naming the
machine.**

| Label | Machine | Prompt | For |
|---|---|---|---|
| **FEDORA** | development machine | `mike@fedora:~/Documents/QSP/qsp$` | git, patches, the gates, `go build`, `scp` |
| **QSP-SERVER** | production, 192.168.1.247 | `mike@qsp-server:~$` | install, systemctl, journalctl, curl, tcpdump |
| **PI-STAR** | hotspot | | Pi-Star and MMDVMHost |
| **MONITOR** | wherever the console is watched | | the dashboard, tailing logs |

The rule: **anything touching the repository is FEDORA, anything touching the
running service is QSP-SERVER.** `scp` ends a FEDORA block; the `install` and
`restart` that follow are a separate QSP-SERVER block and are never chained on
to it.


## How we work

You develop in your container and deliver **numbered patch files** I apply with
`git am` on my Fedora machine. Commits use my identity:
`Mike <k9mls@outlook.com>`.

**Take the number from `git log`, not from this file.** The number used to be
written here and was wrong every time it was read — 0194 for seventy-seven
patches, then 0272 against a tree at 0284, then 0285 one patch after that was
corrected. A standing brief is edited when its subject changes and a patch
number has no subject, so it rots on every patch and rots silently. §7 says to
derive a claim rather than assert it where the derivation exists, and here it
does:

```sh
git log --oneline -1
cat VERSION
```

**And `sudo -v` on its own before any block containing `sudo`.** The password
prompt reads standard input, so the next pasted line goes in as the password and
one command silently does not run. It happened four times on 2026-09-09 and the
version check caught it every time — which is the argument for the check, not
for the habit that made it necessary.

**And run `cat VERSION` again after `git am`, before building.** On 2026-09-08 a
patch file never reached the machine: `git am` said so, the gates then passed,
the build succeeded and the deploy shipped the previous build. Every check after
the failure was answering about the wrong tree. The version and the patch number
move together, so one line says whether what is about to be compiled is what was
meant.

Never commit `go.mod` or `go.sum` — stage with
`git add -A -- ':!go.mod' ':!go.sum'`.

Before every patch: `gofmt`, `go vet`, `staticcheck`, the full test suite, and
the race detector. **staticcheck is not installable through the module proxy in
your container.** Take the release binary, which matches CI:

```sh
curl -sL -o /tmp/sc.tgz https://github.com/dominikh/go-tools/releases/download/2026.2.1/staticcheck_linux_amd64.tar.gz
tar -C /tmp -xzf /tmp/sc.tgz && cp /tmp/staticcheck/staticcheck /usr/local/bin/
```

`cmd/qsp` has **seven** known failures in your container, listed by name in §7,
because no SQLite driver is registered there. **Do not move
`cmd/qsp/driver_sqlite.go` aside** — four documents name that path and removing
it fails the documentation gate, which is how the count came to read eight and
hide a real failure. Use a workspace stub above the repository instead; §7 has
the four lines.

**Your container reaps background processes between commands**, so nothing
survives a `nohup ... &`, and it is a single core.

**Your container ships without Go, and no allowed domain carries a Go binary.**
`go.dev/dl` and the module proxy are both blocked, `golang/go` on GitHub
publishes source rather than binaries, and Ubuntu's newest package is 1.22. The
bootstrap minimum is enforced at run time, so the chain is **1.22 → 1.23 → 1.24.6
→ 1.27** from the source tags on `codeload.github.com`. The patch release
matters: `go1.24.0` is refused as a bootstrap for 1.27. `make.bash` cannot
finish inside one command, but its toolchain phases survive being killed — run
it once, then finish with `go_bootstrap install std` and `install cmd`. §7 has
the exact commands. Allow an hour. Do
it first: a documentation-only patch still has to pass the accuracy gate, and
the accuracy gate is a Go test.

**I have three terminals open** — my Fedora development machine, `qsp-server`
(the Ubuntu VM running QSP), and `pi-star` (my hotspot). Always say which
machine a command is for.

## Two rules that break ties

**Audio is king.** The best audio that can be delivered to the amateur community
is the first requirement, and it overrules features, convenience and elegance.
It has already decided that Talker Alias is passed through and never injected,
and that P25 is carried natively and never transcoded to reach DMR
(ADR-0034). When a decision could go either way, this settles it.

**Talkgroup numbers are never renumbered.** 2 is 2 and 11 is 11, on both sides
of a hotspot. A QSP-only hotspot needs no rewrite rules at all. See §6b, which
supersedes part of §6a.

## How this project actually finds things out

**Every byte read by eye has been wrong; every differential has been right** —
nine times now. **And a right hypothesis evaluated by broken arithmetic also
scores zero**: thirty thousand candidate Reed-Solomon constructions were
searched and all failed while the correct parameters sat inside the search
space, because the division applying them was wrong.
Change exactly one setting, capture again, and diff — or run candidate readings
against thousands of real frames and take the one that scores 99% where the
others score zero. Two captures differing in one known way beat ten differing in
unknown ways. When a reading is plausible and cheap to test, test it rather than
arguing for it. See §8a and §8d.

## Before you edit PROJECT_MEMORY.md

**Check we hold the same bytes first**: `md5sum PROJECT_MEMORY.md`, and compare
with your own copy. That file drifted for most of a day because a patch was
generated against a version the development machine did not have, and three
patches failed before anyone checked. Every patch gets a **unique filename and a
stated md5**, and a patch that will not apply is a question about which bytes
each side holds, not a reason to regenerate the same file name again.

## What I want from you

Tell me when I'm wrong. Push back on my ideas when you disagree — I'd rather
argue than be agreed with. When something doesn't work, look at what the running
system is actually doing before proposing a fix; almost every defect in this
project was found that way and none by the test suite.

Bigger patches rather than many small ones.

**Check the running binary after every deploy, and `qsp --version` is not that
check.** It runs the binary on disk, which `install` has already replaced, so it
answers the same before and after a restart; `systemctl is-active` says only
that something started. Ask the process instead — the `starting` log line on
production, a string unique to the new build in the container. `HANDOVER.md` has
both commands. An afternoon went on debugging a bridge that was never deployed,
and half an hour on a deploy that had worked.

**Grep for the call site after wiring anything across two subsystems.** An
exported method nobody calls compiles, passes vet, passes staticcheck and passes
every test. This has happened nine times; §8a calls it "declared and read by
nothing".

**GitHub Actions minutes are finite, and CI no longer runs on push.** It runs
when I ask for it (`gh workflow run CI`), weekly, and on a release tag — because
five of its six jobs repeated what my machine already runs before every patch.

So give me commands that apply and test a patch locally, and leave pushing to
me. The two things CI checks that I do not are the cross-compiles and `go mod
tidy`.
