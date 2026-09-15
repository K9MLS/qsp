package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"
)

// The encrypted full backup: ADR-0065.

func fullBackupFixture(t *testing.T) FullBackup {
	t.Helper()
	cfg := Default()
	cfg.DMR.Enabled = true
	cfg.DMR.PasswordFile = "/var/lib/qsp/peer.pass"
	return NewFullBackup(cfg, map[string]string{
		"dmr.password":              "the peer password",
		"upstream.bcara.passphrase": "the openbridge passphrase",
		"transcoder.dvstick.zello":  "the zello password",
	}, "0.1.224", time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC))
}

// TestAFullBackupCarriesSecretsAndComesBack is the whole point of the second
// format: a restore that comes up with links that work.
func TestAFullBackupCarriesSecretsAndComesBack(t *testing.T) {
	want := fullBackupFixture(t)
	const passphrase = "a passphrase with spaces and ünïcode"

	var buf bytes.Buffer
	if err := WriteFullBackup(&buf, want, passphrase); err != nil {
		t.Fatalf("writing: %v", err)
	}

	got, err := ReadFullBackup(bytes.NewReader(buf.Bytes()), passphrase)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(got.Secrets) != len(want.Secrets) {
		t.Fatalf("%d secrets came back, want %d", len(got.Secrets), len(want.Secrets))
	}
	for name, value := range want.Secrets {
		if got.Secrets[name] != value {
			t.Errorf("%s came back as %q", name, got.Secrets[name])
		}
	}
	// And the configuration half, so the file is a backup rather than a
	// keyring.
	if got.Backup.QSPVersion != want.Backup.QSPVersion {
		t.Errorf("the version came back as %q", got.Backup.QSPVersion)
	}
	if !got.Backup.Config.DMR.Enabled {
		t.Error("the configuration did not survive")
	}

	// Sorted names, so a restore can show what came back rather than saying
	// "restored".
	names := got.SecretNames()
	if len(names) != 3 || names[0] != "dmr.password" {
		t.Errorf("the names are %v, want them sorted", names)
	}
}

// TestNoSecretAppearsInTheFileInTheClear is the property that makes the format
// worth having at all.
//
// A "full backup" whose passwords are readable with `strings` would be the
// shareable export with a misleading name — and an operator would treat it as
// safe on the strength of the word encrypted.
func TestNoSecretAppearsInTheFileInTheClear(t *testing.T) {
	f := fullBackupFixture(t)
	var buf bytes.Buffer
	if err := WriteFullBackup(&buf, f, "passphrase"); err != nil {
		t.Fatalf("writing: %v", err)
	}
	raw := buf.Bytes()

	for name, value := range f.Secrets {
		if bytes.Contains(raw, []byte(value)) {
			t.Errorf("the secret %s appears in the file in the clear", name)
		}
	}
	// Nor should the secret names, nor anything from the configuration: the
	// whole payload is encrypted, not just the credentials.
	for _, s := range []string{"dmr.password", "transcoder.dvstick", "peer.pass"} {
		if bytes.Contains(raw, []byte(s)) {
			t.Errorf("%q appears in the file in the clear", s)
		}
	}

	// The passphrase itself must be nowhere near it.
	if bytes.Contains(raw, []byte("passphrase")) {
		t.Error("the passphrase appears in the file")
	}
}

// TestTheWrongPassphraseIsRefusedAndSaysWhatItCannotTell.
//
// GCM cannot distinguish a wrong passphrase from an altered byte: both fail
// authentication identically. **Claiming to know which would be a guess
// presented as a diagnosis**, so the message says both possibilities.
func TestTheWrongPassphraseIsRefusedAndSaysWhatItCannotTell(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFullBackup(&buf, fullBackupFixture(t), "right"); err != nil {
		t.Fatalf("writing: %v", err)
	}

	_, err := ReadFullBackup(bytes.NewReader(buf.Bytes()), "wrong")
	if !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("a wrong passphrase gave %v", err)
	}
	if !strings.Contains(err.Error(), "altered") {
		t.Errorf("the error does not admit it cannot tell a wrong passphrase "+
			"from a damaged file: %v", err)
	}

	// An empty passphrase is not a shortcut either.
	if _, err := ReadFullBackup(bytes.NewReader(buf.Bytes()), ""); err == nil {
		t.Error("an empty passphrase opened the backup")
	}
}

// TestAnAlteredByteAnywhereIsRefused, including in the header.
//
// **The header travels in the clear and is authenticated**, which is what
// stops an attacker rewriting the iteration count down to 1 and handing the
// file back for a cheaper attack. That is the reason it is passed as additional
// authenticated data, and this is the test of it.
func TestAnAlteredByteAnywhereIsRefused(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFullBackup(&buf, fullBackupFixture(t), "right"); err != nil {
		t.Fatalf("writing: %v", err)
	}
	raw := buf.Bytes()

	// Every byte of the header, and a sample of the body.
	positions := []int{}
	for i := 0; i < fullBackupHeaderBytes; i++ {
		positions = append(positions, i)
	}
	for i := fullBackupHeaderBytes; i < len(raw); i += 37 {
		positions = append(positions, i)
	}

	for _, i := range positions {
		bad := bytes.Clone(raw)
		bad[i] ^= 1
		if _, err := ReadFullBackup(bytes.NewReader(bad), "right"); err == nil {
			t.Errorf("flipping bit 0 of byte %d still opened the backup", i)
		}
	}

	// The specific attack: lower the iteration count so a brute force is
	// cheap. Byte 10 to 13 hold it.
	weakened := bytes.Clone(raw)
	binary.BigEndian.PutUint32(weakened[10:14], 1)
	if _, err := ReadFullBackup(bytes.NewReader(weakened), "right"); err == nil {
		t.Error("the iteration count was rewritten to 1 and the file still " +
			"opened; the header is not authenticated")
	}
}

// TestTwoBackupsOfTheSameThingShareNoDerivedKey.
//
// A per-file salt, so the same server backed up twice under the same
// passphrase produces two unrelated files. Without it, an attacker who cracked
// one passphrase would have every backup ever made with it, and identical
// ciphertext would show that two files hold the same configuration.
func TestTwoBackupsOfTheSameThingShareNoDerivedKey(t *testing.T) {
	f := fullBackupFixture(t)

	var a, b bytes.Buffer
	if err := WriteFullBackup(&a, f, "same"); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if err := WriteFullBackup(&b, f, "same"); err != nil {
		t.Fatalf("writing: %v", err)
	}

	saltA := a.Bytes()[14:fullBackupHeaderBytes]
	saltB := b.Bytes()[14:fullBackupHeaderBytes]
	if bytes.Equal(saltA, saltB) {
		t.Fatal("two backups share a salt, so they share a derived key")
	}
	if bytes.Equal(saltA, make([]byte, len(saltA))) {
		t.Fatal("the salt is all zeros; nothing random was read")
	}
	if bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("two backups of the same content are byte-identical")
	}

	// Both still open.
	for i, buf := range []*bytes.Buffer{&a, &b} {
		if _, err := ReadFullBackup(bytes.NewReader(buf.Bytes()), "same"); err != nil {
			t.Errorf("backup %d did not open: %v", i, err)
		}
	}
}

// TestTheShareableExportIsNotMistakenForAFullBackup is the confusion having
// two formats creates.
//
// **Mailing the wrong file publishes every password on the server**, so the
// two must be distinguishable, and an import of the wrong one must say which
// it found rather than only complaining.
func TestTheShareableExportIsNotMistakenForAFullBackup(t *testing.T) {
	var shareable bytes.Buffer
	if err := WriteBackup(&shareable, NewBackup(Default(), "0.1.224", time.Now())); err != nil {
		t.Fatalf("writing the shareable export: %v", err)
	}

	_, err := ReadFullBackup(bytes.NewReader(shareable.Bytes()), "passphrase")
	if !errors.Is(err, ErrNotAFullBackup) {
		t.Errorf("the shareable export was read as a full backup: %v", err)
	}
	if !strings.Contains(err.Error(), "different format") {
		t.Errorf("the error does not say what it found: %v", err)
	}

	// And the full backup is not readable as the shareable export, or an
	// operator could import a keyring believing it was configuration.
	var full bytes.Buffer
	if err := WriteFullBackup(&full, fullBackupFixture(t), "p"); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if _, err := ReadBackup(bytes.NewReader(full.Bytes())); err == nil {
		t.Error("an encrypted full backup was read as the shareable export")
	}

	for name, b := range map[string][]byte{
		"nothing":                          nil,
		"a short file":                     {'Q', 'S', 'P'},
		"the right magic and nothing else": FullBackupMagic[:],
	} {
		if _, err := ReadFullBackup(bytes.NewReader(b), "p"); err == nil {
			t.Errorf("%s was read as a full backup", name)
		}
	}
}

// TestANewerFormatIsRefusedEntirely, for ADR-0054's reason.
//
// Importing three-quarters of a configuration is worse than importing none,
// because the quarter that was dropped is invisible and the operator believes
// they have restored a server.
func TestANewerFormatIsRefusedEntirely(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFullBackup(&buf, fullBackupFixture(t), "p"); err != nil {
		t.Fatalf("writing: %v", err)
	}
	raw := bytes.Clone(buf.Bytes())
	binary.BigEndian.PutUint16(raw[8:10], FullBackupFormat+1)

	_, err := ReadFullBackup(bytes.NewReader(raw), "p")
	if err == nil {
		t.Fatal("a newer format was accepted")
	}
	if !strings.Contains(err.Error(), "invisible") {
		t.Errorf("the refusal does not say why partial is worse than none: %v", err)
	}
}

// TestAPassphraseIsRequiredWhenWriting.
//
// A file carrying every credential on the server with no passphrase is one
// nobody could safely keep, and it would be indistinguishable from the
// shareable export in every way except the damage.
func TestAPassphraseIsRequiredWhenWriting(t *testing.T) {
	var buf bytes.Buffer
	err := WriteFullBackup(&buf, fullBackupFixture(t), "")
	if err == nil {
		t.Fatal("a full backup was written with no passphrase")
	}
	if buf.Len() != 0 {
		t.Errorf("%d bytes were written before the refusal", buf.Len())
	}
	if !strings.Contains(err.Error(), "credential") {
		t.Errorf("the refusal does not say what the file would contain: %v", err)
	}
}

// TestTheKeyDerivationIsWhatTheConstantsSay.
//
// The iteration count is OWASP's recommendation for PBKDF2-HMAC-SHA256 rather
// than a number that felt large, and the key is AES-256. Both asserted,
// because a silently lowered count is a weaker file that looks identical.
func TestTheKeyDerivationIsWhatTheConstantsSay(t *testing.T) {
	if FullBackupIterations != 600_000 {
		t.Errorf("the iteration count is %d and OWASP's recommendation for "+
			"PBKDF2-HMAC-SHA256 is 600000", FullBackupIterations)
	}
	if fullBackupKeyBytes != 32 {
		t.Errorf("the derived key is %d bytes; AES-256 is 32", fullBackupKeyBytes)
	}
	if FullBackupSaltBytes < 16 {
		t.Errorf("the salt is %d bytes, which is short for a per-file salt",
			FullBackupSaltBytes)
	}
	if sha256.Size != 32 {
		t.Errorf("SHA-256 is %d bytes", sha256.Size)
	}

	// And the count written into the file is the one used, so a reader
	// derives the same key.
	var buf bytes.Buffer
	if err := WriteFullBackup(&buf, fullBackupFixture(t), "p"); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if got := binary.BigEndian.Uint32(buf.Bytes()[10:14]); int(got) != FullBackupIterations {
		t.Errorf("the file declares %d iterations and the constant is %d",
			got, FullBackupIterations)
	}
}

// TestThePassphraseWarningSaysTheThingThatMatters.
//
// ADR-0065 requires QSP to state, when it creates the file, that the
// passphrase is the operator's to keep and that losing it makes the file
// useless — because a full backup nobody can decrypt is worse than a partial
// one, on account of what the operator believes about it.
func TestThePassphraseWarningSaysTheThingThatMatters(t *testing.T) {
	w := strings.ToLower(PassphraseWarning)
	for _, must := range []string{"does not store", "cannot be opened"} {
		if !strings.Contains(w, must) {
			t.Errorf("the warning does not say %q: %s", must, PassphraseWarning)
		}
	}
	// It has to say where not to keep it, which is the actual failure: a
	// passphrase on the server dies with the server.
	if !strings.Contains(w, "other than this server") {
		t.Errorf("the warning does not say to keep it elsewhere: %s", PassphraseWarning)
	}
}

// TestAFullBackupDoesNotClaimToBeMissingWhatItCarries.
//
// `NewBackup` fills `Missing` with every credential the **shareable** export
// cannot hold — the right answer for that file and the wrong one here. An
// import showing "the peer password was not restored" over a file containing
// it would send an operator to re-enter a credential that already works, and
// would make the list nobody trusts.
//
// The two formats also identify secrets differently: the shareable export by
// the path a secret lived at, a full backup by the name configuration refers
// to it by. Narrowing the list would mean guessing which path is which name,
// and **a list that is wrong about a credential is worse than no list.** So it
// is cleared, and `SecretNames` is the positive statement that replaces it.
func TestAFullBackupDoesNotClaimToBeMissingWhatItCarries(t *testing.T) {
	cfg := Default()
	cfg.DMR.Enabled = true
	cfg.DMR.PasswordFile = "/var/lib/qsp/peer.pass"

	// The shareable export names what it cannot bring, which is the behaviour
	// this test depends on being different.
	shareable := NewBackup(cfg, "0.1.224", time.Now())
	if len(shareable.Missing) == 0 {
		t.Skip("the shareable export lists no missing credentials for this " +
			"configuration, so there is nothing here to distinguish")
	}

	full := NewFullBackup(cfg, map[string]string{
		"dmr.password": "the peer password",
	}, "0.1.224", time.Now())

	if len(full.Backup.Missing) != 0 {
		t.Errorf("a full backup carries a missing-credentials list of %d entries; "+
			"an import would tell the operator to re-enter something the file "+
			"restored: %+v", len(full.Backup.Missing), full.Backup.Missing)
	}

	// And it survives the round trip that way, so the cleared list is what an
	// import sees rather than something recomputed.
	var buf bytes.Buffer
	if err := WriteFullBackup(&buf, full, "p"); err != nil {
		t.Fatalf("writing: %v", err)
	}
	got, err := ReadFullBackup(bytes.NewReader(buf.Bytes()), "p")
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(got.Backup.Missing) != 0 {
		t.Errorf("the missing list came back with %d entries", len(got.Backup.Missing))
	}
	if names := got.SecretNames(); len(names) != 1 || names[0] != "dmr.password" {
		t.Errorf("the positive statement of what came back is %v", names)
	}
}

// TestTheCallersSecretMapIsNotShared, so editing it afterwards cannot change
// what a backup already written describes.
func TestTheCallersSecretMapIsNotShared(t *testing.T) {
	secrets := map[string]string{"dmr.password": "original"}
	f := NewFullBackup(Default(), secrets, "0.1.224", time.Now())

	secrets["dmr.password"] = "changed"
	secrets["added.later"] = "value"

	if f.Secrets["dmr.password"] != "original" {
		t.Error("the backup shares the caller's map; editing it changed what " +
			"the backup holds")
	}
	if _, added := f.Secrets["added.later"]; added {
		t.Error("a secret added to the caller's map after the fact appeared in " +
			"the backup")
	}
}
