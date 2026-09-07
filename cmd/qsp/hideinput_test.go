package main

import (
	"os"
	"strings"
	"testing"
)

// TestHidingAPasswordNeedsNoExternalProgram is the container's finding.
//
// adduser used to run `stty -echo`, and the image is a scratch layer with one
// static binary in it: no stty, no shell, no /bin. A server nobody can create
// an account on is a server nobody can sign in to, and that was the first thing
// an operator hit after a successful install.
func TestHidingAPasswordNeedsNoExternalProgram(t *testing.T) {
	src, err := os.ReadFile("hideinput_linux.go")
	if err != nil {
		t.Fatalf("%v", err)
	}
	// **The import, not the word.** A first version searched for "stty" and
	// failed on the comment explaining why stty was removed — too strict,
	// where this morning's console test was too lax for the same reason: both
	// were reading prose instead of code.
	if strings.Contains(string(src), `"os/exec"`) {
		t.Error("the Linux path still imports os/exec")
	}

	// And the caller must not have kept a copy of the old one.
	adduser, err := os.ReadFile("adduser.go")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if strings.Contains(string(adduser), `"os/exec"`) {
		t.Error("adduser.go still imports os/exec")
	}
}

// TestAPipedPasswordIsRefused keeps the property the stty version had.
//
// A password read from something that is not a terminal appears in whatever
// produced it: a shell history, a script, a CI log. **Refusing is the point of
// prompting at all**, and the ioctl failing with ENOTTY on a pipe is the check.
func TestAPipedPasswordIsRefused(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()

	saved := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = saved }()

	restore, err := hideInput()
	if err == nil {
		restore()
		t.Fatal("a pipe was accepted as a terminal; a password could be piped in")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
}
