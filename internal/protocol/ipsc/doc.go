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
// One message type, in one direction, from one repeater on one firmware.
//
// A Motorola XPR8300 running firmware R02.30.20, configured as an IPSC peer,
// was pointed at a host running nothing. It sent a fourteen-byte message
// beginning 0x90 every ten seconds, indefinitely, and the two captures differ
// in exactly one respect: the repeater's Radio ID was 100 in one and 3132910 in
// the other. That is what identifies the peer ID field and its width, and it is
// the only field in the message this package claims to understand.
//
// # What has not been observed
//
// Everything else. No master has ever replied to one of these messages, so the
// registration handshake beyond its first packet is unknown, as are keepalives,
// the peer list, voice, private calls, text and disconnect. Nine of the
// fourteen bytes have no known meaning. This package therefore parses 0x90 and
// rejects every other leading byte with ErrNotCaptured, rather than guessing.
//
// # Three behaviours worth knowing before implementing a master
//
// The retry interval is ten seconds flat, measured across thirty-five requests
// in two captures with a spread of three milliseconds. There is no backoff and
// no give-up: the repeater was still trying after five minutes.
//
// The peer sources from UDP 50002 while addressing the master on 50000. It does
// not use the master's port as its own, so an implementation that assumes
// symmetry will work against itself and fail against Motorola.
//
// ICMP port unreachable is ignored. The host's kernel answered every request
// with one, eighty microseconds behind it, and the repeater's cadence did not
// change. A master cannot refuse a peer by staying silent, so refusal has to be
// an IPSC-level message — and no such message has been captured.
package ipsc
