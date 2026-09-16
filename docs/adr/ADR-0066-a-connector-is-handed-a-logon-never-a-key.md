# ADR-0066: A connector is handed a logon, never a key

**Status:** Accepted
**Relates to:** [ADR-0009](ADR-0009-cgo-isolation.md),
[ADR-0062](ADR-0062-as-much-as-possible-in-qsp.md),
[ADR-0065](ADR-0065-a-full-backup-encrypted.md)

## Context

The Zello connector is a separate process, `qsp-zello`, because Opus needs cgo
and QSP does not link it (ADR-0009). To log on it needs three credentials: the
RSA private key a logon token is signed with, the account username, and its
password. All three already have a home — QSP's encrypted credential store,
entered through the console, carried in the full backup (ADR-0065), and
returned by no console endpoint, because a page that displays a password
leaks it to whoever is looking at the screen.

The question was how a second process gets a logon without undoing that.

## Options considered

**A. The connector opens the credential store itself**, read-only, on the same
host. Recommended first and then withdrawn. The key file decrypts *every*
credential in the store — peer passwords, link passwords, whatever is added
next — and `qsp-zello` is the most exposed process in the system: the only one
holding a connection to a server on the internet, and the only one decoding
packets from it with C code. **It would put the master key in the process
least deserving of it**, which is ADR-0009's isolation reasoning undone for
credentials. It also couples the connector to QSP's schema, and on Docker
shares a volume holding the whole database.

**B. A separate key file for the connector.** Simplest, and worst: the key
leaves the encrypted store, the console and the backup, and a rotation must
be done in two places.

**C. QSP signs, and hands out the result.** Chosen.

## Decision

**QSP keeps the private key and hands the connector a logon: a freshly signed
token, the username and the password.**

- **A Unix socket, not a port.** Mode 0600, owned by the user QSP runs as, and
  every connection's peer credentials are checked against that user. Nothing
  across a network can reach it, and nothing on the host but QSP's own user.
- **The key never leaves QSP.** It is the long-lived secret — it signs tokens
  for as long as the key pair is valid. A token handed out lives an hour
  (`zello.TokenLifetime`) and is made per connection.
- **Only the three Zello credentials are reachable.** The names are fixed in
  code, not requested by the caller, so the socket cannot be asked for a peer
  password.
- **The connector stores nothing.** It asks at every connection and
  reconnection, so a credential replaced in the console is used on the next
  logon without touching the connector.

## Consequences we accept

**This is a path by which a stored value leaves QSP**, and the property "a
value goes in and never comes out" is narrowed rather than kept whole. It
still holds for the console and every HTTP endpoint, which is the surface it
was written about. The password crosses the socket in clear; the socket is
reachable only by QSP's own user, who can already read the database and the
key beside it, so this adds no reader the host did not already have.

**The connector cannot log on while QSP is down.** It carries audio to and from
QSP, so it is useless then anyway; the dependency is real and costs nothing.

**The socket is a new local interface** with its own tests and its own health
check, which reports whether the three credentials are present so that "the
connector cannot log on" can be told apart from "nobody entered a password".
