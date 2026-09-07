# Handover, 2026-09-07 night

Read `NEW-SESSION.md` for the standing brief and **§8k** of `PROJECT_MEMORY.md`,
then **§8a**, which is the section that matters most. §8b through §8j are
superseded and say so.

## Start here: two defects that took production down tonight

**Neither is built.** They are the price of the last hour and they come before
anything else.

### 1. "We listen on" is written unvalidated, and it stopped QSP starting

The Links page accepted `qsp.hopto.me:62045` in the accept form's **We listen
on** field and wrote it into an upstream. That name resolves to the router's
public address, which this machine does not have, so:

```
upstream "Test Server": cannot listen on qsp.hopto.me:62045:
listen udp 198.51.100.238:62045: bind: cannot assign requested address
```

QSP refused to start — correctly, rather than dropping a link an operator
configured — and **systemd crash-looped until it hit its start limit.**

The field beside it *is* validated: 0259 refuses `0.0.0.0` in **They send to**,
because a bind address is not somewhere a far end can reach. The two fields are
exact opposites and only one was checked. Same form, same afternoon.

**What to build:** validate the listen address where the accept handler writes
it — it must be an address this host can bind, so `0.0.0.0:62045` or a LAN
address, never a public name. `peering.ErrBindAddress` is the model for the
message.

### 2. `-check` passed a configuration the process then died on

```
sudo qsp -config /var/lib/qsp/qsp.json -check
/var/lib/qsp/qsp.json is valid
```

and the service then failed at bind time. **A gate that gives false assurance is
worse than no gate**, and the operator used it exactly as intended.

**What to build:** `-check` should attempt the binds it can — listeners and
upstream listen addresses — and report what would fail. It cannot prove a port
is reachable from outside; it can prove an address is one this host has.

### 3. Deploy 0260, which is committed and never went out

`A link can be removed` is at HEAD and is not on either server. It turns
tonight's recovery — hand-editing JSON twice on a live production server — into
two clicks.

## What happened tonight, in order

The operator tried to peer the test server to production **through the console**,
which is the right way and the way it will have to work in public. It failed
five times and each failure was a defect:

1. **The offer form had no callsign box**, while the invitation is refused
   without one. The error named a field that did not exist and then advised
   checking two fields that were already correct. Fixed in 0257.
2. **The address field accepted `https://` on a UDP host and port.** Fixed in
   0257.
3. **The reciprocal demanded a passphrase that does not exist.** Only one
   passphrase exists in a peering and the offering side generated it, so the
   operator had nothing to type into a box the form insisted on. Fixed in 0258.
4. **The exchange could not terminate.** `handleAcceptPeering` built a
   reciprocal unconditionally, so accepting a reply produced another reply,
   forever. **No instruction could have got the operator out** — three messages
   were spent telling them where to paste while the page manufactured an
   infinite regress. Fixed in 0259.
5. **A link could not be removed**, from anywhere. Fixed in 0260, not deployed.

Then the link that all of that produced took production down.

## The rule this page broke, and it is general

**Anything a page creates, it must be able to remove.** Nothing in this project
checked that, on any page. The Links page shipped without it and an operator
found out by needing it, on a live network, at the worst moment.

Worth auditing the other console pages for the same shape before adding
anything to them.

## The failure that produced all five

**Five separate things were designed from scratch today and found to be already
built**: `/api/peers` address redaction, IPSC `CallViews` returning nil, the
console's `data` pill, the hint disclosure button, and **the entire Links page**,
which was proposed as new work while it was on screen.

Every one was a single `grep` away. §8a carries the rule now — *check whether
the thing exists before designing it* — and the deeper version is this: **the
peering flow was reviewed by reading it and not by using it.** Every one of
tonight's five defects surfaced within ten minutes of an operator actually
clicking through, and none had surfaced in the code review that preceded it.

## What is finished and working

**Private text over IP Site Connect**, confirmed on air. The trellis codec is
proved against 54 real MMDVMHost bursts (54 of 54 decode; 0 of 54 with the
tables that shipped in 0242), both Rate 1/2 and Rate 3/4 have fixtures, and one
text is now one row in Last heard.

**The container install**, run on a clean Ubuntu VM. Nine defects found and
fixed, including a database that landed outside the volume — silent data loss on
every rebuild — and `adduser` failing because a `scratch` image has no `stty`.

## Open, in order

1. The listen-address validation, above.
2. `-check` attempting binds, above.
3. Deploy 0260.
4. **[ADR-0049](docs/adr/ADR-0049-first-account-setup-token.md)**: the first
   administrator account should be created from the home page rather than a
   terminal command. Decisions recorded, nothing built.
5. **No peer has ever registered with a containerised instance.** The handshake,
   the access list and the NAT-rebind path are all untested in a container.
6. Text over IPSC produces no call record entry of its own; the tracker covers
   it, and §8k has the detail.
7. The remaining UI pages have never been reviewed by using them.

## Traps

**`qsp --version` is not `systemctl is-active`, and neither is the other.** Both
were confused tonight: a version check was offered where a service check was
needed, and a running binary reported a version while the service was dead.

**`systemctl restart` on a rate-limited service stops it and then refuses to
start it.** Strictly worse than doing nothing. `systemctl reset-failed` first,
every time, once a service has crash-looped.

**A measurement filed as an exception is a defect already found.**

**A test that reads prose instead of code passes for the wrong reason.** Three
today: one searched a file for a field name and found it in a comment, one
searched for `stty` and found the comment explaining its removal, one searched
for a colour token and found the note explaining why it was not invented. Strip
comments before searching.

**Never count test failures.** The container baseline is seven, by name, in §7.

**`staticcheck` does run in the container**, contrary to §7.

**A failed `git am` leaves a rebase directory behind.** `git am --abort` first.
