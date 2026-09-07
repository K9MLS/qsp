# ADR-0049: The first administrator account is created from a token in the log

**Status:** Proposed — decisions only, nothing built
**Date:** 2026-09-07

## Context

A fresh QSP has no accounts, so the console cannot be signed in to. The only way
to create the first one is a terminal command:

```sh
docker compose exec -it qsp /qsp -config /var/lib/qsp/qsp.json adduser mike
```

On 2026-09-07 that command failed on a clean machine, because `adduser` hid the
typed password by running `stty -echo` and the container image is a `scratch`
layer with no stty in it. **A server nobody can sign in to is not installed**,
and it was the first thing an operator hit after an otherwise successful
`docker compose up`. Fixed in 0.1.97 with a terminal ioctl, but the episode
exposed the shape of the problem rather than the bug: the console knows perfectly
well that it has no accounts and says nothing about it, while the way to fix that
lives somewhere else entirely.

The operator's instinct: the home page should ask to set up an administrator
account.

## The thing that decides the design

**The console binds to `0.0.0.0:8080`**, because ADR-0048 established that
`127.0.0.1` under host networking is a wall on minute one for a newcomer.

So a plain "create your administrator" page on a fresh instance is **owned by
whoever reaches it first**. For a service whose entire purpose is to be
reachable from the internet, that is not a hypothetical: a scanner finds an open
port faster than an operator finishes reading `docker compose logs`.

This project already refuses to be open by default. The configuration validator
will not write a master that accepts every repeater, and ADR-0048 made the first
run ask who may connect before anything listens. **The console should be held to
the same standard as the radio side**, and today it is not — it is simply
unusable instead, which is a different thing from being safe.

## Decision

**A one-time setup token, printed to the journal on first start when no accounts
exist.**

```
level=INFO msg="no administrator account yet"
  setup_url="http://<host>:8080/setup?token=k7m2-9xqp-4vth"
  note="valid until an account is created"
```

The operator copies the line, opens it, chooses a username and password.

### Why a token rather than an open page

**It proves the visitor can read the machine's logs**, which is the same thing
as proving they control the machine. That is exactly the authority being handed
over, and nothing weaker should hand it over.

It is also barely any friction: copying a URL out of `docker compose logs` is
one step more than clicking a link, and it is a step the operator is already
taking to see whether the thing started.

### Why not loopback-only setup

The other honest answer is to serve `/setup` only to `127.0.0.1` and make the
operator tunnel. It is safer still, and it reintroduces the exact wall ADR-0048
removed — a newcomer with a VPS and no SSH tunnel is stuck. The token gets the
same guarantee without the wall.

### Rules the implementation has to keep

- **The route exists only while there are no accounts.** After the first is
  created, `/setup` is gone — not disabled behind a flag, gone — so it is not a
  permanent unauthenticated surface waiting for a mistake.
- **The token is generated per process start** and held in memory only. A
  restart issues a new one and invalidates the old; it is never written to the
  configuration or the database.
- **It is compared in constant time**, like every other secret in this codebase.
- **`adduser` keeps working**, unchanged. An operator who prefers a terminal
  should not have to use a browser, and the container's own `-it` path is the
  one that recovers a forgotten password through `unlock`.
- **The token appears once per start, at info.** A secret that is re-printed
  every few minutes ends up in more logs than it needs to.
- **The page says what it is doing.** "This is the first account on this server
  and it will be an administrator" — because somebody arriving from a forum post
  should not have to infer that.

## Consequences

**The console gains an unauthenticated route for the first time**, and it is the
one that matters most. That is the reason this is an ADR rather than a patch:
`internal/server/middleware.go` lists the endpoints that skip authentication, and
adding to that list deserves a decision with reasons written down rather than a
line changed in passing.

**A rendered page must be tested for its absence as well as its presence.** The
recurring failure in this project is a test that arranges the thing it asserts —
a `/setup` test that passes because no accounts existed in the fixture would say
nothing at all. The test that matters is: create an account, then confirm the
route is gone and the token no longer works.

**The journal is now a place secrets appear.** It already carries peer
addresses; a setup token is a different weight. Worth stating in `SECURITY.md`
rather than discovered by somebody pasting logs into an issue.

## What this does not change

`deploy/docker/README.md` keeps its `adduser` section. Two documented ways to
create an account is one more than ADR-0048's rule about install paths would
normally allow, but they are not two install paths — they are a browser and a
terminal doing the same job, and every operator has one of the two.
