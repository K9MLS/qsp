package config

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// VersionStore keeps the configuration history.
//
// An interface for the same reason auth.Repository is one: what is worth
// testing — the order things are recorded in, and what a rollback means — is
// testable without a database, and the SQL is three statements.
//
// Rows are never updated or deleted. A rollback appends a new version whose
// content matches an older one, which is what lets the history answer "what was
// running on the night of the net" long afterwards.
type VersionStore interface {
	// Append records a version and returns it with its assigned number.
	Append(ctx context.Context, v Version) (Version, error)
	// List returns versions newest first, at most limit of them.
	List(ctx context.Context, limit int) ([]Version, error)
	// Get returns one version by number. It reports false, and no error, when
	// there is no such version.
	Get(ctx context.Context, number int64) (Version, bool, error)
	// Latest returns the most recent version, if there is one.
	Latest(ctx context.Context) (Version, bool, error)
}

// ErrNotWritable means the configuration file cannot be saved to.
var ErrNotWritable = errors.New("config: the configuration file is not writable")

// ErrSavedNotApplied marks a save that was recorded and written to the file
// and could not then be applied to the running instance. It is a save: the
// version exists and the next restart uses it.
var ErrSavedNotApplied = errors.New("config: saved, but not applied to the running instance")

// Writer saves a configuration to the file it was loaded from.
//
// **The file is the source of truth**, per ADR-0027. The version history says
// what changed and when; this is what a restart reads.
type Writer struct {
	path string
}

// NewWriter prepares to save to a path.
//
// An empty path means the instance was started with no configuration file and
// is running on defaults. Saving would then have to invent a location, and an
// operator who never chose one would find a file appear somewhere they did not
// expect — so it is refused instead, and the refusal says why.
func NewWriter(path string) (*Writer, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: this instance was started without -config, so there is "+
			"no file to save to; start it with a configuration file to change settings "+
			"from the console", ErrNotWritable)
	}
	return &Writer{path: path}, nil
}

// Path returns the file being written.
func (w *Writer) Path() string { return w.path }

// Writable reports whether a save could succeed, without attempting one.
//
// The console asks this so it can say up front that it is read-only, rather
// than letting somebody fill in a form and fail at the moment they press save.
// A club running QSP from a read-only file or a container image is a reasonable
// posture, and finding out early is the difference between a posture and a
// fault.
func (w *Writer) Writable() error {
	dir := filepath.Dir(w.path)

	// The directory matters more than the file: the write is a temporary file
	// and a rename, so a writable file in a read-only directory still cannot
	// be saved.
	probe, err := os.CreateTemp(dir, ".qsp-writable-*")
	if err != nil {
		return fmt.Errorf("%w: %s is not writable by this process (%v); "+
			"the configuration is saved by replacing the file, which needs write "+
			"permission on the directory as well", ErrNotWritable, dir, err)
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)

	// **The file's own permissions are deliberately not checked.** Saving
	// replaces it by renaming a temporary file over it, and rename(2) needs
	// write permission on the directory rather than on the file — a 0444 file
	// owned by somebody else is replaced without complaint if the directory
	// allows it.
	//
	// An earlier version checked the mode bits here and would have reported a
	// perfectly writable instance as read-only. It was also wrong about its
	// own justification: an immutable attribute does not appear in the
	// permission bits, so the check did not catch what it claimed to.
	return nil
}

// Save writes a configuration atomically.
//
// **A temporary file in the same directory, then a rename.** A QSP that
// restarts while half a configuration is on disk starts with nothing valid, and
// the operator's next move is guessing. The temporary file must share a
// directory with the target or the rename crosses a filesystem and stops being
// atomic.
func (w *Writer) Save(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		// Refusing to write something that cannot be loaded again would
		// otherwise strand the operator on the next restart. Save already
		// checks this; doing it here as well means a caller that assembled a
		// Config by hand cannot bypass it.
		return err
	}

	dir := filepath.Dir(w.path)
	tmp, err := os.CreateTemp(dir, ".qsp-config-*")
	if err != nil {
		return fmt.Errorf("%w: cannot create a temporary file in %s: %v", ErrNotWritable, dir, err)
	}
	tmpName := tmp.Name()
	// Removed on every failure path. A directory slowly filling with
	// half-written configurations is its own problem.
	defer func() { _ = os.Remove(tmpName) }()

	if err := Save(tmp, cfg); err != nil {
		_ = tmp.Close()
		return err
	}
	// Flushed before the rename, or a crash between the two leaves a file that
	// exists and is empty — which is worse than one that does not exist.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cannot flush the configuration to disk: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cannot close the temporary configuration: %w", err)
	}

	// The mode of the file being replaced is kept. A configuration that was
	// 0600 must not become world-readable because it was saved.
	mode := fs.FileMode(0o600)
	if info, err := os.Stat(w.path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("cannot set permissions on the new configuration: %w", err)
	}

	if err := os.Rename(tmpName, w.path); err != nil {
		return fmt.Errorf("%w: cannot replace %s: %v", ErrNotWritable, w.path, err)
	}
	return nil
}
