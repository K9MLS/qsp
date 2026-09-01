# Security Policy

## Reporting a vulnerability

**Please do not open a public issue for a security vulnerability.**

Report it privately through GitHub's private vulnerability reporting on this
repository. Include what you found, how to reproduce it, and what an attacker
could achieve.

Expect an acknowledgement within a week. QSP is maintained by volunteers and
there is no SLA, but security reports are the first thing looked at.

## Scope

QSP is **hobbyist software for amateur radio**. It carries no warranty and must
never be used in public safety or life safety critical applications.

That said, a QSP instance is typically internet-facing, accepts UDP from
anywhere, and controls a club's repeater network. Vulnerabilities are taken
seriously.

### In scope

- Remote crash or hang from network input
- Memory or goroutine exhaustion from network input
- Authentication or authorisation bypass
- Injection of any kind
- Credential disclosure, including via logs or the audit trail
- Path traversal or unsafe file access
- Unauthorised routing changes
- Replay attacks against peer identity

### Out of scope

- Anything requiring physical access to the server
- Denial of service by overwhelming the host's bandwidth
- Missing hardening headers with no demonstrated impact
- Vulnerabilities in a reverse proxy the operator deployed in front of QSP

## Security model

### Assumptions

1. **Every network packet is hostile.** Parsers validate lengths, identifiers,
   ranges and state before acting. Malformed input must never panic. Parsers are
   fuzzed.
2. **The console may be exposed.** A strict Content-Security-Policy with no
   `unsafe-inline` is applied; the console is built to satisfy it.
3. **TLS is terminated by a reverse proxy.** QSP does not implement TLS. It
   honours forwarding headers **only** when the operator has declared
   `behind_proxy` — otherwise any client could forge its address in the audit
   trail.
4. **Handlers can have defects.** Panics are recovered into a 500 so that one
   broken console endpoint cannot drop a bridge carrying live traffic.
5. **Secrets reach logs by accident.** `audit.Redact` matches key names
   over-eagerly by design.

### Credentials

Passwords are hashed with PBKDF2-HMAC-SHA256 at 600,000 iterations with a
16-byte random salt. Hashes are self-describing, so the algorithm and cost can
be upgraded without invalidating existing passwords; see
[ADR-0006](docs/adr/ADR-0006-password-hashing.md).

Work per verification attempt is bounded so that an unauthenticated caller
cannot force arbitrary CPU consumption.

### Authentication

Administrator accounts exist, and **the only way one is created is
`qsp adduser` on the host**. See
[ADR-0026](docs/adr/ADR-0026-authentication.md): a setup page open until the
first account exists is a race an instance loses silently, so there is none, and
the web surface has no unauthenticated path that writes anything at any point in
the instance's life.

`/api/login` exchanges a username and password for a session cookie.
`/api/logout` ends it. `/api/session` reports who the caller is, answering
"nobody" rather than an error so that a console loading normally does not
produce one.

A wrong password and an unknown username give the same answer and take the same
time. Failed attempts are counted on the account and lock it briefly;
`qsp unlock <username>` clears that from the host, which is the same recovery
path the password itself has. The cookie
is `HttpOnly` and `SameSite=Lax`, and carries `Secure` when `server.behind_proxy`
says QSP is behind TLS — setting it unconditionally would silently break a club
running plain HTTP on a LAN.

Roles are deliberately absent: there is one kind of account and it can do
everything. The audit trail records who did what, which is the part that settles
arguments.

Events are written to the log and to `audit_events` in the database, and the
two fail independently — a database that is locked or full does not take the
log copy with it. Detail is redacted on the way in rather than on the way out,
because a secret written to an append-only table is a secret in every backup of
it.

Every authentication is recorded: a successful sign-in, a sign-out, a refused
password, and a refusal caused by lockout, the last as `denied` rather than
`failure`. **The failures matter more than the successes.** An attempt against a
username that holds no account is the shape of somebody guessing, and a trail
containing only successes cannot show it. The username is recorded as typed,
which may name no account.

### Configuration endpoints

`/api/config` reads the running configuration and saves a new one.
`/api/config/versions` lists the history and
`/api/config/versions/{number}` returns one version's document, which is how a
restore reads what it is about to write. All three require a logged-in
administrator and refuse a cross-origin write.

**These are the endpoints that change what QSP does.** A save is validated,
recorded as a version, written to the configuration file, and handed to the
goroutine that owns the routing core — in that order, so that a change which
fails to reach the disk is still one an operator can find and attribute. See
[ADR-0027](docs/adr/ADR-0027-configuration-writes.md).

Reading the configuration needs a session too. The document is not a set of
secrets — the peer password lives in a file it merely names — but it is a map of
the host, and an unauthenticated reader has no business with it.

Every save is an audit event naming the administrator, whether it succeeded or
not. An operator who could not save is a fact worth having later, and no record
would make it look as though nobody tried.

Some settings are saved and cannot take effect until QSP restarts: listen
addresses, the peer password file, the database, the logging format, and the
links an upstream holds. A save names them rather than reporting a bare
"restart required".

### Peer authentication

**A source address that repeatedly fails to log in stops being answered.** Six
refusals in fifteen minutes and QSP ignores that address for five minutes,
including the challenge — which is where a guesser would otherwise collect a
fresh salt on every attempt. A successful login clears the history, so a member
who fixes their password is not held to the attempts before they did.

Throttling is per source address rather than per repeater ID, because an ID is
whatever the caller claims and a determined guesser would vary it. The address
is the one thing a remote party cannot choose freely.

**This bounds guessing rather than preventing it.** Roughly seventy attempts an
hour is hopeless against a strong peer password and ruinous against a weak one,
so the password still has to be a real one.

A refused login is logged with the reason — a wrong password, an unknown ID, a
digest from the wrong address, or one with no challenge outstanding — because
those need different things done about them. A run is reported once rather than
once per attempt, and the console shows what is currently being refused.

### Outbound requests

**QSP makes one kind of request off the instance, and only when configured.**
With `dmr.callsigns.enabled`, it asks the amateur DMR registry at
`radioid.net` about radio IDs heard on this instance — one at a time, cached,
never in bulk. See [ADR-0030](docs/adr/ADR-0030-radio-id-lookup.md).

`dmr.callsigns.contact` is required and is sent in the User-Agent along with
QSP's version. The registry asks automated clients to identify themselves, and
the address is the operator's because it is the operator making the requests.

Nothing else reaches the internet. Map tiles are fetched by the browser rather
than by QSP, and no telemetry of any kind is sent anywhere.

### Read-only endpoints

`/healthz`, `/readyz`, `/api/events`, `/api/peers`, `/api/join`,
`/api/join/config` and static
console assets are unauthenticated and read-only. That includes the access
control, network settings, bridges and history pages, which are markup like
every other console page: the endpoints
behind it refuse anonymously, which is where the decision belongs, and it shows
a sign-in prompt rather than a form when nobody is signed in.

`/api/peers` returns callsigns, radio IDs, peer source addresses, and the
position a hotspot announces. It is unauthenticated, which is a further reason
to follow the deployment guidance below rather than exposing the console
directly.

`/api/join` returns the network's address, port and talkgroups — what a club
member needs to point a hotspot at it. It deliberately does **not** return the
shared peer password, so the endpoint is safe to expose to a club's members
even though the rest of the console is not. It also reports how many peers are
connected, and identifies the caller's own hotspot by matching source
addresses, which is a hint rather than an assertion: several members behind one
router share a public address.

`/api/join/config` renders the DMRGateway network block a member pastes into
their own hotspot. It returns nothing `/api/join` does not already return: the
same address, port and talkgroups, arranged as configuration. **The password is
a placeholder in the output** and is never substituted, for the same reason
`/api/join` omits it.

Its `prefix`, `block` and `radio_id` parameters are the member's own facts
about their own hotspot, not the club's. A value out of range falls back rather
than failing, so an edited URL produces a page rather than an error nobody can
act on, and every parameter is rendered into a fixed template that cannot carry
arbitrary text into the file.

When `radio_id` is absent, QSP uses the radio it has heard through the caller's
peer, matched by source address, and omits the private call rules entirely when
it has heard none. It does not derive an ID from the peer's two-digit suffix:
that suffix is a convention rather than a rule of the protocol, and a rule
naming the wrong radio would send a member's private calls to somebody else.

`/api/links` reports the links to other networks and requires a session,
unlike `/api/peers`. A peer list describes stations whose operators chose to
join this network. A link names somebody else's server, the address it is
reached at, and whether their passphrase is verifying — which is theirs to
disclose rather than this instance's.

`/api/links/offer` generates a peering invitation and the passphrase behind it.
**The passphrase is returned once and is never readable again from the
console**: it is written to a file beside `dmr.password_file` at mode 0600 when
a peering is accepted, and configuration — which is versioned, stored in the
database, and shown in the console — carries only its path.

`/api/links/accept` writes a link and a bridge after an administrator confirms
what an invitation says. It refuses a request without an explicit confirmation
flag, so a peering cannot be created by a request made in passing, and records
an audit event naming the far end's callsign and address whether it succeeds or
fails. A failed acceptance is worth having later; its absence would suggest
nobody tried.

Neither endpoint verifies that a callsign belongs to whoever sent the
invitation. That is a claim, checkable against RadioID.net by a person. An
operator agreeing to peer has already decided who they are dealing with.

`/api/calls` reads the record of completed transmissions and **requires a
session, unlike the live list on the overview**. The overview shows what is
happening now, which anybody within range of a repeater can hear anyway. This is
up to `dmr.calls.retain` of who transmitted and when — a record of members'
activity, and one an administrator should have to sign in to read.

It returns no audio. QSP carries bursts it never decodes, and a record of who
spoke is not a recording of what they said.

`dmr.peer_passwords` names a directory of per-peer password files, each named
for a radio ID. A peer with a file of its own authenticates against it and
**not** against the shared password, so removing a member is deleting one file
and nobody else reconfigures anything.

The override is the property revocation depends on: if the shared password still
worked for a peer that has its own, deleting somebody's file would silently
return them to the secret they already know. An unreadable or world-readable
file is a refusal for that peer rather than a fallback, because falling back on
a permissions mistake turns it into a silently weakened network.

Removal is not disconnection. A peer already registered keeps its session until
it times out or the service restarts; deleting a file stops the next login. For
somebody who must be off the network now, the registration access list is what
does it.

`POST` and `DELETE` on `/api/peers/{id}/password` issue and remove one member's
password. Both require a session and both write an audit event naming the
administrator and the radio ID, because *who removed whom, and when* is the
question a club asks afterwards.

**A generated password is returned once and is never readable again.** It is
written to a file at mode 0600 in a directory created at 0700, and the
configuration records only the directory. Removing a password that does not
exist is reported as the state rather than as a failure: an administrator asking
twice should be told the peer was already using the shared password.

### Deployment guidance

- Bind the console to `127.0.0.1` and reach it through a reverse proxy with TLS.
- Do not expose the console directly to the internet.
- Set `behind_proxy` only when a proxy you control is genuinely in front.
- Back up the configuration export before every upgrade
  ([ADR-0007](docs/adr/ADR-0007-schema-downgrade.md)).
