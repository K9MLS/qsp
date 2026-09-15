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

Administrator accounts exist, and **the first is created in the browser through
a setup page guarded by a one-time token** (ADR-0056, amending ADR-0026). The
race an open setup page loses — a server with an empty database belonging to
whoever loads it first — is real, and the token is what guards it: without one
an attacker who wins the race gets a form they cannot submit.

The token is generated at startup when no account exists, logged once, held in
memory and never written to disk. **It is not required from loopback**, because
a request from the machine itself is from somebody who could read the journal
anyway. It is compared in constant time, and a refusal does not distinguish a
wrong token from an absent one.

**Every account after the first is created from the console**, by an
administrator who is already authenticated. `qsp adduser` on the host survives
as the recovery procedure for having lost every administrator, and is documented
in the README rather than displayed in the console.

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

### The encrypted full backup

`/api/admin/full-backup` and `/api/admin/full-restore` carry **configuration
and the credentials**, encrypted with a passphrase the operator supplies. See
[ADR-0065](docs/adr/ADR-0065-a-full-backup-encrypted.md).

**Mailing the wrong backup publishes every password on the server.** That is
the first thing to get right about having two: `/api/admin/backup` is safe to
send and this is not, the file extensions differ (`.qspbackup.json` against
`.qspfull`), and a restore of one through the other's endpoint says which it
found rather than only complaining.

A POST rather than a GET, because it takes a passphrase in a body and because
each call produces a file carrying every secret on the server — not a safe,
repeatable read. **The passphrase travels in the body**, never a query string,
which is logged by every proxy in the way and kept in a browser's history; a
passphrase in a log is every backup made with it.

AES-256-GCM, with the key derived by PBKDF2-HMAC-SHA256 at OWASP's recommended
600 000 iterations and a per-file salt. The cleartext header carrying the salt
and that count is **authenticated**, so it cannot be rewritten to 1 and handed
back for a cheaper attack. Argon2id would be preferable and is not in the
standard library.

**A credential that cannot be decrypted fails the whole backup** rather than
being omitted. A file silently missing one credential produces a restore where
three links work and one does not, for a reason nothing in the file records —
and the operator cannot know it was incomplete when they made it.

**A restore confirms first**, and carries the same identity warning the
shareable restore does: a backup holds a server identifier, so taking it makes
this machine a replacement for the one that made it, and two servers claiming
one identity is a failure neither will report. The credentials restored belong
to links whose far ends have not been asked, which is that same confirmation
covering more.

**Credentials are written before the configuration.** If the configuration
landed first and a credential write then failed, the server would be running a
configuration whose links have no passwords — silent, and looking correct. The
other order leaves credentials for links that do not exist yet, which is inert.

The audit trail records that a full backup was taken and by whom, and **never
the passphrase or the names of the credentials**: a trail listing which
credentials exist is a map for whoever later gets the file.

### Credential endpoints

`/api/secrets` lists the credentials an operator has entered, and
`/api/secrets/{name}` stores one on PUT and removes it on DELETE. All require a
logged-in administrator and refuse a cross-origin write.

**There is no endpoint that returns a credential.** Not an omission — a page
that displays a password leaks it to whoever is looking at the screen, and an
operator who needs the value has it elsewhere or should replace it. The listing
names each credential and says when and by whom it changed, and answers that
**without decrypting anything**, so it cannot leak a value even if the page
rendering it is wrong. The type it returns has no field for one.

**The name travels in the path and the value in the body.** A query string is
logged by every proxy in the way, written into an access log, and kept in a
browser's history.

**Why these exist separately from the configuration endpoints.**
[ADR-0065](docs/adr/ADR-0065-a-full-backup-encrypted.md) settles that all
configuration is entered in the console, including secrets — and that a secret
still lives outside the configuration document. Those are not in tension: the
first is about where an operator types, the second about where the value is
kept. A password written into configuration would appear in every version
snapshot, every diff and every version the console shows, in plain text, with
an author's name attached, because `configuration_versions` stores the whole
document for every save.

Credentials are stored AES-256-GCM under a key in a file beside the database,
created on first use. **What that protects and what it does not**: a copy of
the database without the key file yields nothing, which matters because a
SQLite file is handed around in ways a configuration file is not — sent for
diagnosis, caught in a storage snapshot. It does **not** protect a compromised
host, since whoever can read the database can usually read the key beside it.
Losing the key loses every credential, and the recovery is re-entering them.

An instance started without a database has nowhere to keep a credential and
**says so** rather than accepting one and discarding it, which would leave an
operator believing a link was configured.

The audit trail records who changed which credential and when, **by name and
never by value**: a trail is read, exported and kept far longer than a session,
and a password in it is a password in every copy of it.

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

`/api/links/offer-link` offers a link to another QSP server, and **it writes
before it answers.** A link is a peer registration (ADR-0051), so the offering
side allocates a DMR ID in its registration list and a password against that ID.
Both are written in one act: an ID permitted with no password behind it is
refused at login with a message that reads like the far end's mistake, and a
password with no list entry is refused with MSTNAK, which carries no reason at
all. The password is returned once, is never readable again, and is written to
`dmr.peer_passwords` at mode 0600 in a directory at 0700 — so a link removed
later takes its own credential with it rather than needing every member's
password changed, which is what ADR-0035 exists for. The invitation carries only
a fingerprint of the password, so a forwarded mail thread is not a credential.

`/api/links/accept` writes a link and a bridge after an administrator confirms
what an invitation says. It refuses a request without an explicit confirmation
flag, so a peering cannot be created by a request made in passing, and records
an audit event naming the far end's callsign and address whether it succeeds or
fails. A failed acceptance is worth having later; its absence would suggest
nobody tried.

`/api/setup`, on GET and POST, creates the **first** administrator and refuses
once one exists — 404 rather than a message, so somebody probing cannot tell a
configured QSP from anything else. It is gated by a one-time token generated
when the server starts with no account, logged once, held in memory only and
never written to disk; a restart mints a new one. **No token is required from
loopback**, because a request from the machine itself is from somebody who could
read the token from the journal in any case — that recognises a check already
passed rather than removing one. The token is compared in constant time and a
refusal does not distinguish a wrong token from an absent one. See ADR-0056,
which amends ADR-0026.

`/api/users` manages every administrator after the first, and needs a session
rather than a token: an administrator adding another is already authenticated.
On POST it creates an account and returns a generated password **once** — one
administrator never chooses or learns another's password. `/api/users/{name}/password`, on POST,
resets one the same way and clears any lockout. `/api/users/{name}`, on DELETE,
removes an account
and every session it holds in one transaction, and **refuses to remove the last
administrator** with a 409, because a console able to lock an operator out of
their own server is worse than one that refuses.

`/api/admin`, on GET, assembles the administration page: what this server is,
whether the running process matches its saved configuration, and what is running
that nobody configured per link (ADR-0055). It reads rather than computes — the
health report is the source for subsystem state, so the two cannot drift. The
server identifier is returned **truncated and is not editable by any endpoint**,
which is deliberate: "what everybody calls everybody else" and "an operator can
change it" cannot both be true (ADR-0053).

`/api/admin/backup`, on GET, returns this server's configuration as a file.
**It carries no secret**: ADR-0012 keeps passwords in files beside the
configuration precisely so a document that is versioned, diffed and pasted into
support requests never holds a credential, and an export inherits that. It is
safe to email, keep in a repository, or hand to somebody helping, and it stops
being safe the moment it holds a password. So it **lists the credentials it
cannot carry**, by the thing that needs each one, and the file answers that
question on its own without being imported.

`/api/admin/restore`, on POST, replaces every setting on this server with an
export. A request without `confirm` is answered with what would happen rather
than by doing it. The backup's server identifier is taken only when the operator
accepts that this machine is a **replacement** for the one that made it: two
servers claiming one identity is a collision that fails silently, and QSP cannot
see the other machine, so it asks rather than checking. `new_identity` generates
a fresh one instead. The console's own listening address is deliberately not
restored — it belongs to the machine rather than to the configuration, and a
restored server that cannot bind is discovered when the console stops answering.
An export from a newer QSP is refused outright with both versions named, because
importing three-quarters of a configuration leaves the missing quarter invisible.

`/api/admin/callsigns`, on PUT, turns the callsign lookup on or off and sets
the contact address. **It is the only setting the administration page may
change**, under the rule that a page may edit a setting when it is the page that
reports the problem — and, binding harder, may not otherwise. Enabling it
without a contact address is refused rather than saved: the registry asks
automated clients to identify themselves, so a lookup with no contact never
runs, and a stored setting that says on and does nothing is the shape §7
forbids.

`/api/restart`, on POST, stops QSP so that its supervisor starts it again. It
requires a session like every other administrative action, is recorded in the
audit trail **before** it happens — a record written afterwards is one that
never gets written — and exits by the ordinary signal path, so shutdown runs
exactly as it does for `systemctl restart`. **It does not promise the server
comes back**: QSP cannot see its own supervisor from inside, so the response
says what QSP does and names the case where the machine has nothing set to
start it again.

`/api/links/{name}/address`, on PUT, changes where one link reaches the far end
and nothing else. A link's name is a file path, its DMR ID is what the far end's
access list allows, and its password was agreed with somebody else — changing
any of those is a new peering rather than a correction, and doing it under the
word "edit" would look like one and behave like the other. The three addresses
that produce a link reporting itself healthy while carrying nothing are refused:
a scheme on the front, a bind address, and anything that is not host:port. An
inbound link has no address on this side and is answered as such.

`/api/links/inbound/{id}`, on DELETE, stops this server accepting registrations
from one DMR ID. **It is not a removal**: a link that dialled in is in nobody's
configuration here, so the far end is untouched and will keep dialling. What is
withdrawn is this side's consent — the password issued to that ID alone is
deleted, and the registration list is made to refuse it. Without this a server
could not decline a neighbour from its console at all, which ADR-0052 rule 1
requires of a federation. It does not disconnect a session already established;
that survives until it times out or QSP restarts, and the response says so. A
link authenticating with the shared peer password has nothing of its own to
revoke, and the response says that too, because an operator who believes a
network is locked out when it is not has been told something worse than nothing.

`/api/links/{name}`, on DELETE, removes a link, the bridge that was created
with it, and its passphrase file. **The file is deleted after the configuration is
saved, never before**: a passphrase removed from under a link that is still
configured leaves an instance that cannot authenticate and cannot say why,
while a file left behind under a link that is gone is untidy and harmless.

It removes only what accepting a peering created — the upstream, the bridge
named for it, and the passphrase. A bridge an operator wrote themselves is left
alone even when it routes to that upstream, and named in the response, because
deleting somebody's hand-written configuration as a side effect is a surprise
nobody asked for. It records an audit event either way.

Neither offer nor accept verifies that a callsign belongs to whoever sent the
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
