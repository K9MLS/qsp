// Package ipsc implements Motorola IP Site Connect, as far as it has been
// observed and no further.
//
// # Provenance
//
// There is no published IPSC specification. Every message in this package was
// derived from traffic captured in testdata/ipsc/. Per
// docs/adr/ADR-0029-ipsc-from-capture.md, no IPSC implementation source —
// DMRlink, HBlink3 or any other — has been read, ported or transliterated, and
// none may be read without recording the decision: doing so makes this package
// a derivative work permanently, and no later rewrite undoes it.
//
// # What has been observed
//
// Seven message types between two repeaters that registered to each other over
// the internet, plus one repeater talking into silence.
//
// A K9MLS XPR8300 on firmware R02.30.20 acted as master; a second repeater,
// remote and behind fourteen hops, acted as peer. Registration is a six-packet
// exchange rather than a request and an acknowledgement, and the link then
// settled into keepalives for twenty minutes.
//
// Four types have an understood purpose: 0x90 and 0x91 register, 0x96 and 0x97
// keep alive. Three do not: 0x85, 0xf0 and 0xf1. They are named for their bytes
// rather than given descriptive names, because a descriptive name is a claim.
//
// # The one structure everything agrees on
//
// Byte 0 is the type and bytes 1 to 4 are the sender's own radio ID, big-endian.
// That holds across seven types, two directions, two repeater models and two
// firmware versions, and it is the only structure this package encodes.
//
// It is the sender rather than the subject: a registration request carries the
// peer's ID and its reply carries the master's. One repeater talking into
// silence could not have shown that, because there was only ever one party —
// it took a capture with both ends in it.
//
// # What has not been observed
//
// Voice, private calls, text, and a clean disconnect. Nine of the sixteen bytes
// of 0x91 and thirty-nine of the forty-four of 0xf1 have no known meaning, and
// sixteen of the latter look like entropy rather than structure. A peer list is
// the obvious guess for 0xf1 and remains a guess: the capture contains one
// peer, so nothing distinguishes a list from a fixed record.
//
// # Four behaviours worth knowing before implementing a master
//
// Registered and unregistered peers run on different clocks. An unanswered peer
// retries 0x90 every ten seconds, flat, with no backoff and no give-up. A
// registered peer sends 0x96 every fifteen. An implementation using one
// interval for both is wrong in whichever state it was not written for, and
// looks correct when tested against itself.
//
// The peer does not source from the master's port. The XPR8300 addressed 50000
// and sent from 50002; the remote repeater addressed 50000 and sent from 50004.
// Reply to the port a datagram came from, never to the port it was sent to.
//
// ICMP port unreachable is ignored. A kernel refused every request eighty
// microseconds after it arrived and the cadence did not change, so a master
// cannot turn a peer away by staying silent. Refusal has to be an IPSC-level
// message, and no capture contains one.
//
// A Motorola repeater has two port settings and they are not the same field.
// One is the master it dials, one is the port it binds. Set to 50000 and 50001,
// a master that looked correctly configured served a port nobody was calling.
// testdata/ipsc/ipsc-phase2-master-not-bound.pcap is what that looks like.
package ipsc
