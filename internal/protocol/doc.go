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
// P25 over IP is built: the p25 package reads the frames and internal/p25link
// serves gateways. It was blocked on a capture containing real P25 voice, and
// testdata/p25 now holds three — voice, four talkgroups, and the registration
// exchange, which turned out not to exist.
//
// **Linking a Motorola Quantar is a different problem and is not built.** A
// Quantar's linking interface is a V.24 daughtercard running bit-oriented HDLC
// rather than anything over IP, and nothing here opens a serial port. See
// docs/P25-PLANNING.md.
package protocol
