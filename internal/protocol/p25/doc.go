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
// It is not implemented. As with the hbp package, implementation is blocked on
// captured real traffic for golden-frame fixtures, and on the licence question
// recorded in docs/adr/ADR-0008. testdata/p25 holds an idle capture and a
// request for the voice one, which is the only remaining blocker.
package p25
