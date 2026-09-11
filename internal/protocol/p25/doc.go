// Package p25 will implement P25 reflector networking.
//
// **P25 is a network of its own, not a translation layer over DMR.** A P25 call
// between P25 endpoints crosses QSP without a vocoder, exactly as a DMR call
// does: P25 carries IMBE and DMR carries AMBE+2, so routing one through the
// other means decoding and re-encoding, and tandem vocoding always sounds
// worse. Most clubs will run one protocol or the other, and a P25-only club is
// the whole product for that club rather than a compatibility mode. See
// docs/adr/ADR-0034.
//
// **The frame layer is built and proven against a real capture**; routing is
// not. `testdata/p25/p25-voice.pcap` holds seven transmissions with no dropped
// frames, and all 565 of them round-trip byte for byte through frame.go.
//
// **The talkgroup and the source radio are located too**, from a second capture
// with fourteen transmissions across four talkgroups — one of them returned to
// after another had been used, which is what makes it a finding rather than a
// coincidence. Frame 0x65 carries the talkgroup and frame 0x66 the radio, and
// the operator confirmed all four numbers against the radio's own programming.
//
// So this package can now answer the three questions routing asks of a frame:
// what kind it is, which talkgroup it belongs to, and who sent it.
//
// What is not built is the listener — binding a port, answering a gateway's
// registration, and handing frames to the routing core. The registration
// exchange is still uncaptured: both captures began with the gateway already
// running or restarted underneath by a timer. See
// testdata/p25/CAPTURE-REQUEST.md.
package p25
