package main

import (
	"os"
	"strings"
	"testing"
)

// TestTheConfiguredSessionLifetimeReachesTheAuthService is the half that is
// easy to leave out.
//
// The field, its default and its validation can all be right while the running
// service still constructs `auth.Policy{}` and ignores them — which is what
// this file did until 2026-09-14, in all three places that built a policy. A
// setting an operator can write and the server does not read is worse than no
// setting, because the file says it is configured.
//
// **Source inspection, and its limit stated**: it proves the value is passed,
// not that sessions then last that long. The behaviour is covered in
// internal/auth; this covers the join between them, which is where it was
// broken.
func TestTheConfiguredSessionLifetimeReachesTheAuthService(t *testing.T) {
	src, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatalf("reading app.go: %v", err)
	}
	text := string(src)

	i := strings.Index(text, "auth.NewService(repo, auth.Policy{")
	if i < 0 {
		t.Fatal("the auth service is no longer built here; this test needs rewriting")
	}
	policy := text[i : i+300]
	if !strings.Contains(policy, "SessionLifetime") {
		t.Error("the running server builds auth.Policy without a session " +
			"lifetime, so server.session_lifetime is written by an operator " +
			"and read by nothing")
	}
}
