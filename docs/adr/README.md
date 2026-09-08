# Architecture Decision Records

Each file records one decision: the context, the options, the choice, and the
consequences we accepted.

An ADR is never edited to change its decision. A decision that turns out to be
wrong gets a new ADR that supersedes the old one, and the old one is marked
superseded. The history of why we thought something is as useful as the
conclusion.

| ADR | Decision | Status |
|---|---|---|
| [0001](ADR-0001-go.md) | Go for the core | Accepted |
| [0002](ADR-0002-single-writer-routing-core.md) | Single-writer routing core | Accepted — amended |
| [0003](ADR-0003-event-bus.md) | Sequenced event bus with bounded replay | Accepted |
| [0004](ADR-0004-no-external-dependencies.md) | No external dependencies in the core | Accepted |
| [0005](ADR-0005-sqlite-driver.md) | SQL driver registered by the binary, not the storage package | Accepted |
| [0006](ADR-0006-password-hashing.md) | PBKDF2-HMAC-SHA256 with a self-describing hash format | Accepted |
| [0007](ADR-0007-schema-downgrade.md) | Refuse to run against a newer schema | Accepted |
| [0008](ADR-0008-protocol-licensing.md) | Protocol implementation sources | Open — interim rules in force |
| [0009](ADR-0009-cgo-isolation.md) | cgo connectors ship as separate binaries | Accepted |
| [0010](ADR-0010-protocol-codec-shape.md) | Protocol codecs parse but do not interpret | Accepted |
| [0011](ADR-0011-nat-rebind.md) | A source address change requires re-authentication | Accepted — needs field validation |
| [0012](ADR-0012-peer-password-file.md) | The peer password lives in a file, not the configuration | Accepted |
| [0013](ADR-0013-routing-decision-is-pure.md) | The routing decision is a pure function | Accepted |
| [0014](ADR-0014-contention.md) | A transmission occupies its origin as well as its destinations | Accepted |
| [0015](ADR-0015-level-triggered-scheduler.md) | The scheduler is level-triggered, and stores wall time | Accepted |
| [0016](ADR-0016-ptt-triggered-bridging.md) | PTT-triggered bridging, and how it merges with the schedule | Accepted |
| [0017](ADR-0017-first-dependency.md) | Adopting modernc.org/sqlite, the first dependency | Accepted |
| [0018](ADR-0018-openbridge.md) | OpenBridge for linking to other networks | Proposed — narrowed by 0051 to foreign networks |
| [0019](ADR-0019-master-repeats.md) | A master repeats; bridging is a layer on top | Accepted |
| [0020](ADR-0020-access-control.md) | Access control, and why it is checked in two places | Accepted |
| [0021](ADR-0021-private-calls-and-data.md) | Private calls and data are in scope, and share one missing thing | Proposed |
| [0022](ADR-0022-timeslot-contention.md) | Contention belongs to the timeslot, not the talkgroup | Proposed |
| [0023](ADR-0023-talkgroup-subscription.md) | Peers attach talkgroups, and mostly attach them by talking | Proposed |
| [0024](ADR-0024-outbound-peer-mode.md) | Outbound peer mode, and the network it must not be used against | Proposed |
| [0025](ADR-0025-no-bundled-map.md) | A map with no library, and a tile source that is configuration | Proposed |
| [0026](ADR-0026-authentication.md) | The first administrator is made from a shell, not a browser | Proposed |
| [0027](ADR-0027-configuration-writes.md) | The file stays the source of truth, and the owner applies the change | Proposed |
| [0028](ADR-0028-parrot.md) | Parrot replays bytes it never understood | Proposed |
| [0029](ADR-0029-ipsc-from-capture.md) | IPSC is built from a capture, and the capture is the hard part | Proposed |
| [0030](ADR-0030-radio-id-lookup.md) | Radio IDs are looked up one at a time, and QSP says who is asking | Proposed |
| [0031](ADR-0031-loop-prevention.md) | A transmission is recognised by who sent it, not by where it arrived | Proposed — amended; 0051 replaces the blunt rule for QSP-to-QSP |
| [0032](ADR-0032-peering-is-agreed.md) | A peering is agreed by two people, and QSP can prove it was | Accepted |
| [0033](ADR-0033-last-heard-is-a-record.md) | Last heard is a record, and net control is who it is for | Accepted |
| [0034](ADR-0034-p25-is-native.md) | P25 is a network of its own, and audio is never transcoded to reach it | Accepted |
| [0035](ADR-0035-per-peer-passwords.md) | A member can be removed without changing everybody's password | Accepted |
| [0036](ADR-0036-ipsc-voice-is-not-a-dmr-burst.md) | IPSC voice is not a DMR burst, and bridging is not a copy | Accepted |
| [0037](ADR-0037-dmr-fec-is-a-wrapper-not-a-codec.md) | The DMR FEC is a wrapper, and QSP may add or remove it | Accepted |
| [0038](ADR-0038-routing-core-is-shared.md) | The routing core is reached by more than one listener, so it locks | Accepted |
| [0039](ADR-0039-the-peer-table-is-shared.md) | The peer table is shared too, and the fix belonged one layer down | Accepted |
| [0040](ADR-0040-the-air-interface-is-specified.md) | The air interface is specified, and IPSC is not | Accepted |
| [0041](ADR-0041-ipsc-transmit-from-inference.md) | Sending voice to a repeater is built from inference, not capture | Accepted — confirmed on air |
| [0042](ADR-0042-the-outbound-frame-shape-is-measured.md) | The outbound frame shape is measured, and the colour code belongs to the repeater | Accepted |
| [0043](ADR-0043-qsp-is-the-master.md) | QSP is the master, and a club runs no second one | Accepted |
| [0044](ADR-0044-access-control-covers-ipsc.md) | Access control covers IP Site Connect, with no new configuration | Accepted |
| [0045](ADR-0045-ipsc-text-messages.md) | Text over IP Site Connect is DMR data in the voice envelope | Accepted — amended; block sizes superseded by 0047 |
| [0046](ADR-0046-ipsc-private-calls.md) | A private call over IP Site Connect is `0x81` | Accepted |
| [0047](ADR-0047-rate-34-text-blocks.md) | A text message is Rate 3/4 blocks, and QSP carries them whole | Accepted — confirmed on air |
| [0048](ADR-0048-container-install.md) | The container install, and what it has to get right for a stranger | Accepted — built and run; nine defects found |
| [0049](ADR-0049-first-account-setup-token.md) | The first administrator account is created from a token in the log | Proposed |
| [0050](ADR-0050-a-reciprocal-says-so.md) | A reciprocal says so in the token, so an exchange can end after a restart | Accepted |
| [0051](ADR-0051-a-qsp-link-is-a-peer.md) | A link between two QSP servers is a peer, not a bridge | Accepted — confirmed on air |
| [0052](ADR-0052-qsp-is-federated.md) | QSP is a federated network | Accepted |
