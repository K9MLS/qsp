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
// What is missing is the talkgroup. Frames 0x66 to 0x69 each carry three bytes
// that were identical across every captured transmission, which is where the
// Link Control lives — but one radio on one talkgroup makes a constant field
// indistinguishable from constant framing. A capture with two talkgroups
// settles it; see testdata/p25/CAPTURE-REQUEST.md.
//
// So QSP can recognise and relay P25 today and cannot decide where a call
// goes, which is the honest state of it.
package p25
