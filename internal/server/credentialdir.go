package server

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Where QSP reads and writes password files.
//
// **Inside the directory that holds its configuration file, and nowhere
// else.** A password file's path is a setting, and settings are saved from the
// console and carried in backups. Until 0.1.334 the path was believed
// wherever it pointed, which made three things possible for anybody signed
// in, or anybody who could get a backup file restored (found 2026-10-07, C1
// to C3 and C5):
//
//   - a full restore wrote the backup's "password files" to any path the
//     service could write, because the list of paths it was allowed to write
//     came from the configuration inside the same backup;
//   - a password-file setting pointed at any other file, followed by a full
//     backup, carried that file's first 64 KiB away;
//   - removing a link deleted whatever file its passphrase setting named.
//
// The directory is fixed by how the process was started (-config), which
// nothing in the console can change. Both ways QSP is installed keep the
// configuration and every password file together in /var/lib/qsp, so for
// them nothing changes. A server whose password files were put somewhere else
// by hand is told which file and which directory, and nothing is done to
// that file until it is moved.

// errOutsideCredentialDir marks a path refused for being outside it.
var errOutsideCredentialDir = errors.New("outside the directory that holds this server's configuration")

// credentialPath returns path made absolute, if it is inside this server's
// directory, and an error naming both if it is not.
//
// **Links are followed before it is judged.** A path that is inside the
// directory by its spelling and outside it by a symbolic link is outside it:
// the directory part is resolved as far as it exists, and a final component
// that is itself a link is refused, since writing through one writes
// wherever it points.
func (s *Server) credentialPath(path string) (string, error) {
	root := s.opts.CredentialDir
	if root == "" {
		return "", fmt.Errorf("%q: %w: this server was started without a configuration file, "+
			"so it has no directory for password files", path, errOutsideCredentialDir)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("an empty path: %w", errOutsideCredentialDir)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("%q: %w", path, err)
	}
	resolved := resolveExisting(abs)
	if info, err := os.Lstat(resolved); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("%q is a symbolic link: %w (%s)", path, errOutsideCredentialDir, root)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%q is %w, %s; QSP reads and writes password files only inside it",
			path, errOutsideCredentialDir, root)
	}
	return resolved, nil
}

// resolveExisting follows symbolic links in as much of path as exists, and
// keeps the rest as written: a file about to be created has no link to
// follow, and its directory may.
func resolveExisting(path string) string {
	rest := ""
	for dir := path; ; {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			// The last component is judged by the caller, so a link there is
			// reported and not followed.
			if rest == "" {
				if info, lerr := os.Lstat(dir); lerr == nil && info.Mode()&fs.ModeSymlink != 0 {
					return filepath.Join(resolveExisting(filepath.Dir(dir)), filepath.Base(dir))
				}
			}
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return path
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

// CredentialDirFor is the directory a server started with this configuration
// file keeps its password files in: the one the file is in, with links
// followed, so that it compares equal to what credentialPath resolves.
func CredentialDirFor(configPath string) string {
	if strings.TrimSpace(configPath) == "" {
		return ""
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return ""
	}
	dir := filepath.Dir(abs)
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return dir
}
