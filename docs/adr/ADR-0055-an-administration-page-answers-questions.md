# ADR-0055: An administration page answers questions, and is not a settings dump

**Status:** accepted, 2026-09-09

## Context

**Several of the defects found on 2026-09-08 existed because nothing could show
them.**

- A server's identifier is generated at startup, written to disk, and announced
  to every neighbour. It appears in one log line that scrolls away.
- The callsign lookup is enabled on production and disabled on the test server.
  Nothing in either console says so; it was found by an operator noticing that
  one Last-heard table showed callsigns and the other showed numbers.
- The two servers had different restart policies — `on-failure` and
  `unless-stopped` — and the console had no idea, so a restart button built
  without checking would have taken production off the air.
- The running server and its configuration disagreeing is the most valuable
  state QSP knows and it is reported only in passing, on one page, per link.

The version is the plainest case. It exists, and an operator spent an evening
reading it out of `journalctl` because there was nowhere on the page to look.

**The cause is not any one omission.** This project has built capability faster
than it has built visibility into capability. Nine patches in one session added
a Stop accepting button and a restart button, both because an operator noticed
the gap rather than because the design anticipated it. An administrator who
never opens a terminal cannot today answer *what is this server, and is it
working properly.*

**And administrators do not open terminals.** That is the premise of the whole
console, and the rule this project already works to: every operation must be
completable there, without editing a configuration file on the server. It is
what produced the IPSC panel, the credential page, the address field and the
Stop accepting button, each after an operator hit the gap.

## Decision

**One page, five blocks, each answering a question an administrator actually
asks.** Not a control panel and not a settings editor.

### 1. This server

Display name, callsign, **identifier**, version, uptime.

The identifier is shown truncated to eight characters and is **never editable**
(ADR-0053). It is displayed because two links with the same display name are
otherwise indistinguishable, and for no other reason; a field an operator reads
every day would be a field they came to think of as the name.

### 2. Agreement

**Does the running server match its configuration, and what differs.**

This block is why the page earns its place. The state is already computed —
`config.NeedsRestart` names the settings a save has not applied — and is
reported only as a sentence beside whichever link happened to be saved. A
server can be several changes away from what its configuration says, and today
no single view says so.

Where they disagree, the restart control appears here, with the same two clicks
and the same wording as elsewhere: QSP exits, something else starts it, and if
nothing does, it stays down.

### 3. Services

The things running that an operator did not configure per-link: **callsign
lookup** on or off and when it last succeeded, whether the configuration is
writable, how many peers and links are connected.

Facts, with a link to the page that owns each. This block reports; it does not
duplicate the pages that configure.

### 4. Backup and restore

Export, import, and the list of credentials an export cannot carry (ADR-0054).

**This has no home anywhere else**, which is part of why the page cannot wait:
ADR-0054 is written and there is nowhere to put the buttons.

### 5. Callsign lookup

The one setting this page may edit: a toggle and a contact address.

## The rule that keeps this from becoming a settings dump

**A page may edit a setting when it is the page that reports the problem.**

Sending an operator to another page to fix what this one is complaining about is
the same failure as telling them to restart and not offering the button. But the
converse binds harder: **if this page is not reporting a problem with a setting,
it does not get to edit it.** Every field that arrives later must justify itself
against that sentence, and "it is administration, so it belongs on the
administration page" is not a justification.

The callsign lookup qualifies because this page reports it as off. Nothing else
currently does.

## Design

The console's existing tokens and layout language apply unchanged. The
`ui-ux-pro-max` design system was consulted and its landing-page pattern —
oversized type, `clamp(3rem, 10vw, 12rem)` headlines — was **rejected**: it
describes a marketing page for an operations product, not an operations page.
Its typography recommendation, Fira Code with Fira Sans, is already what
`console.css` uses, which is a reassuring confirmation rather than a change.

What the design database does bind:

- **Contrast at least 4.5:1**, and the dark tokens in `tokens.css` already carry
  a note about a ratio that fell under that floor. Nothing on this page may
  reintroduce it.
- **Visible focus rings** on everything interactive. Never removed.
- **Touch targets at least 44×44px with 8px between them.** The page will be
  read on a phone by somebody standing next to a repeater.
- **Confirm before an irreversible or disruptive action**, which the restart and
  the import both are.
- **Show loading, then success or failure.** An import and a restart both take
  long enough that silence reads as a broken page.
- **Labels, not placeholders**, for the contact field.
- **No emoji as icons**, and no icon-only buttons.

Density is high — this is a status page, not a marketing page — but it is five
blocks, not a wall. Anything that does not answer a question an administrator
asks goes on the page that owns it.

## Consequences we accept

**A sixth block will be proposed, and the rule is what refuses it.** The value
here is a page that can be read in ten seconds, and every addition costs some of
that.

**Some information appears twice**, in a block here and on the page that owns
it. That is the cost of a page that answers *is this server healthy* without
sending anybody elsewhere, and it is a cost rather than a benefit: each
duplicated fact is a second thing that can drift, so each must be derived from
the same source rather than restated.

**The page cannot see the machine it runs on.** It cannot know whether anything
will restart QSP, whether the disk is full, or whether a firewall is dropping
UDP. It says what it knows and does not imply checks it has not made — the rule
the restart note already follows.

## What this does not decide

Whether other pages gain a link to it, and where it sits in the navigation.
Whether the health report and this page converge; they answer overlapping
questions and the relationship needs its own thought. How an import behaves
mid-session, which belongs with the backup work.

## Alternatives rejected

**A settings page listing every configuration field.** It would be quicker to
build, would remove the terminal from more workflows, and would become the thing
this record exists to prevent: a page nobody reads, a second and worse
configuration editor, and a surface where a mistake is one careless click from a
network outage. The console's other pages configure their own subjects and know
what a mistake there means; a generic editor knows nothing.

**Putting the restart button here only**, rather than beside the message that
asks for one. Rejected on 2026-09-08 for a reason that still holds: a restart
button on a page an operator is already frustrated with is a button pressed
without reading. It appears in both places, and both are places where a restart
has just been said to be needed.

**Leaving the identifier off the page entirely.** Tempting, since an identifier
nobody reads is working correctly. But two servers may choose the same display
name and nothing prevents it (ADR-0053), and when that happens the truncated
identifier is the only thing that tells them apart.
