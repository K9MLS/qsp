# Starting a new QSP session

Paste the text below into a new chat and attach `qsp-handoff.bundle`.

---

I'm Mike, K9MLS. I'm building **QSP**, a free open-source DMR network routing
and linking server in pure Go — a community alternative to commercial a commercial DMR server
software. The repository is `github.com/K9MLS/qsp`.

**It carries a real network.** Two stations use it daily: mine in Denton, Texas,
and KB9TYC's in Wisconsin, Wisconsin, about a thousand miles apart. Voice, private
calls, text messages and parrot all work on air.

Attached is a git bundle of the whole repository. Please start by reading
`PROJECT_MEMORY.md` — particularly:

- **§0**, which sets out how I want this project approached
- **§6a**, what two members on a real network taught us
- **§6b**, what a second day taught us — it supersedes parts of §6a, especially
  about talkgroup rewriting
- **§8b**, where this session should start, and what was settled and should not
  be reopened
- **§7**, working conventions — the section on working on my machines is all
  learned from things that went wrong
- **§8a**, how this project finds its defects
- **§8**, where a new session should start

Then read `CHANGELOG.md` for recent work and `docs/adr/README.md` for the
decision records.

## How we work

You develop in your container and deliver **numbered patch files** I apply with
`git am` on my Fedora machine. Number them from **0163**. Commits use my
identity: `Mike <k9mls@outlook.com>`.

Never commit `go.mod` or `go.sum` — stage with
`git add -A -- ':!go.mod' ':!go.sum'`.

Before every patch: `gofmt`, `go vet`, `staticcheck`, the full test suite, and
the race detector. **staticcheck is not installable through the module proxy in
your container.** Take the release binary, which matches CI:

```sh
curl -sL -o /tmp/sc.tgz https://github.com/dominikh/go-tools/releases/download/2026.2.1/staticcheck_linux_amd64.tar.gz
tar -C /tmp -xzf /tmp/sc.tgz && cp /tmp/staticcheck/staticcheck /usr/local/bin/
```

`cmd/qsp` has eight known failures in your container because
no SQLite driver is registered there; move `cmd/qsp/driver_sqlite.go` aside to
compile it.

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

**GitHub Actions minutes are finite, and CI no longer runs on push.** It runs
when I ask for it (`gh workflow run CI`), weekly, and on a release tag — because
five of its six jobs repeated what my machine already runs before every patch.

So give me commands that apply and test a patch locally, and leave pushing to
me. The two things CI checks that I do not are the cross-compiles and `go mod
tidy`.
