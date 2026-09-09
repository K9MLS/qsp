# ADR-0054: A backup restores a server, not its secrets

**Status:** accepted, 2026-09-08

## Context

**A server's configuration exists in one place: the box it runs on.** Links,
access lists, bridges, talkgroup rules, the schedule, and — once
[ADR-0053](ADR-0053-three-names-for-a-server.md) is built — the identifier that
is what every neighbour calls it. A failed disk takes all of it, and the only
recovery today is an administrator remembering what was there.

That is tolerable while every server belongs to the operator who built it. It
stops being tolerable the moment somebody else is running one: the first thing
anybody says when helping a stranger with a broken server is *send me your
configuration*, and there is no way to.

**The obvious design is wrong**, and it is worth writing down why before
somebody builds it. "Export the configuration, import it on the new box" gives
back the links, the lists and the bridges, and gives back **no credential at
all** — because [ADR-0012](ADR-0012-peer-password-file.md) deliberately keeps
secrets in files beside the configuration rather than inside it, so that a
document which is versioned, diffed, shown in a console and copied about is
never the thing holding a password. Three fields point at secrets rather than
containing them: `dmr.password_file`, an upstream's `passphrase_file`, and an
upstream's `password_file`.

So a naively restored server comes up with every link configured, every link
unable to log in, and a page reporting them all as configured and not open.
**That looks exactly like a network fault and is not one.**

## Decision

### The export carries configuration and the identifier, and no secrets

One file. The configuration as it stands, the identifier from 0053, the QSP
version that wrote it, and the time. Safe to email, to keep in a repository, to
hand to somebody helping — which is the whole point, and stops being true the
moment it holds a password.

**It names the credentials it cannot carry.** Not silence, and not a warning in
a manual: the export lists every secret the configuration refers to, by the
thing that needs it — this link, this member — so the file itself says what a
restore will be missing. A restore then has a checklist rather than a mystery.

### An import says what it could not bring back, per credential

The page an operator lands on after importing lists each link and member whose
secret is absent, with the action beside it. For a QSP link that action already
exists and is two clicks: the offering side reissues a password and an
access-list entry in one act (0288), so the far end's administrator regenerates
an invitation and the restored server accepts it.

**A restored link is reported as awaiting a credential, not as broken.** The
distinction is the entire reason this record exists.

### The identifier travels, and an import is a replacement

0053 requires it: without the identifier a restored server is a stranger to
every neighbour, and re-agreeing every link is exactly the pain a backup exists
to avoid.

**But two servers holding one identifier is the collision class this project has
met repeatedly**, and every instance of it failed silently. So an import states
plainly that it is a replacement, and asks the operator to confirm that the
original is not still running. QSP cannot verify that — it has no way to see
the other machine — so it does the honest thing and says so rather than
implying a check it did not make.

An operator who wants a second server rather than a replacement imports and asks
for a new identifier, which is a different button with different words on it.

### A version older than this build migrates; a version newer refuses

An export carries the version that wrote it. Older is migrated by the same
mechanism the database already uses. **Newer is refused entirely**, with the two
versions named, because importing three-quarters of a configuration is worse
than importing none: the quarter that was dropped is invisible, and the operator
believes they have restored a server.

## Consequences we accept

**A backup is not a complete recovery, and the export says so on its face.**
That is the trade for a file safe to store and send. The alternative — an
encrypted export carrying secrets — moves the problem to a passphrase which is
itself lost with the machine, and makes every copy of the file a credential.

**A restore needs the other operator for each link.** Unavoidable and correct:
the far end's consent is theirs to give, and a restored server that could log
into its neighbours with no action on their part would be a server whose
credentials survive a machine nobody controls any more.

**The export is a supported format, so it cannot change casually.** It carries a
version for that reason.

## What this does not decide

Whether the export is also the way a fresh server is provisioned, which is
adjacent and larger. Whether call history, the audit trail or callsign data are
included — the decision here is about configuration; those are operational data
with different retention rules and belong in their own record. How a whole
network is backed up, as opposed to one server, which is a different problem
with a different answer.

## Alternatives rejected

**An encrypted export containing secrets.** Simple to describe, and it makes
every copy of the backup a credential — including the one in the repository, the
one in the mail thread, and the one on the failed disk. It also needs a
passphrase, which is stored where? On the machine that just died. It converts a
recoverable situation into a lost one and calls it convenience.

**Copying the password files alongside.** Honest, and it puts the operator back
in the business of moving secrets by hand, which is what this project has been
removing since 0035. It also does nothing for the case the export exists for:
handing the file to somebody who is helping.

**Silently restoring links with no credentials.** What a naive implementation
does. The links report configured and not open, an operator reads that as a
network fault, and the evidence that would explain it — that no password was
ever restored — is nowhere on the page.
