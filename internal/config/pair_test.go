package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shipped example configurations are validated here rather than trusted.
//
// **A broken example is worse than none.** Somebody following deploy/ has no
// reason to doubt a file the project ships, so a typo in one is debugged as a
// fault in QSP. Nothing else reads these files, so nothing else would notice.
func TestShippedExamplesAreValid(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "deploy", "*", "*.json"))
	if err != nil {
		t.Fatalf("globbing deploy: %v", err)
	}
	if len(paths) < 2 {
		t.Fatalf("found %d example configurations; this check has gone blind", len(paths))
	}
	for _, path := range paths {
		t.Run(filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path), func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatalf("opening: %v", err)
			}
			defer f.Close()
			if _, err := Load(f); err != nil {
				t.Errorf("%s does not load: %v", path, err)
			}
		})
	}
}

// TestThePairFacesItself.
//
// Two instances peered over OpenBridge, which has no connection establishment:
// each sends to an address agreed in advance rather than one discovered from
// the other's packets. **So the two files have to be read together to be
// right**, and a mismatched port pair produces a link that reports itself
// healthy while carrying nothing in one direction.
func TestThePairFacesItself(t *testing.T) {
	load := func(name string) Config {
		t.Helper()
		f, err := os.Open(filepath.Join("..", "..", "deploy", "pair", name))
		if err != nil {
			t.Fatalf("opening %s: %v", name, err)
		}
		defer f.Close()
		cfg, err := Load(f)
		if err != nil {
			t.Fatalf("loading %s: %v", name, err)
		}
		return cfg
	}

	alpha, bravo := load("alpha.json"), load("bravo.json")
	if len(alpha.DMR.Upstreams) != 1 || len(bravo.DMR.Upstreams) != 1 {
		t.Fatal("each instance needs exactly one link to the other")
	}
	a, b := alpha.DMR.Upstreams[0], bravo.DMR.Upstreams[0]

	if a.Address != b.ListenAddress {
		t.Errorf("alpha sends to %s and bravo listens on %s", a.Address, b.ListenAddress)
	}
	if b.Address != a.ListenAddress {
		t.Errorf("bravo sends to %s and alpha listens on %s", b.Address, a.ListenAddress)
	}
	if a.PassphraseFile != b.PassphraseFile {
		t.Error("OpenBridge authenticates on a shared passphrase; these read different files")
	}
	if a.NetworkID == b.NetworkID {
		t.Error("both instances announce the same network ID, so neither can tell " +
			"its own traffic from the other's")
	}

	// Every port either instance binds must be unique across the pair. Two
	// processes on one machine sharing a port is a second instance that starts,
	// reports healthy, and never receives anything.
	//
	// **Compared by port rather than by address.** 0.0.0.0:62041 and
	// 127.0.0.1:62041 are different strings and the same socket, so comparing
	// the whole address would pass a pair that cannot both start.
	port := func(addr string) string {
		if i := strings.LastIndex(addr, ":"); i >= 0 {
			return addr[i+1:]
		}
		return addr
	}
	seen := map[string]string{}
	for _, p := range []struct{ what, addr string }{
		{"alpha console", alpha.Server.ListenAddress},
		{"alpha DMR", alpha.DMR.ListenAddress},
		{"alpha link", a.ListenAddress},
		{"bravo console", bravo.Server.ListenAddress},
		{"bravo DMR", bravo.DMR.ListenAddress},
		{"bravo link", b.ListenAddress},
	} {
		if prev, ok := seen[port(p.addr)]; ok {
			t.Errorf("%s and %s both bind port %s", prev, p.what, port(p.addr))
		}
		seen[port(p.addr)] = p.what
	}

	// **The DMR listeners must be reachable from off this machine.** Bound to
	// loopback they are tidy and useless: no hotspot on the LAN can reach
	// either instance, so the harness observes nothing but its own console
	// polling and cannot answer the question it exists for.
	for _, l := range []struct{ what, addr string }{
		{"alpha", alpha.DMR.ListenAddress},
		{"bravo", bravo.DMR.ListenAddress},
	} {
		if strings.HasPrefix(l.addr, "127.") || strings.HasPrefix(l.addr, "[::1]") {
			t.Errorf("%s listens for peers on %s, which no hotspot can reach", l.what, l.addr)
		}
	}

	// The databases must differ too, and this is easy to get wrong by copying.
	if alpha.Database.DSN == bravo.Database.DSN {
		t.Error("both instances share one database file")
	}

	// **The pair exports and imports the same talkgroup on purpose.** It is the
	// ordinary club configuration and the one that would loop, which is exactly
	// what this harness exists to watch not happen.
	if len(a.Export) == 0 || len(a.Import) == 0 || len(b.Export) == 0 || len(b.Import) == 0 {
		t.Error("the pair must export and import the same talkgroup, or it exercises nothing")
	}
	if !strings.Contains(alpha.DMR.Join.NetworkName, "alpha") ||
		!strings.Contains(bravo.DMR.Join.NetworkName, "bravo") {
		t.Error("the instances are not named apart, so a console cannot say which is which")
	}
}
