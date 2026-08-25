# ADR-0012: The peer password lives in a file, not in the configuration

**Status:** Accepted

## Context

QSP's HBP master authenticates peers with a shared secret. The obvious place for
it is the configuration document, alongside the listen address and timeouts.

That collides directly with three properties the configuration system was built
to have. Configuration is **versioned** — every save is written to
`configuration_versions`. It is **exportable** — operators back it up and share
it when asking for help. It is **diffable** — the console shows field-level
changes between versions.

A password in that document would therefore be written to the database on every
save, appear in every backup, and be rendered on screen in a diff. Constitution
§8 forbids storing credentials in plaintext without a documented architectural
reason, and "it was convenient" is not one.

## Decision

`dmr.password_file` holds a **path**. The document records where the secret is,
never what it is.

`config.LoadPeerPassword` reads it as a separate, explicit act, outside the
`Config` type entirely. Nothing carries the password into the version history,
an export, or a diff, because nothing can: it is not a field.

Supporting rules:

- **The DMR listener is disabled by default.** A freshly installed QSP must not
  begin accepting connections before an operator decides it should.
- **Enabled with an unreadable or empty password file is a fatal startup error**,
  not a silent downgrade to a disabled listener. A master that authenticates
  nobody is worse than no master, and an operator who enabled the listener
  believes it is running.
- **An empty file is rejected**, since it would otherwise authenticate any peer
  that guessed an empty password.
- Validation names the setting and explains the file's expected contents and
  mode, so the error is actionable without reading documentation.

## Consequences

- The secret never reaches SQLite, an export, a diff, or the console.
- **Accepted cost:** first-run friction. An operator must create a file before
  enabling the listener. The validation error tells them exactly what to create,
  which is the mitigation.
- Rotating the password means editing the file and restarting. Reload without
  restart is not implemented; when it is, it should re-read the file rather than
  cache the value.
- The same pattern applies to the Zello private key (clarification C5) and to
  any future credential. This ADR is the precedent.
- **Not addressed here:** file permissions are not enforced. QSP does not refuse
  to start on a world-readable password file. That would be a reasonable
  hardening step and is deliberately left out of scope rather than forgotten.
