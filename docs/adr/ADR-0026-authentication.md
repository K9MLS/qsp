# ADR-0026: The first administrator is made from a shell, not a browser

**Status:** Proposed

## Context

QSP exposes no endpoint that changes state. Every configuration change is SSH
and a text editor, which is the last thing the parity document still marks
against QSP and the reason a club cannot run it without an operator who is
comfortable on a command line.

The admin interface is what closes that, and it cannot exist before
authentication does. The pieces are mostly present: ADR-0006 chose PBKDF2 with
a self-describing hash format and `internal/auth` implements it,
`configuration_versions` exists and migrates, `audit_events` is ready for an
actor, and `SetTable` and `SetAccess` already apply a change without a restart.

What is missing is who is allowed to press the button.

## The decision that matters is the first account

Everything else here is ordinary. The bootstrap is not, because every answer is
a way of letting somebody become an administrator, and the wrong one lets the
wrong somebody.

**A setup page, open until the first account is made**, is the friendliest and
is a race. A QSP reachable from the internet, restarted with an empty database,
belongs to whoever loads it first — and the operator would have no way to know
it had happened, because the result looks exactly like a working setup.

**A default password** is the same failure with a longer fuse. Every instance
that never changes it is compromised by reading the documentation.

**A token printed to the journal at startup** is defensible: reading the journal
means shell access. But it puts a credential in a log that is copied into
support requests and pasted into chats, which is the precise problem ADR-0012
solved for the peer password by keeping it out of the configuration document.

### Decision

**The first administrator is created from the command line, and there is no
other way.**

```
qsp adduser K9MLS
```

It prompts for a password on the terminal, applies `auth.ValidatePassword`,
hashes it with the parameters ADR-0006 chose, and writes one row. It reads the
same configuration the server does, so it writes to the same database, and it
refuses to run if that database is unreachable rather than creating an account
somewhere unexpected.

This requires shell access on the host. **That is not a cost, it is the point.**
Whoever installed QSP has it; whoever has not installed QSP should not be able
to become its administrator. The web surface therefore never has an
unauthenticated path that writes anything, at any moment in the instance's life,
including the minute after it first starts.

The same command creates every subsequent account. An admin interface that
creates users is a reasonable thing to want later; it is not needed to make the
admin interface exist, and shipping the smaller thing first keeps the
unauthenticated surface at nothing.

## Sessions

**Opaque random tokens, stored server-side, in the database.**

Not signed cookies carrying claims. The property worth having is that an
administrator who is removed, or a session that is suspected, stops working
immediately — and a self-contained token cannot be revoked without keeping a
list of the revoked ones, which is the server-side store arriving by a worse
route.

- The cookie is `HttpOnly` and `SameSite=Lax`. Lax rather than Strict so that
  following a link to the console does not appear logged out, which teaches
  people to log in twice and defeats the point.
- `Secure` is set **when QSP knows it is behind TLS**, which is what
  `server.behind_proxy` already records. Setting it unconditionally would break
  a club running plain HTTP on a LAN by silently dropping the cookie; not
  setting it on a public instance would leak the session. Neither is a default
  worth having, so the existing setting decides it.
- Sessions expire, and the expiry is checked server-side. A cookie's own
  lifetime is a hint the browser may ignore.

## Passwords are guessed slowly

PBKDF2 makes each attempt expensive, which is most of the answer, and the same
cost falls on QSP. An unauthenticated request that triggers a key derivation is
a denial-of-service primitive as much as a login.

So failed attempts are counted per account and throttled, and the count lives
with the account rather than in memory, because a process restart is otherwise a
free reset for whoever is guessing.

**A wrong password and an unknown username produce the same response and take
the same time.** A login form that answers faster for a name nobody holds is a
list of valid callsigns.

## What is deliberately not decided

**Roles.** There is one kind of account and it can do everything. A club with
three officers does not need a permission matrix, and inventing one before
anybody has asked means guessing which distinctions matter. The audit trail
already records who did what, which is the part that settles arguments.

**Password reset by email.** QSP sends no mail and adding an SMTP dependency to
recover an account is out of proportion. `qsp adduser` on an existing name
resets that account's password, from the shell, by the person who owns the host.

**Any of this being enough for the open internet.** Authentication is the floor,
not the ceiling; nothing here is a substitute for the console being behind a
proxy an operator controls.

## Consequences

- `migrations/0003_users.sql` adds accounts and sessions. Both are ordinary
  tables; the care is in the columns that are absent, since a password hash is
  the only credential stored and no reset token, security question or recovery
  code joins it.
- Every write endpoint that follows can assume an authenticated actor, and
  `audit_events.actor` finally has something true to put in it.
- **The console gains its first stateful surface**, and with it CSRF, which
  `SameSite=Lax` mitigates and does not close.
- A locked-out club is not locked out: the host they run QSP on is the recovery
  path, and it is the same one they used to install it.
