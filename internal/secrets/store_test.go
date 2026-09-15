package secrets

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/database"
)

// The store's SQL.
//
// **These cannot run in the development container**, which has no SQLite
// driver — the same reason seven cmd/qsp tests cannot. They are written to run
// on the operator's machine, and the cryptography they sit on top of is tested
// separately in crypto_test.go where no driver is needed.
//
// Skipping rather than failing when the driver is absent, because a package
// that cannot be compiled against is worse than one whose tests are honest
// about where they run.

func store(t *testing.T) (*Store, context.Context) {
	t.Helper()
	if !database.DriverRegistered("sqlite") {
		t.Skip("no sqlite driver in this build; these tests run where one is")
	}
	ctx := context.Background()

	db, err := database.Open(ctx, nil, database.Options{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "qsp.db"),
	})
	if err != nil {
		t.Fatalf("opening a database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrating: %v", err)
	}

	s, err := Open(Options{
		DB:      db.SQL(),
		KeyPath: filepath.Join(t.TempDir(), "secrets.key"),
		Now:     func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("opening a store: %v", err)
	}
	return s, ctx
}

// TestASecretGoesInAndComesBack.
func TestASecretGoesInAndComesBack(t *testing.T) {
	s, ctx := store(t)

	const name, value = "transcoder.dvstick.zello", "a password with spaces and ünïcode"
	if err := s.Set(ctx, name, value, "mike"); err != nil {
		t.Fatalf("setting: %v", err)
	}
	got, err := s.Get(ctx, name)
	if err != nil {
		t.Fatalf("getting: %v", err)
	}
	if got != value {
		t.Errorf("the secret came back as %q", got)
	}

	// The empty string is a value, not an absence: a link configured with an
	// empty password and one with no password need different answers.
	if err := s.Set(ctx, "empty", "", "mike"); err != nil {
		t.Fatalf("setting an empty value: %v", err)
	}
	got, err = s.Get(ctx, "empty")
	if err != nil {
		t.Fatalf("getting an empty value: %v", err)
	}
	if got != "" {
		t.Errorf("an empty secret came back as %q", got)
	}
}

// TestAMissingSecretIsDistinctFromAnEmptyOne.
func TestAMissingSecretIsDistinctFromAnEmptyOne(t *testing.T) {
	s, ctx := store(t)

	_, err := s.Get(ctx, "never.set")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing secret gave %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "never.set") {
		t.Errorf("the error does not name the secret: %v", err)
	}

	has, err := s.Has(ctx, "never.set")
	if err != nil {
		t.Fatalf("checking: %v", err)
	}
	if has {
		t.Error("a secret that was never set reports as present")
	}

	if err := s.Set(ctx, "never.set", "", "mike"); err != nil {
		t.Fatalf("setting: %v", err)
	}
	has, err = s.Has(ctx, "never.set")
	if err != nil {
		t.Fatalf("checking: %v", err)
	}
	if !has {
		t.Error("a secret set to the empty string reports as absent")
	}
}

// TestASecondWriteReplacesRatherThanAccumulating.
//
// A table of every password a server has ever held is a liability, and rotation
// means the old value should stop existing.
func TestASecondWriteReplacesRatherThanAccumulating(t *testing.T) {
	s, ctx := store(t)
	const name = "dmr.password"

	if err := s.Set(ctx, name, "first", "mike"); err != nil {
		t.Fatalf("setting: %v", err)
	}
	if err := s.Set(ctx, name, "second", "blake"); err != nil {
		t.Fatalf("replacing: %v", err)
	}

	got, err := s.Get(ctx, name)
	if err != nil {
		t.Fatalf("getting: %v", err)
	}
	if got != "second" {
		t.Errorf("the secret is %q after a replacement, want the new value", got)
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("%d rows after two writes to one name; the old value still "+
			"exists somewhere", len(list))
	}
	if list[0].UpdatedBy != "blake" {
		t.Errorf("the author is %q, want the one who made the change", list[0].UpdatedBy)
	}
}

// TestAListingNamesSecretsAndNeverTheirValues.
//
// A page that displays a password leaks it to anybody looking at the screen,
// and nothing here decrypts — so a listing cannot leak a credential even if
// the page rendering it is wrong.
func TestAListingNamesSecretsAndNeverTheirValues(t *testing.T) {
	s, ctx := store(t)

	const value = "unmistakable-secret-value"
	for _, name := range []string{"b.second", "a.first", "c.third"} {
		if err := s.Set(ctx, name, value, "mike"); err != nil {
			t.Fatalf("setting %s: %v", name, err)
		}
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("%d records, want 3", len(list))
	}
	// Sorted, so a console shows a stable order rather than whatever the
	// database felt like.
	if list[0].Name != "a.first" || list[1].Name != "b.second" || list[2].Name != "c.third" {
		t.Errorf("the listing is not sorted by name: %+v", list)
	}
	for _, r := range list {
		if r.UpdatedAt.IsZero() {
			t.Errorf("%s has no timestamp; a credential nobody can date is a "+
				"credential nobody trusts", r.Name)
		}
		if r.UpdatedBy != "mike" {
			t.Errorf("%s records author %q", r.Name, r.UpdatedBy)
		}
	}

	// The Record type has no field for a value, which is the real guarantee —
	// this asserts the listing cannot be made to carry one by accident.
	for _, r := range list {
		if strings.Contains(r.Name+r.UpdatedBy, value) {
			t.Errorf("the value appears in the listing for %s", r.Name)
		}
	}
}

// TestDeletingSomethingThatIsNotThereIsNotAnError.
//
// The caller wanted it gone and it is gone. An error here would make every
// "remove this credential" path need a prior existence check, and the one that
// forgot would report a failure for a success.
func TestDeletingSomethingThatIsNotThereIsNotAnError(t *testing.T) {
	s, ctx := store(t)

	if err := s.Delete(ctx, "never.existed"); err != nil {
		t.Errorf("deleting a missing secret: %v", err)
	}

	if err := s.Set(ctx, "temporary", "value", "mike"); err != nil {
		t.Fatalf("setting: %v", err)
	}
	if err := s.Delete(ctx, "temporary"); err != nil {
		t.Fatalf("deleting: %v", err)
	}
	if _, err := s.Get(ctx, "temporary"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a deleted secret gave %v, want ErrNotFound", err)
	}
}

// TestAStoreWithAnotherKeyCannotReadWhatWasStored.
//
// The failure an operator will actually hit: the key file is lost, restored
// from a different backup, or regenerated. Every secret becomes unreadable, and
// **the message has to say the credential must be entered again** rather than
// reporting a missing secret — because those need entirely different actions.
func TestAStoreWithAnotherKeyCannotReadWhatWasStored(t *testing.T) {
	if !database.DriverRegistered("sqlite") {
		t.Skip("no sqlite driver in this build; these tests run where one is")
	}
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "qsp.db")

	db, err := database.Open(ctx, nil, database.Options{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	defer db.Close()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrating: %v", err)
	}

	keys := t.TempDir()
	first, err := Open(Options{DB: db.SQL(), KeyPath: filepath.Join(keys, "one.key")})
	if err != nil {
		t.Fatalf("opening a store: %v", err)
	}
	if err := first.Set(ctx, "dmr.password", "secret", "mike"); err != nil {
		t.Fatalf("setting: %v", err)
	}

	// The same database, a different key.
	second, err := Open(Options{DB: db.SQL(), KeyPath: filepath.Join(keys, "two.key")})
	if err != nil {
		t.Fatalf("opening a second store: %v", err)
	}

	_, err = second.Get(ctx, "dmr.password")
	if err == nil {
		t.Fatal("a store with a different key read the secret")
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("an undecryptable secret reported as missing; those need " +
			"different actions from an operator")
	}
	if !strings.Contains(err.Error(), "entered again") {
		t.Errorf("the error does not say what has to happen: %v", err)
	}

	// But it can still see that something is there, which is what a health
	// check needs in order to report the problem at all.
	has, err := second.Has(ctx, "dmr.password")
	if err != nil {
		t.Fatalf("checking: %v", err)
	}
	if !has {
		t.Error("a store with the wrong key cannot see that a secret exists, so " +
			"nothing can report the mismatch")
	}
}

// TestASecretNeedsAName, so an empty key cannot become a row nothing can find.
func TestASecretNeedsAName(t *testing.T) {
	s, ctx := store(t)
	if err := s.Set(ctx, "", "value", "mike"); err == nil {
		t.Error("a secret with no name was stored")
	}
}
