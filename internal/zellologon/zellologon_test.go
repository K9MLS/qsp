package zellologon

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/secrets"
)

type mapStore map[string]string

func (m mapStore) Get(_ context.Context, name string) (string, error) {
	v, ok := m[name]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}

func (m mapStore) Has(_ context.Context, name string) (bool, error) {
	_, ok := m[name]
	return ok, nil
}

// failingStore stands for a database QSP cannot read.
type failingStore struct{}

func (failingStore) Get(context.Context, string) (string, error) {
	return "", errors.New("database is locked")
}
func (failingStore) Has(context.Context, string) (bool, error) { return false, nil }

func testKey(t *testing.T) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func complete(t *testing.T) mapStore {
	return mapStore{PrivateKeyName: testKey(t), UsernameName: "k9mls-gateway", PasswordName: "hunter2",
		"peer-password": "must never be served"}
}

func serve(t *testing.T, store Getter, allow func(net.Conn) error) (string, *Server) {
	t.Helper()
	// A short directory: a Unix socket path is limited to about 108 bytes,
	// and t.TempDir() under a long test name can exceed it.
	dir, err := os.MkdirTemp("", "zl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "s")
	srv, err := Listen(Options{SocketPath: path, Store: store, Issuer: "issuer-from-portal", allowPeer: allow})
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.Serve(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return path, srv
}

// TestALogonIsHandedOutAndTheKeyIsNot is ADR-0066 as a test.
//
// To see it bite: add the PEM to the Logon struct and the reply, and the
// "the key" check fails on the raw bytes the connector received.
func TestALogonIsHandedOutAndTheKeyIsNot(t *testing.T) {
	store := complete(t)
	path, srv := serve(t, store, nil)

	var raw []byte
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write([]byte(`{"want":"logon"}` + "\n"))
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 8192)
	n, _ := conn.Read(buf)
	raw = buf[:n]
	conn.Close()

	for _, secret := range []string{"PRIVATE KEY", "must never be served",
		strings.Split(store[PrivateKeyName], "\n")[1]} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the reply carries %q; only a token, a username and a password may cross", secret)
		}
	}

	logon, err := Fetch(context.Background(), path)
	if err != nil {
		t.Fatalf("fetching: %v", err)
	}
	if logon.Username != "k9mls-gateway" || logon.Password != "hunter2" {
		t.Errorf("got %+v", logon)
	}
	parts := strings.Split(logon.Token, ".")
	if len(parts) != 3 {
		t.Fatalf("the token %q is not a JWT", logon.Token)
	}
	claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c struct {
		Iss string `json:"iss"`
		Exp int64  `json:"exp"`
	}
	_ = json.Unmarshal(claims, &c)
	if c.Iss != "issuer-from-portal" || time.Until(time.Unix(c.Exp, 0)) > 2*time.Hour {
		t.Errorf("claims %+v; want the configured issuer and a short expiry", c)
	}
	if served, _, _ := srv.Counters(); served != 2 {
		t.Errorf("served %d, want 2", served)
	}
}

// TestARefusalSaysWhatTheOperatorShouldDo.
//
// To see a row fail: map every store error to KindMissing in logon, and
// "the store cannot be read" tells an operator to re-enter a password that
// is not the problem.
func TestARefusalSaysWhatTheOperatorShouldDo(t *testing.T) {
	missing := func(name string) Getter {
		s := complete(t)
		delete(s, name)
		return s
	}
	badKey := complete(t)
	badKey[PrivateKeyName] = "-----BEGIN PRIVATE KEY----\nnot a key\n-----END PRIVATE KEY-----"
	tests := []struct {
		name     string
		store    Getter
		wantKind string
		wantText string
	}{
		{"no private key", missing(PrivateKeyName), KindMissing, PrivateKeyName},
		{"no username", missing(UsernameName), KindMissing, UsernameName},
		{"no password", missing(PasswordName), KindMissing, PasswordName},
		{"a key that does not parse", badKey, KindUnusable, ""},
		{"the store cannot be read", failingStore{}, KindUnavailable, "database is locked"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, _ := serve(t, tc.store, nil)
			_, err := Fetch(context.Background(), path)
			var ze *Error
			if !errors.As(err, &ze) {
				t.Fatalf("error %v is not a refusal from QSP", err)
			}
			if ze.Kind != tc.wantKind || !strings.Contains(ze.Message, tc.wantText) {
				t.Errorf("refusal %q %q, want kind %q mentioning %q", ze.Kind, ze.Message, tc.wantKind, tc.wantText)
			}
		})
	}
}

// TestAnotherUserIsServedNothing: the second lock.
//
// To see it bite: skip the allowPeer call in answer.
func TestAnotherUserIsServedNothing(t *testing.T) {
	path, srv := serve(t, complete(t), func(net.Conn) error {
		return errors.New("uid 1001 asked for a logon and only uid 999 is served")
	})
	if _, err := Fetch(context.Background(), path); err == nil {
		t.Fatal("a refused peer received a logon")
	}
	if _, peer, _ := srv.Counters(); peer != 1 {
		t.Errorf("refused peer %d, want 1", peer)
	}
}

// TestTheRealPeerCheckAdmitsThisUser runs samePeerUser for real: the test
// process is the same user on both ends, so it must pass.
func TestTheRealPeerCheckAdmitsThisUser(t *testing.T) {
	path, _ := serve(t, complete(t), nil)
	if _, err := Fetch(context.Background(), path); err != nil {
		t.Fatalf("the same user was refused: %v", err)
	}
}

// TestTheSocketIsItsOwnersAlone and a stale one is replaced.
//
// To see it bite: delete the Chmod in Listen (the umask here leaves 0755).
func TestTheSocketIsItsOwnersAlone(t *testing.T) {
	path, _ := serve(t, complete(t), nil)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("the socket's mode is %o, want 600", perm)
	}
}

// TestAPathThatIsNotASocketIsNeverRemoved: a mistaken path must not delete
// whatever the operator keeps there.
func TestAPathThatIsNotASocketIsNeverRemoved(t *testing.T) {
	dir, _ := os.MkdirTemp("", "zl")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "important")
	if err := os.WriteFile(path, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(Options{SocketPath: path, Store: mapStore{}, Issuer: "x"}); err == nil {
		t.Fatal("Listen succeeded over a regular file")
	}
	if b, _ := os.ReadFile(path); string(b) != "keep me" {
		t.Error("the file at the socket path was removed or changed")
	}
}
