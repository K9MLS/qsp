package dbtest

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Break it: return skip where decide returns fail, and a machine that was
// told the database tests must run goes back to passing without them.
func TestAMissingDriverIsAFailureWhereItWasAskedFor(t *testing.T) {
	tests := []struct {
		name       string
		registered bool
		require    string
		want       action
	}{
		{name: "a driver, nothing asked", registered: true, require: "", want: run},
		{name: "a driver, required", registered: true, require: "1", want: run},
		{name: "no driver, nothing asked", registered: false, require: "", want: skip},
		{name: "no driver, required", registered: false, require: "1", want: fail},
		{name: "no driver, required by any value", registered: false, require: "0", want: fail},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := decide(tc.registered, tc.require); got != tc.want {
				t.Errorf("decide(%v, %q) = %d, want %d", tc.registered, tc.require, got, tc.want)
			}
		})
	}
}

// The product registers its driver in cmd/qsp and nowhere else. This package
// registers one too, so anything but a test importing it would move that
// decision without anybody deciding.
//
// Break it: import this package from any file not ending in _test.go.
func TestOnlyTestsImportThisPackage(t *testing.T) {
	const path = `"github.com/k9mls/qsp/internal/database/dbtest"`
	root := filepath.Join("..", "..", "..")

	var testImporters int
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "build" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if !strings.Contains(string(body), path) {
			return nil
		}
		if strings.HasSuffix(p, "_test.go") {
			testImporters++
			return nil
		}
		t.Errorf("%s imports dbtest and is not a test; only cmd/qsp chooses the product's driver", p)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
	if testImporters == 0 {
		t.Fatal("no test imports dbtest; this check walked the wrong tree or the tests have gone")
	}
}
