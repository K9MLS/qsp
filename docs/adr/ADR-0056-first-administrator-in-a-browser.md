# ADR-0056: The first administrator is made in a browser, with a token

**Status:** accepted, 2026-09-09

**Amends [ADR-0026](ADR-0026-authentication.md)**, which decided the opposite
and gave good reasons. Those reasons are answered below rather than ignored.

## Context

ADR-0026 chose `qsp adduser` from a shell and rejected a browser setup page,
because a QSP reachable from the internet with an empty database **belongs to
whoever loads it first**, and the operator would never know — the result looks
exactly like a working setup. That risk is real and this record does not
dispute it.

It also rejected a token printed to the journal, on the grounds that it puts a
credential in a log that is copied into support requests and pasted into chats,
which is precisely what [ADR-0012](ADR-0012-peer-password-file.md) keeps the
peer password out of the configuration to avoid.

**Two things have changed since.**

**The console became the product.** When 0026 was written, QSP exposed no
endpoint that changed state and the admin interface did not exist. It now has
one, and every operation — access lists, bridges, schedules, links, peer
credentials, backup — is done there. A project whose premise is *an operator
should never need a terminal* that requires a terminal for the very first thing
an operator does is arguing with itself.

**And the objection is about durable secrets.** The peer password ADR-0012
protects stays valid for the life of a network; a log containing it is a
liability for years. A setup token is valid **only while no administrator
exists**, which on a working server is minutes. A journal pasted into a support
request afterwards contains a string that opens nothing. Those are different
objects, and the rule against one does not automatically reach the other.

## Decision

**When no administrator exists, QSP serves a setup page and nothing else.**
Every path redirects to it. It takes a username and a password, creates the
first account, and then **refuses to run again** — not hidden, refused.

### The token, and when it is not needed

**A one-time token gates the form.** QSP generates it at startup when no
administrator exists, logs it once beside a line saying the server has no
administrator, and holds it in memory only. A restart mints a new one; nothing
is written to disk, so there is no file to leak or forget.

**Except from loopback, where there is no token to type.** A request arriving
on the loopback interface is from somebody already on the machine, who could
read the token from the journal in any case. Skipping it there is not a
weakening — it is recognising a check that has already been passed. So:

- **On your own machine**: open the console, fill in the form, never touch a
  terminal.
- **On a server across a network**: one line from `docker logs` or
  `journalctl`, pasted into the form.

**The token dies the instant an account exists.** A setup page still answering
on a running network is a way in, so it answers 404 rather than a form.

### Everything after the first is in the console, with no token

An administrator adding a second administrator is already authenticated; that
*is* the check. The administration page lists accounts and can add one, reset a
password, or remove one.

**Three rules that are cheap now and awkward later:**

- **The last administrator cannot be removed.** A console able to lock an
  operator out of their own server is worse than one that refuses.
- **Removing an account ends its sessions.** Otherwise "removed" means "removed
  in about a fortnight", which is the session lifetime.
- **A reset mints a password and shows it once**, rather than letting one
  administrator choose another's. It is the shape the peer credentials page
  already uses, and it keeps one administrator from knowing another's password.

### One command survives, and it is a recovery procedure

Every administrator lost, and one must be made from a shell. `qsp adduser`
stays for that and is documented in the README rather than printed on a page.

**A page that prints shell commands is a page admitting it cannot do the
thing.** That is the failure that produced the restart button and the Stop
accepting button, and repeating it deliberately would be worse than either.

## Consequences we accept

**A server on a hostile network, restarted with an empty database, is still a
race — but the token is what you have to win it.** Without the token an attacker
gets a form they cannot submit. 0026's objection is answered rather than
eliminated: the window exists, and something is guarding it.

**The token is in the journal.** Accepted for the reason above, and narrowed:
logged once, never displayed in the console, never written to disk, invalid the
moment it is used.

**A restart before setup invalidates the previous token.** Correct rather than
inconvenient — a token that survived a restart would be a token that survives an
operator walking away.

## What this does not decide

Roles. Every account is an administrator, as under 0026; if QSP ever needs a
read-only operator or a per-club scope, that is its own record and its own data
model. Password policy beyond what `internal/auth` already enforces. Whether
sessions should be listable and revocable individually, which is adjacent and
larger.

## Alternatives rejected

**An open setup page with no token.** The friendliest, and it is 0026's race
with nothing guarding it. Gitea has shipped vulnerabilities of exactly this
shape.

**A time window after startup** — setup allowed for five minutes, then refused.
Portainer does this. It fails badly for the case QSP is built for: a club server
that comes up at three in the morning after a power cut, with nobody watching,
and is then unconfigurable until somebody restarts it deliberately.

**A default password.** The same failure with a longer fuse; every instance that
never changed it is compromised by reading the documentation.
