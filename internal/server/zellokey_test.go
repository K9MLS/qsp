package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/secrets"
	"github.com/k9mls/qsp/internal/zellologon"
)

// memoryCredentials is a credential store with no database, so this runs in
// every build rather than only where SQLite is.
type memoryCredentials map[string]string

func (m memoryCredentials) List(context.Context) ([]secrets.Record, error) { return nil, nil }
func (m memoryCredentials) Get(_ context.Context, n string) (string, error) {
	v, ok := m[n]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}
func (m memoryCredentials) Set(_ context.Context, n, v, _ string) error { m[n] = v; return nil }
func (m memoryCredentials) Delete(_ context.Context, n string) error    { delete(m, n); return nil }
func (m memoryCredentials) Has(_ context.Context, n string) (bool, error) {
	_, ok := m[n]
	return ok, nil
}

// TestABadZelloKeyIsRefusedWhenItIsEntered.
//
// A PEM header one dash short looks entirely normal. Stored, it would fail
// only when the connector next logged on, as a refusal naming nothing.
//
// To see a row fail: delete the CheckPrivateKey block in handleSetSecret, and
// every bad key is stored with a 200.
func TestABadZelloKeyIsRefusedWhenItIsEntered(t *testing.T) {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	good := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	tests := []struct {
		name, credential, value string
		wantCode                int
	}{
		{"a real key", zellologon.PrivateKeyName, good, http.StatusOK},
		{"a header one dash short", zellologon.PrivateKeyName,
			strings.Replace(good, "-----BEGIN PRIVATE KEY-----", "----BEGIN PRIVATE KEY-----", 1), http.StatusBadRequest},
		{"the public half by mistake", zellologon.PrivateKeyName,
			"-----BEGIN PUBLIC KEY-----\nMIIBIjAN\n-----END PUBLIC KEY-----\n", http.StatusBadRequest},
		{"not a key at all", zellologon.PrivateKeyName, "hunter2", http.StatusBadRequest},
		{"any other credential is not parsed as a key", zellologon.PasswordName, "hunter2", http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := memoryCredentials{}
			srv, a := newSecretServer(t, store)
			body, _ := json.Marshal(map[string]string{"value": tc.value})
			rec := authed(t, srv, a, http.MethodPut, "/api/secrets/"+tc.credential, string(body))
			if rec.Code != tc.wantCode {
				t.Fatalf("storing gave %d, want %d: %s", rec.Code, tc.wantCode, rec.Body)
			}
			_, stored := store[tc.credential]
			if stored != (tc.wantCode == http.StatusOK) {
				t.Errorf("stored = %v after a %d", stored, rec.Code)
			}
			if tc.wantCode == http.StatusBadRequest && strings.Contains(rec.Body.String(), "BEGIN") {
				t.Errorf("the refusal echoes the pasted key back: %s", rec.Body)
			}
		})
	}
}
