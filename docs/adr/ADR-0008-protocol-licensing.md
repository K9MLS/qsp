# ADR-0008: Protocol implementation sources

**Status:** OPEN (interim rules in force; no longer blocks phase 1)

**Update 2026-08-23.** Two things were settled that are often confused with this
ADR but are separate from it:

- **QSP's outbound licence is GPL-3.0**, and K9MLS is the copyright owner.
- **Employer clearance was granted.**

Neither answers the question below. Our own licence does not govern what we are
permitted to *derive from*, and an employer's clearance says nothing about a
third party's CC BY-NC-SA document. The interim rules therefore remain in force
and are now load-bearing rather than precautionary, because protocol code exists
from this point onward.

**Update, research pass.** The published Homebrew repeater protocol
specification (DL5DI, G4KLX, DG1HT, 2015-07-26), as reproduced on the
BrandMeister wiki, **has now been read and used**, under the "protocol
documents" limb of the interim rules.

What that changed, stated plainly:

- Six message types are now implemented from the documented format rather than
  left unimplemented: `MSTNAK`, `MSTACK`, `MSTCL`, `RPTCL`, `MSTPING`,
  `RPTPONG`. They are marked in the code as specification-derived and
  fixture-free.
- **No implementation source was read**, and no text, table or code was copied.
  What was taken is the wire format: field names, offsets and lengths.
- The document carries the CC BY-NC-SA licence this ADR is about. Reading it
  does not resolve the question below; it makes answering it more pressing,
  because QSP now demonstrably derives from it.

The usual position is that protocol facts — offsets, lengths, orderings — are
not copyrightable, and that implementing from a specification is exactly what a
specification is published for. That reasoning is sound enough to proceed on and
not sound enough to publish on without checking.

Everything in `internal/protocol/hbp` remains derived from captured traffic or
from that document. Provenance is recorded per message type.

**Update 2026-08-27, IPSC.** Motorola IPSC is required for the repeaters clubs
actually run — XPR8300, XPR8400, SLR7500, MTR3000 — and BLUEPRINT-v1 §4 makes it
a phase. That forces this ADR, because IPSC differs from every protocol
considered above in one way that matters:

**There is no published IPSC specification.** The Homebrew Protocol had a
document to implement from, however awkwardly licensed. IPSC has none. Motorola
has not published one, and every open implementation is reverse-engineered. The
"protocol documents" limb of the interim rules therefore does not exist for
IPSC, and the remaining routes are captured traffic or somebody else's source.

### A correction to this document

**DMRlink is not CC BY-SA 3.0.** The Context below says it is; that is wrong for
the code. Every source file carries a GNU General Public License header, version
3 or later, copyright Cortney T. Buffington N0MJS. HBlink3 is GPL-3.0 likewise.
The CC BY-SA attribution in that project applies to its documentation, not its
Python.

That correction changes the picture, because GPL-3.0 into GPL-3.0 is not a
conflict to be reconciled — it is the arrangement that licence exists to permit.

### Two routes, and the first is already allowed

**Captured traffic.** The interim rules already permit implementing from real
traffic, and that is how `internal/protocol/hbp` was built and validated. K9MLS's
club operates the exact repeaters IPSC is wanted for, so captures are obtainable
in a way they are not for most projects. **This route needs no amendment at all
and is the preferred one**, for the same reason the testdata rules prefer
captures generally: a fabricated packet only proves the parser agrees with its
author, and a reverse-engineered implementation is somebody else's idea of the
protocol rather than the protocol.

**Deriving from DMRlink or HBlink3.** Permitted by their licence into a GPL-3.0
project. The consequence is real and permanent: QSP's IPSC implementation would
be a derivative work, requiring attribution and the GPL notice, and that cannot
be undone later by rewriting the code from memory.

### Decision

1. **IPSC is implemented from captured traffic**, under the existing interim
   rules, with provenance recorded per message type as for HBP.
2. **Reading DMRlink or HBlink3 is permitted where captures are insufficient**,
   which they will be for message types the club's equipment never emits. Where
   that happens it is recorded in the code at the point of use, and QSP carries
   the attribution and GPL notice a derivative work owes. This is an explicit
   narrowing of the "do not read any reference implementation" rule, which was
   written against CC BY-NC-SA and GPL-2.0 sources and was over-broad for
   GPL-3.0 ones.
3. **The CC BY-NC-SA question below remains open.** Nothing here resolves it;
   IPSC simply does not touch it, because there is no CC BY-NC-SA IPSC document
   to touch.

This is reasoning about licence terms, not legal advice, and it is recorded so
that somebody qualified can disagree with something specific.

## Context

QSP is GPL-3.0. The reference material for the protocols it must implement
carries licences that have not been reconciled with that choice.

- HBlink3 and FreeDMR state their work interprets the Homebrew Repeater Protocol
  from the 2015-07-26 DMRplus documents, and that those documents are licensed
  **Creative Commons BY-NC-SA**. The **non-commercial** clause is a content
  licence term interacting with a software licence.
- DMRlink was recorded here as **CC BY-SA 3.0**. That is incorrect for the
  code; see the 2026-08-27 update above. Its source files are GPL-3.0-or-later.
- G4KLX's P25 client code is understood to be **GPL-2.0**, which does not flow
  forward into a GPL-3.0 project.

Three questions are unresolved:

1. May a GPL-3.0 project implement a protocol whose specification document is
   CC BY-NC-SA?
2. Does reading GPL-2.0 implementation source to understand behaviour affect a
   GPL-3.0 implementation?
3. What attribution is owed, and where does it belong in the repository?

## Interim decision, pending an answer

**Nobody writes protocol code until this is resolved.**

Working rules in the meantime:

- Implement strictly from protocol **documents** and **captured real traffic**.
- Do not read, port or transliterate any reference implementation source.
- Record the provenance of every fixture in `testdata/`.
- Credit the protocol authors in `README.md` and `BLUEPRINT.md`.

These rules are stated in the package documentation for `internal/protocol/hbp`
and `internal/protocol/p25` so that a contributor meets them before writing a
line.

## Consequences

- Phase 1 is blocked on a legal answer, not an engineering one.
- If the answer is unfavourable, it affects how the Homebrew Protocol may be
  implemented — the core of the product. That is why this is being resolved
  before code exists rather than after.
- This ADR is updated with the answer and its status changed to Accepted or
  Superseded. It is not deleted.
