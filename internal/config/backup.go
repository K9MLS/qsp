package config

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Backup and restore (ADR-0054).
//
// # What an export is, and what it deliberately is not
//
// A server's configuration exists in one place: the box it runs on. A failed
// disk takes the links, the access lists, the bridges and the identifier that
// every neighbour calls it by. That is tolerable while every server belongs to
// the operator who built it, and stops being tolerable the moment somebody else
// is running one — the first thing anybody says when helping a stranger is
// *send me your configuration*.
//
// **It carries no secret at all.** ADR-0012 keeps passwords in files beside the
// configuration rather than inside it, precisely so that a document which is
// versioned, diffed, shown in a console and pasted into support requests is
// never the thing holding a credential. An export inherits that: it is safe to
// email, to keep in a repository, and to hand to somebody helping, and it stops
// being safe the moment it holds a password.
//
// **So it names the credentials it cannot carry.** Not silence, and not a
// warning in a manual: the file itself lists every secret the configuration
// refers to, by the thing that needs it. A restore then has a checklist rather
// than a mystery — because a naively restored server comes up with every link
// configured, every link unable to log in, and a page reporting them all as
// configured and not open, **which looks exactly like a network fault and is
// not one.**

// BackupVersion is the format of the export document.
//
// Separate from the QSP version and from the configuration's own schema
// version, because it answers a different question: whether the software
// reading this file understands the shape of the envelope around the
// configuration.
const BackupVersion = 1

// Backup is a server's configuration, ready to be written to a file.
type Backup struct {
	// Format is the envelope version. Refused when newer than this build.
	Format int `json:"format"`
	// QSPVersion is the release that wrote the export, for a human reading it.
	QSPVersion string `json:"qsp_version"`
	// ExportedAt is when it was written, in UTC.
	ExportedAt time.Time `json:"exported_at"`
	// Identifier is the server's identity (ADR-0053), carried so that a
	// restored server is the same server to its neighbours rather than a
	// stranger every link has to be agreed with again.
	Identifier string `json:"identifier,omitempty"`
	// Config is the configuration itself.
	Config Config `json:"config"`
	// Missing lists the credentials this file does not carry, by the thing
	// that needs each one.
	//
	// **Written into the export rather than computed on import**, so that the
	// file answers the question on its own — an operator reading a backup in a
	// mail thread can see what a restore will be missing without running it.
	Missing []MissingCredential `json:"missing_credentials,omitempty"`
}

// MissingCredential names one secret an export cannot carry.
type MissingCredential struct {
	// Kind is what needs it: "link" or "peers".
	Kind string `json:"kind"`
	// Name identifies the thing — a link's name, or the peer password file.
	Name string `json:"name"`
	// Path is where the secret lived on the machine that made the export.
	Path string `json:"path"`
	// Fix is what an operator does about it, in their terms.
	Fix string `json:"fix"`
}

// NewBackup builds an export from a running configuration.
func NewBackup(cfg Config, qspVersion string, now time.Time) Backup {
	return Backup{
		Format:     BackupVersion,
		QSPVersion: qspVersion,
		ExportedAt: now.UTC().Truncate(time.Second),
		Identifier: strings.TrimSpace(cfg.Server.Identifier),
		Config:     cfg,
		Missing:    MissingCredentials(cfg),
	}
}

// MissingCredentials lists every secret a configuration points at.
//
// **Every path the configuration names, not every path that exists.** A file
// the configuration does not mention is not a credential this server uses, and
// listing it would send an operator hunting for something nothing needs.
func MissingCredentials(cfg Config) []MissingCredential {
	var out []MissingCredential

	if p := strings.TrimSpace(cfg.DMR.PasswordFile); p != "" {
		out = append(out, MissingCredential{
			Kind: "peers", Name: "the shared peer password", Path: p,
			Fix: "set a password on this server and give it to every hotspot that " +
				"uses it, or issue each member their own from the Access page",
		})
	}
	if d := strings.TrimSpace(cfg.DMR.PeerPasswords); d != "" {
		out = append(out, MissingCredential{
			Kind: "peers", Name: "per-member passwords", Path: d,
			Fix: "reissue each member's credential from the Access page; a member whose " +
				"password is absent is refused at login",
		})
	}

	for _, u := range cfg.DMR.Upstreams {
		name := strings.TrimSpace(u.Name)
		if p := strings.TrimSpace(u.PasswordFile); p != "" {
			fix := "ask the other operator for a fresh invitation and accept it; the " +
				"offering side reissues the password and the access-list entry together"
			if !u.QSPLink() {
				fix = "ask the other network's administrator for the login password again"
			}
			out = append(out, MissingCredential{
				Kind: "link", Name: name, Path: p, Fix: fix,
			})
		}
		if p := strings.TrimSpace(u.PassphraseFile); p != "" {
			out = append(out, MissingCredential{
				Kind: "link", Name: name, Path: p,
				Fix: "ask the other network's administrator for the passphrase again, or " +
					"agree a fresh peering",
			})
		}
	}

	// Stable order, so two exports of one configuration are the same bytes and
	// a diff between two backups shows what changed rather than what moved.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// WriteBackup writes an export as indented JSON.
func WriteBackup(w io.Writer, b Backup) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(b); err != nil {
		return fmt.Errorf("config: writing a backup: %w", err)
	}
	return nil
}

// Errors an import can report.
var (
	// ErrBackupNewer is an export written by a QSP newer than this one.
	ErrBackupNewer = fmt.Errorf("config: this backup was written by a newer QSP")
	// ErrNotABackup is a file that is not an export at all.
	ErrNotABackup = fmt.Errorf("config: this is not a QSP backup")
)

// ReadBackup reads an export, and refuses one it cannot fully understand.
//
// # Why a newer format is refused outright rather than partly applied
//
// **Importing three-quarters of a configuration is worse than importing none.**
// The quarter that was dropped is invisible: the operator sees a server that
// started, believes it is restored, and discovers months later that a talkgroup
// rule or an access list was silently absent. Refusing names both versions and
// leaves them with a working machine and a clear next step.
func ReadBackup(r io.Reader) (Backup, error) {
	var b Backup
	dec := json.NewDecoder(r)
	// **Unknown fields are refused, unlike on the wire.** A backup is read once
	// by an operator restoring a server, and a field this build does not
	// understand is exactly the case above: something in the file that will not
	// reach the restored machine. The identity packet ignores unknown fields
	// because it is read on every link by software that may be older; this is
	// the opposite situation.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return Backup{}, fmt.Errorf("%w: %v", ErrNotABackup, err)
	}
	if b.Format == 0 {
		return Backup{}, ErrNotABackup
	}
	if b.Format > BackupVersion {
		return Backup{}, fmt.Errorf("%w: the file is format %d and this QSP reads %d; "+
			"upgrade QSP before importing it", ErrBackupNewer, b.Format, BackupVersion)
	}
	if err := b.Config.Validate(); err != nil {
		return Backup{}, err
	}
	return b, nil
}
