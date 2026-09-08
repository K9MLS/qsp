package audit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// TestEveryDeclaredActionIsKnown is the test that was missing while three
// actions were emitted and recorded none of the time.
//
// `peering.offered`, `peering.accepted` and `peering.removed` were sent to
// Record for weeks. Record rejected all three as undeclared, the caller logged
// a warning nobody read, and **SECURITY.md stated as fact that accepting a
// peering writes an audit event naming the far end whether it succeeds or
// fails.** ADR-0032 required it. The document was the only place it was true.
//
// # Why this parses the source
//
// Go cannot enumerate the constants of a type at run time, so the declaration
// and the map are two lists that must agree and nothing made them. Reading the
// AST is reading code, not prose: a constant inside a comment is not a
// declaration and cannot be mistaken for one, which is the failure mode a
// string search would have.
func TestEveryDeclaredActionIsKnown(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "audit.go", nil, 0)
	if err != nil {
		t.Fatalf("cannot read the action declarations: %v", err)
	}

	var found int
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		ident, ok := spec.Type.(*ast.Ident)
		if !ok || ident.Name != "Action" {
			return true
		}
		for i, name := range spec.Names {
			if i >= len(spec.Values) {
				continue
			}
			lit, ok := spec.Values[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Errorf("%s has an unreadable value %s", name.Name, lit.Value)
				continue
			}
			found++
			if !IsKnownAction(Action(value)) {
				t.Errorf("%s is declared as %q and is not in knownActions: "+
					"Record will reject every event using it, at run time, "+
					"into a log line", name.Name, value)
			}
		}
		return true
	})

	// A parser that matched nothing would pass silently, which is the shape of
	// green this project has been caught by three times.
	if found < 9 {
		t.Fatalf("found only %d action declarations; the parser is not reading them", found)
	}
}

// TestThePeeringActionsAreDeclared names them, because a count is what let a
// missing one hide before.
func TestThePeeringActionsAreDeclared(t *testing.T) {
	for _, a := range []Action{
		ActionPeeringOffered,
		ActionPeeringAccepted,
		ActionPeeringRemoved,
	} {
		if !IsKnownAction(a) {
			t.Errorf("%q is not a known action, so no peering using it is ever recorded", a)
		}
	}
}
