package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// notAnAddress lists dotted numbers that are not addresses. ETSI clause
// numbers look exactly like one to a regular expression, and there is no way to
// tell 5.1.2.2 from a host by its shape alone.
//
// **A new clause number will fail this test, and that is the cheaper mistake.**
// Adding a line here is a moment's work; publishing somebody's home address is
// not undoable. Anything added must be a real clause or table number.
var notAnAddress = map[string]string{
	"5.1.2.2": "ETSI TS 102 361-1, the preamble clause",
	"5.1.2.3": "ETSI TS 102 361-1, the terminator clause",
	"7.1.1.4": "ETSI TS 102 361-2, a PDU layout",
	"7.1.1.5": "ETSI TS 102 361-2, the other PDU layout",
	"8.2.2.2": "ETSI TS 102 361-1, the control-pair block",
}

var dotted = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)

// TestTheRepositoryPublishesNoAddress refuses any public IP address in the
// repository, in any file.
//
// **The rule is no public addresses, not no known-sensitive ones.** The
// 2026-09-16 scrub replaced the addresses somebody had noticed and left the
// rest, and what remained turned out to include another operator's home IP in
// ten files — four of them Go tests, because a test written from a real capture
// keeps whatever address was on the wire. Judging each address on its merits is
// how that happened: every one of them looked fine to whoever wrote it.
//
// So there is nothing to judge now. Private ranges, loopback, link-local,
// multicast and the three documentation ranges are allowed; everything else
// fails, including addresses that are genuinely public infrastructure — a
// reflector, a DMR master, a resolver in an example. Losing those to
// 198.51.100.x costs a reader nothing, and it means no future capture can
// argue its way in.
func TestTheRepositoryPublishesNoAddress(t *testing.T) {
	var offenders []string

	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			rel = path
		}
		// Captures are read as bytes: an address inside one is ASCII text on
		// the wire, which is how three of them carried the operator's own.
		for _, m := range dotted.FindAll(b, -1) {
			ip := string(m)
			if _, ok := notAnAddress[ip]; ok {
				continue
			}
			if !isPublic(ip) {
				continue
			}
			offenders = append(offenders, fmt.Sprintf("%s in %s", ip, rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}

	if len(offenders) > 0 {
		shown := offenders
		if len(shown) > 10 {
			shown = shown[:10]
		}
		t.Errorf("the repository contains %d public address(es):\n  %s\n"+
			"use a documentation range — 203.0.113.x, 198.51.100.x or 192.0.2.x — "+
			"or add a clause number to notAnAddress if that is what it is",
			len(offenders), strings.Join(shown, "\n  "))
	}
}

// isPublic reports whether an address is one the internet routes to somebody.
func isPublic(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	var o [4]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n > 255 || (len(p) > 1 && p[0] == '0') {
			return false // not an address, or not written as one
		}
		o[i] = n
	}
	switch {
	case o[0] == 0, o[0] == 10, o[0] == 127:
		return false // this host, private, loopback
	case o[0] == 172 && o[1] >= 16 && o[1] <= 31:
		return false // private
	case o[0] == 192 && o[1] == 168:
		return false // private
	case o[0] == 100 && o[1] >= 64 && o[1] <= 127:
		return false // carrier-grade NAT, which QSP's docs discuss
	case o[0] == 169 && o[1] == 254:
		return false // link-local
	case o[0] >= 224:
		return false // multicast and reserved
	case o[0] == 203 && o[1] == 0 && o[2] == 113:
		return false // documentation, RFC 5737
	case o[0] == 198 && o[1] == 51 && o[2] == 100:
		return false // documentation
	case o[0] == 192 && o[1] == 0 && o[2] == 2:
		return false // documentation
	}
	return true
}

// TestIsPublicKnowsWhatItIsLookingAt keeps the classifier honest: it decides
// whether something is published, so a wrong answer either leaks an address or
// fails the build on a clause number.
//
// **The fixtures are octets, not strings, so this file contains no public
// address of its own.** The first version used a real one as its positive case
// and the test above flagged its own table — which is the guard working, and a
// reminder that a test is part of the repository it checks.
func TestIsPublicKnowsWhatItIsLookingAt(t *testing.T) {
	tests := []struct {
		name string
		in   [4]int
		want bool
	}{
		{name: "an ordinary routable address", in: [4]int{42, 42, 42, 42}, want: true},
		{name: "another, in a different block", in: [4]int{11, 22, 33, 44}, want: true},
		{name: "documentation range", in: [4]int{203, 0, 113, 60}, want: false},
		{name: "second documentation range", in: [4]int{198, 51, 100, 222}, want: false},
		{name: "third documentation range", in: [4]int{192, 0, 2, 1}, want: false},
		{name: "private", in: [4]int{192, 168, 1, 247}, want: false},
		{name: "private, the sixteen-bit block", in: [4]int{172, 17, 0, 1}, want: false},
		{name: "private, ten-dot", in: [4]int{10, 0, 0, 1}, want: false},
		{name: "just outside the sixteen-bit block", in: [4]int{172, 32, 0, 1}, want: true},
		{name: "loopback", in: [4]int{127, 0, 0, 1}, want: false},
		{name: "carrier-grade NAT, which the docs discuss", in: [4]int{100, 64, 0, 1}, want: false},
		{name: "just outside carrier-grade NAT", in: [4]int{100, 128, 0, 1}, want: true},
		{name: "link-local", in: [4]int{169, 254, 1, 1}, want: false},
		{name: "multicast", in: [4]int{239, 255, 255, 250}, want: false},
		{name: "wildcard", in: [4]int{0, 0, 0, 0}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := fmt.Sprintf("%d.%d.%d.%d", tc.in[0], tc.in[1], tc.in[2], tc.in[3])
			if got := isPublic(in); got != tc.want {
				t.Errorf("isPublic(%q) = %v, want %v", in, got, tc.want)
			}
		})
	}
}

// TestIsPublicRejectsWhatIsNotAnAddress covers the shapes that look like one to
// the regular expression: an octet out of range, and a version string with
// leading zeros, which appears inside a capture.
func TestIsPublicRejectsWhatIsNotAnAddress(t *testing.T) {
	// The five-octet case is built rather than written, because a five-octet
	// string holds a routable four-octet address as a substring and the guard
	// above reads it out of this file -- from the code, and then from the
	// comment explaining the code. Three times a fixture here has tripped the
	// rule it exists to test, which is the rule working.
	tooLong := fmt.Sprintf("%d.%d.%d.%d.%d", 1, 2, 3, 4, 5)
	for _, s := range []string{"256.1.1.1", "02.25.01.100", "1.2.3", tooLong, "a.b.c.d"} {
		t.Run(s, func(t *testing.T) {
			if isPublic(s) {
				t.Errorf("isPublic(%q) = true, want false; it is not an address", s)
			}
		})
	}
}
