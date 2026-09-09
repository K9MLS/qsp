package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// **The exemption must come from the connection, never from a header.**
// `X-Forwarded-For` is whatever the client wrote, so trusting it would let
// anybody claim to be local — which is the whole exemption, handed over.
func TestLoopbackIsReadFromTheConnectionNotAHeader(t *testing.T) {
	for _, tc := range []struct {
		name string
		addr string
		want bool
	}{
		{"IPv4 loopback", "127.0.0.1:51234", true},
		{"IPv6 loopback", "[::1]:51234", true},
		{"another loopback address", "127.0.0.53:51234", true},
		{"a LAN address", "192.168.1.27:51234", false},
		{"a public address", "203.0.113.7:51234", false},
		{"nonsense", "not-an-address", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/setup", nil)
			r.RemoteAddr = tc.addr
			if got := fromLoopback(r); got != tc.want {
				t.Errorf("fromLoopback(%q) is %v, want %v", tc.addr, got, tc.want)
			}
		})
	}

	// A forwarded header claiming loopback changes nothing.
	r := httptest.NewRequest("GET", "/api/setup", nil)
	r.RemoteAddr = "203.0.113.7:51234"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	r.Header.Set("X-Real-IP", "127.0.0.1")
	if fromLoopback(r) {
		t.Error("a forwarded header claiming loopback was believed; anybody could send one")
	}
}

// The token is unguessable and different every time. It guards a window of
// minutes, so it is sized against guessing rather than against an offline
// attack — there is nothing here to attack offline.
func TestASetupTokenIsUnguessableAndFresh(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		token, err := newSetupToken()
		if err != nil {
			t.Fatalf("newSetupToken: %v", err)
		}
		if len(token) != setupTokenBytes*2 {
			t.Fatalf("the token is %d characters, want %d", len(token), setupTokenBytes*2)
		}
		if seen[token] {
			t.Fatalf("two tokens collided: %q", token)
		}
		seen[token] = true
	}
}

// **A refusal must not say which way it was wrong.** An error distinguishing a
// wrong token from an absent one tells somebody probing that they are close.
func TestARefusedTokenSaysNothingUseful(t *testing.T) {
	body := readSource(t, "setup.go")

	// **The message itself, not the file.** A first version searched the whole
	// source and matched the comment explaining this very rule — a check that
	// fails on the prose describing it is a check nobody keeps.
	at := strings.Index(body, `"error": "that setup token`)
	if at < 0 {
		t.Fatal("the refusal message has been reworded; check it still says nothing useful")
	}
	end := strings.Index(body[at:], "})")
	message := strings.ToLower(body[at : at+end])

	for _, leak := range []string{"no token", "empty", "wrong token", "does not match",
		"expected", "should be"} {
		if strings.Contains(message, leak) {
			t.Errorf("the refusal distinguishes how the token was wrong: %q", leak)
		}
	}
	if !strings.Contains(body, "ConstantTimeCompare") {
		t.Error("the token is compared with ==, which leaks it a character at a time to " +
			"somebody willing to measure")
	}
}

// **Unreachable is not \"no administrator\".** Serving the setup page because a
// query failed would offer the server to anybody during a database problem,
// which is exactly when nobody is watching.
func TestAFailedLookupDoesNotOpenSetup(t *testing.T) {
	body := readSource(t, "setup.go")
	at := strings.Index(body, "cannot tell whether this server has an administrator")
	if at < 0 {
		t.Fatal("the failed-lookup path does not log")
	}
	// The lines after that log call must return false — not fall through to
	// treating the error as an empty database.
	after := body[at:]
	if !strings.Contains(after[:200], "return false") {
		t.Error("a failed lookup does not return false, so a database problem opens setup")
	}
}
