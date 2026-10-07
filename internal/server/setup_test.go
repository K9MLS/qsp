package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/auth"
	"github.com/k9mls/qsp/internal/logging"
)

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

// TestEveryPageGoesToSetupUntilThereIsAnAdministrator is the test that would
// have caught it.
//
// **ADR-0056 says every path redirects to the wizard, and the first build did
// not do it.** The page existed and nothing sent anybody there: an operator
// installing QSP landed on Overview, with no way to learn what they were
// missing. Found by installing it, not by reading it.
func TestEveryPageGoesToSetupUntilThereIsAnAdministrator(t *testing.T) {
	srv := &Server{opts: Options{Setup: noAccounts{}}, log: logging.Discard()}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("the page"))
	})
	h := srv.beforeSetup(next)

	for _, path := range []string{"/", "/index.html", "/links", "/admin.html", "/join"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusFound {
			t.Errorf("%s answered %d rather than redirecting to setup", path, rec.Code)
		}
		if got := rec.Header().Get("Location"); got != "/setup" {
			t.Errorf("%s redirected to %q, want /setup", path, got)
		}
	}

	// **The wizard and what it needs must not redirect to themselves**, or an
	// operator gets a redirect loop and no idea why.
	for _, path := range []string{"/setup", "/setup.html", "/console.css", "/setup.js",
		"/tokens.css", "/mark.svg"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code == http.StatusFound {
			t.Errorf("%s redirects, which the wizard needs not to", path)
		}
	}
}

// Once an administrator exists nothing redirects, or a working console would
// send every visitor to a page that answers 404.
func TestNothingRedirectsOnceSetupIsDone(t *testing.T) {
	srv := &Server{opts: Options{Setup: haveAccounts{}}, log: logging.Discard()}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := srv.beforeSetup(next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("a configured server answered %d for /, want 200", rec.Code)
	}
}

type noAccounts struct{}

func (noAccounts) AnyAccount(ctx context.Context) (bool, error) { return false, nil }
func (noAccounts) CreateAccount(ctx context.Context, u, p string) (auth.Account, error) {
	return auth.Account{Username: u}, nil
}

type haveAccounts struct{}

func (haveAccounts) AnyAccount(ctx context.Context) (bool, error) { return true, nil }
func (haveAccounts) CreateAccount(ctx context.Context, u, p string) (auth.Account, error) {
	return auth.Account{Username: u}, nil
}
