package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/audit"
)

// TestNoAuditHelperIsPassedALiteral is the guard the Action type could not be.
//
// An untyped string literal converts to audit.Action without a word, so
// `recordSecret(r, "secret.set", …)` compiled, passed vet and staticcheck, and
// was refused by Validate at run time — for every credential stored, backup
// taken and peer credential issued, until 2026-09-16. This reads the package's
// source and refuses a literal, or a conversion, where an action is passed.
//
// To see it bite: change one call back to `s.recordSecret(r, "secret.set", …)`.
func TestNoAuditHelperIsPassedALiteral(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", f, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !strings.HasPrefix(sel.Sel.Name, "record") || len(call.Args) < 2 {
				return true
			}
			checked++
			switch arg := call.Args[1].(type) {
			case *ast.BasicLit:
				if arg.Kind == token.STRING {
					t.Errorf("%s: %s is passed the literal %s; use a declared audit.Action constant, "+
						"or the event is refused at run time", fset.Position(arg.Pos()), sel.Sel.Name, arg.Value)
				}
			case *ast.CallExpr:
				if s, ok := arg.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Action" {
					t.Errorf("%s: %s is passed a conversion to audit.Action; use a declared constant",
						fset.Position(arg.Pos()), sel.Sel.Name)
				}
			}
			return true
		})
	}
	if checked < 10 {
		t.Fatalf("checked only %d calls to record helpers; this test has gone blind", checked)
	}
}

// TestNoAuditEventIsBuiltWithALiteral is the same guard for the other way an
// action reaches Record: an audit.Event written out by hand.
//
// `Action: "service.restart.requested"` in restart.go compiled for the same
// reason the helper calls did, sat outside what the test above reads, and was
// refused at run time on every console restart until 0.1.342. This reads this
// package and cmd/qsp, which builds the started and stopped events, and refuses
// a string literal or a conversion as an Event's Action.
//
// To see it bite: put `Action: "service.restart.requested",` back in
// restart.go.
func TestNoAuditEventIsBuiltWithALiteral(t *testing.T) {
	for _, tc := range []struct {
		dir string
		// least is how many Event literals the directory is known to hold, so
		// a parser that stops matching fails rather than passing on nothing.
		least int
	}{
		{dir: ".", least: 8},
		{dir: "../../cmd/qsp", least: 2},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			files, err := filepath.Glob(filepath.Join(tc.dir, "*.go"))
			if err != nil {
				t.Fatal(err)
			}
			fset := token.NewFileSet()
			events := 0
			for _, f := range files {
				if strings.HasSuffix(f, "_test.go") {
					continue
				}
				file, err := parser.ParseFile(fset, f, nil, 0)
				if err != nil {
					t.Fatalf("parsing %s: %v", f, err)
				}
				ast.Inspect(file, func(n ast.Node) bool {
					lit, ok := n.(*ast.CompositeLit)
					if !ok {
						return true
					}
					typ, ok := lit.Type.(*ast.SelectorExpr)
					if !ok || typ.Sel.Name != "Event" {
						return true
					}
					if pkg, ok := typ.X.(*ast.Ident); !ok || pkg.Name != "audit" {
						return true
					}
					events++
					for _, elt := range lit.Elts {
						kv, ok := elt.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						if key, ok := kv.Key.(*ast.Ident); !ok || key.Name != "Action" {
							continue
						}
						switch v := kv.Value.(type) {
						case *ast.BasicLit:
							t.Errorf("%s: an audit.Event is built with the action %s; use a "+
								"declared audit.Action constant, or Record refuses it at run time",
								fset.Position(v.Pos()), v.Value)
						case *ast.CallExpr:
							if s, ok := v.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Action" {
								t.Errorf("%s: an audit.Event is built with a conversion to "+
									"audit.Action; use a declared constant", fset.Position(v.Pos()))
							}
						}
					}
					return true
				})
			}
			if events < tc.least {
				t.Fatalf("found %d audit.Event literals in %s, expected at least %d; "+
					"this test has gone blind", events, tc.dir, tc.least)
			}
		})
	}
}

// TestEveryCredentialAndBackupActionIsAccepted: each action the console emits
// for credentials and backups is one Validate accepts.
func TestEveryCredentialAndBackupActionIsAccepted(t *testing.T) {
	for _, a := range []audit.Action{
		audit.ActionSecretSet, audit.ActionSecretRemoved,
		audit.ActionPeerCredentialIssued, audit.ActionPeerCredentialRevoked,
		audit.ActionConfigExported, audit.ActionConfigRestored,
		audit.ActionConfigFullExported, audit.ActionConfigFullRestored,
		audit.ActionServiceRestartRequested,
	} {
		e := audit.Event{OccurredAt: time.Now(), Actor: "mike", Action: a, Outcome: audit.OutcomeSuccess}
		if err := e.Validate(); err != nil {
			t.Errorf("%s: %v", a, err)
		}
	}
}
