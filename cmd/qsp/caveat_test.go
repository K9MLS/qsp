package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNoCaveatDeniesAFixtureThatExists.
//
// The IPSC relay logged "built from inference; no capture of a master sending
// voice exists" on every start. That stopped being true on 2026-09-03, when
// ipsc-master-voice.pcap was captured — 347 packets of an XPR8300's own RF, 288
// of them voice, read by four tests including internal/ipscbridge/master_test.go.
//
// **A stale caveat is worse than none.** On 2026-09-08 a repeater keyed up on
// network audio and transmitted silence, and that line was read twice as
// evidence the direction could not be verified, while the reference to verify
// it against had been in the repository for five days. It cost an hour and sent
// the search the wrong way.
//
// This reads the source rather than the log because the claim lives in the
// source, and the fixture list is the only thing that can contradict it.
func TestNoCaveatDeniesAFixtureThatExists(t *testing.T) {
	// **String literals only, via the AST.** Reading the whole file matches
	// the comment that explains this fix, which quotes the old caveat — so the
	// test failed against the corrected code. What is logged is a literal, and
	// a literal is what this is about.
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "app.go", nil, 0)
	if err != nil {
		t.Fatalf("cannot read app.go: %v", err)
	}
	var literals []string
	ast.Inspect(file, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if v, err := strconv.Unquote(lit.Value); err == nil {
				literals = append(literals, v)
			}
		}
		return true
	})
	if len(literals) < 20 {
		t.Fatalf("found only %d string literals; the parser is not reading app.go", len(literals))
	}
	text := strings.Join(literals, "\n")

	// The fixture the old caveat denied.
	fixture := filepath.Join("..", "..", "testdata", "ipsc", "ipsc-master-voice.pcap")
	if _, err := os.Stat(fixture); err != nil {
		t.Skipf("no master-voice fixture in the tree, so nothing to contradict: %v", err)
	}

	for _, denial := range []string{
		"no capture of a master sending voice exists",
		"no capture of a master sending voice",
	} {
		if strings.Contains(text, denial) {
			t.Errorf("app.go still says %q while %s is in the repository and "+
				"four tests read it", denial, "testdata/ipsc/ipsc-master-voice.pcap")
		}
	}

	// And the caveat that replaced it has to point somewhere usable, or it is
	// a warning with no next step.
	if !strings.Contains(text, "ipsc-master-voice.pcap") {
		t.Error("the IPSC relay caveat names no reference to measure against")
	}
}
