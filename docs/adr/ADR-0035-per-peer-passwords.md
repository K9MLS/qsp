# ADR-0035: A member can be removed without changing everybody's password

**Status:** Accepted
**Relates to:** [ADR-0012](ADR-0012-peer-password-file.md),
[ADR-0020](ADR-0020-access-control.md)

## Context

`dmr.password_file` holds one secret and every peer on the network uses it. That
is what the HBP master authenticates against, and there is no alternative.

For three members who know each other it is exactly right, and ADR-0012's
reasoning about where the secret lives is unchanged by anything here.

It fails in one specific way, and the failure gets worse with every member who
joins. **Removing one person means changing everyone's password.** A member
leaves, or a secret ends up somewhere it should not be, and the only remedy is a
new shared password and every remaining member reconfiguring their hotspot on
the same evening. Twelve members means twelve reconfigurations to remove one.

A shared secret also leaks through whoever is least careful with it, and nobody
can tell which of them it was. One of this network's passwords reached a chat
log inside a week, in a configuration file pasted for an unrelated reason.

### What access control already does, and what it does not

ADR-0020's registration list can refuse a radio ID. That is real and it is the
right answer to *this specific station must not connect*.

It is not revocation of a credential. A refused ID is a refused number, and the
password that number was using still works for anybody who has it — including
the person just removed, under a different ID. Blocking by identity and
revoking by secret are different acts, and a network that can only do the first
is a network where the secret never really goes away.

## Decision

**A peer may have a password of its own. The shared one remains, as the
fallback.**

`dmr.peer_passwords` names a directory. A file in it named for a radio ID holds
that peer's password; a peer with no such file authenticates against
`dmr.password_file` as it does today.

Removing a member is deleting one file. Nobody else notices, nothing else
changes, and no other member reconfigures anything.

### Why a directory of files rather than a table

For ADR-0012's reasons, unchanged and now applying to more secrets rather than
fewer. Configuration is versioned, exportable and diffable; a password in it is
a password in the version history, in every backup, and rendered on screen in a
diff. A directory of paths keeps every one of them out of the document, out of
`configuration_versions`, and out of an export an operator sends when asking for
help.

It also means removal is `rm`, which works when the console is down, the database
is locked, or the person doing it is on a phone over SSH at midnight. **The
moment a credential most needs revoking is not the moment to depend on the most
machinery.**

### The shared password stays, and stays the default

A club that does not want the bookkeeping keeps what it has. Per-peer passwords
are opt-in per peer, not a migration everybody performs.

**Not deprecating it is deliberate.** A three-member club issuing three
individual secrets has added work and removed nothing, and a scheme that is
tedious at small scale is one people work around — by sharing one file between
members, which is the shared password again with extra steps and less honesty
about it.

### A per-peer password overrides, never merely adds

If a peer has its own file, the shared password does **not** work for that peer.

Otherwise removal does nothing: deleting somebody's file would silently return
them to the shared secret they already know, and an administrator would believe
they had revoked access they had in fact restored. That is the worst failure
available here, because it is quiet and it looks like success.

## Consequences

- **The join page issues a password per member, or says the club uses a shared
  one.** It currently says the password is *"sent to you separately"*, which
  stays true either way, but an administrator generating a member's credential
  needs somewhere to do it.
- **An unreadable per-peer file is a refusal for that peer, not a fallback.**
  Falling back to the shared password on a read error would turn a permissions
  mistake into a silently weakened network. The refusal is logged with the
  reason, which is the drop the console now explains rather than counts.
- **Removal is not disconnection.** A peer already registered keeps its session
  until it times out or the service restarts; deleting a file stops the next
  login, not the current one. An administrator removing somebody urgently needs
  both, and the access list is what does the second.
- **Every file is mode 0600 and checked on startup**, as `dmr.password_file`
  already is. A world-readable directory of member credentials is worse than one
  shared secret, and this must not be the change that introduces it.
- **The audit trail records issuing and revoking**, since 0139 made
  authentication auditable. Who removed whom, and when, is exactly the question
  a club asks afterwards.
- **This does not solve a rogue member who still has their radio.** A password
  stops a hotspot registering; it does not stop somebody transmitting through
  another member's hotspot. ADR-0020's subscriber list is that answer, and the
  two are complementary rather than alternatives.
