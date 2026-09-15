# ADR-0065: A full backup, encrypted, alongside the shareable export

**Status:** Accepted
**Reopens:** [ADR-0054](ADR-0054-a-backup-restores-a-server-not-its-secrets.md)
**Relates to:** [ADR-0012](ADR-0012-peer-password-file.md),
[ADR-0053](ADR-0053-three-names-for-a-server.md),
[ADR-0055](ADR-0055-an-administration-page-answers-questions.md)

## Context

[ADR-0054](ADR-0054-a-backup-restores-a-server-not-its-secrets.md) decided that
an export carries configuration and no secrets, and rejected an encrypted
export carrying them. Its argument:

> It also needs a passphrase, which is stored where? On the machine that just
> died. It converts a recoverable situation into a lost one and calls it
> convenience.

The operator reopened it with a shorter argument: **you should be able to back
up everything, and if there is a security issue you change the passwords.**

That answers the strongest objection in the record. ADR-0054's case against
secrets in a backup rests on every copy of the file becoming a credential —
which is true, and is a description of a risk rather than of a loss.
Credential rotation is the standard response to exposure, it is available to
every account QSP holds, and it is cheaper than reconstructing a server from
memory. The record weighed the exposure and did not weigh the rotation.

Two of its points survive the reopening and shape this decision rather than
blocking it.

**The passphrase problem is real and is not solved by saying so.** An encrypted
backup is exactly as recoverable as its key, and an operator who keeps the key
on the server has a file nobody can open. That is worse than a partial backup,
because they believe they are covered.

**And the shareable export cannot be replaced.** ADR-0054's file exists to be
emailed to somebody helping with a broken server — that is the reason it was
written — and an encrypted archive of secrets cannot do that job.

One objection turned out weaker than expected. A peer password is shared with
another operator, and restoring it onto a clone of a dead machine affects them
rather than only the operator restoring. But ADR-0054 **already** requires an
import to state that it is a replacement and to ask the operator to confirm the
original is not running, precisely because two servers holding one identifier
is a collision class this project has met repeatedly. That confirmation covers
the credentials as much as the identifier, so the shared-secret objection
largely dissolves into a decision already taken.

## Decision

**There are two backups, with different words on them.**

### The shareable export is unchanged

Everything ADR-0054 decided about it stands: configuration, the identifier, the
version, the time; no secrets; it names every credential it cannot carry, by
the thing that needs it; an import reports a restored link as *awaiting a
credential* rather than broken.

**It is not widened.** Its value is being safe to email, keep in a repository
and hand to a stranger, and that value ends the moment it holds a password.

### The full backup carries everything, encrypted

Configuration, the identifier, and **the secrets**: peer passwords, upstream
passphrases, connector credentials. Encrypted with a passphrase the operator
supplies. Its purpose is one operator recovering their own server, and **a
restore from it comes up with links that work** — which is the entire gain and
the reason the operator asked.

An import of it is a replacement in ADR-0054's sense and carries the same
confirmation, for the same reason: the credentials it restores belong to links
whose far ends have not been asked.

### QSP states what the passphrase means, once, at the moment it matters

**When the backup is created**, not in a manual and not in a tooltip: the
passphrase is the operator's to keep, QSP does not store it, and losing it
makes the file useless.

This is the surviving half of ADR-0054's objection, handled rather than
dismissed. A full backup nobody can decrypt is worse than a partial one because
of what the operator believes about it, and the only defence is saying so
plainly while they still have the choice.

QSP does not check where the passphrase is kept, and does not imply that it
has.

### Secrets stay outside the configuration version history

Unchanged from [ADR-0012](ADR-0012-peer-password-file.md) and for a reason
specific to how this server stores configuration: `configuration_versions`
holds **the full JSON document** for every save, so that an operator can
inspect, diff and roll back. A secret written into configuration therefore
lands in every snapshot, every diff and every version shown in the console,
in plain text, with an author's name attached.

So configuration continues to *refer* to secrets rather than contain them. The
full backup reads them from where they live; the version history never holds
them.

### Both are produced and consumed from the console

Per the operator's stated preference and
[ADR-0055](ADR-0055-an-administration-page-answers-questions.md): **all
configuration is entered in the console, including secrets.** No credential
requires placing a file by hand.

That is a change in the entry point and not in the storage rule. A secret typed
into a console page is still stored outside the versioned document — the
operator is spared the file, and the export is still safe to send.

## Consequences we accept

**Every copy of a full backup is a credential**, and the operator has accepted
that in exchange for a recovery that works. Rotation is the response to
exposure, and it is available for every secret QSP holds.

**A lost passphrase is a lost backup**, with no recovery path at all. QSP says
so when it makes the file. It is the cost of the exchange and it is not
reducible by anything QSP can do.

**Two formats to keep working, not one.** Both carry a version, both refuse a
newer one, and the reasoning is ADR-0054's: importing three-quarters of a
configuration is worse than importing none, because the quarter that was
dropped is invisible.

**And two buttons an operator can confuse.** The names have to carry the
difference, because one file is safe to email and the other is not, and an
operator who mails the wrong one has published their passwords. That is the
first thing to get right about them.

## Alternatives rejected

**Widening the existing export.** It would destroy the property that export
exists for, and leave no safe file to send anybody.

**One export with an "include secrets" checkbox.** The same file name, the same
extension, and one of them is a credential. An operator forwarding a file they
made last month cannot tell which they have.

**QSP storing the passphrase so a restore is automatic.** Then the passphrase
is on the machine that died, which is ADR-0054's objection restored in full.

**Leaving ADR-0054 as it stands and doing this quietly.** Rejected because it
was accepted with reasoning, and a decision reversed without a record is a
decision somebody reinstates later from the argument still written down.
