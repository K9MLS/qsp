// Package protocol groups QSP's wire protocol implementations.
//
// Each protocol lives in its own subpackage rather than behind a shared
// abstraction. DMR's Homebrew Protocol and P25 differ enough in framing,
// identity and timing that a common interface invented before either exists
// would constrain both. A shared abstraction may be extracted later, from
// working code rather than from speculation.
//
// The Homebrew Protocol is implemented in the hbp subpackage. Every message in
// testdata/hbp/ parses and round-trips byte-for-byte, the codec is fuzzed, and
// a WPSD hotspot completed the login handshake against it on 2026-08-23.
//
// P25 is a later phase. It is blocked on a capture containing an actual P25
// transmission — testdata/p25/ holds polling traffic only — and on the
// licensing question in ADR-0008; see docs/architecture/testing.md.
package protocol
