package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/k9mls/qsp/internal/config"
)

// First-run configuration.
//
// # What this is for
//
// Until now the only way to run QSP was to build it from source and write a
// configuration file by hand. That is a reasonable bar for the three people on
// the author's network and an unreasonable one for the community this project
// exists to serve. See [ADR-0048](../../docs/adr/ADR-0048-container-install.md).
//
// # What a first run actually needs, which is not what was assumed
//
// The plan said the one thing to ask for was the operator's radio ID. **That
// was wrong, and the config package said so the moment it was asked.** A
// Homebrew master has no radio ID of its own — peers bring theirs — and
// MasterID belongs to IP Site Connect, which starts disabled. Default()
// validates with no input at all.
//
// What the validator does refuse is a listener with no password and no access
// policy, and it names both:
//
//   - **a peer password**, the shared secret hotspots authenticate with. It
//     lives in a file rather than in the configuration, because configuration
//     gets pasted into forum posts.
//   - **an access block**, because a listener on 0.0.0.0 with no policy means
//     every repeater may register, every subscriber may transmit and every
//     talkgroup is carried.
//
// Both are things an operator genuinely has to decide, and both already fail
// loudly with a message saying what to do. That is a better first run than a
// radio ID would have been: it ends with a server that has a password and a
// stated policy rather than one that starts and is open.
//
// # Why it lives here rather than in an entrypoint script
//
// A shell script in the container image would mean abandoning scratch for a
// base image with a shell, and would put this logic where the test suite
// cannot reach it. Here it is tested like everything else, the image stays one
// static binary, and **the systemd install gets the same behaviour for free**.

// Environment variables a first run reads.
//
// Variables rather than flags because the container reads them from a .env
// file, which is one line to edit and the shape every Docker user already
// knows. A systemd unit sets the same names with Environment=.
const (
	// peerPasswordEnv is the shared secret hotspots authenticate with.
	peerPasswordEnv = "QSP_PEER_PASSWORD"
	// allowedPeersEnv is the comma-separated list of repeater IDs permitted to
	// register.
	//
	// **Required, and that was not the plan.** The intention was to write an
	// empty permit list so a fresh instance carried nothing until its operator
	// filled it in. The validator refuses that: a permit list with no entries
	// refuses every station, and it treats writing one as a mistake rather
	// than a policy — which it usually is.
	//
	// Being made to ask is the better outcome. An operator states who may
	// connect before anything is listening, rather than starting a server that
	// is silently useless and finding out when a hotspot will not register.
	allowedPeersEnv = "QSP_ALLOWED_PEERS"
)

// errNoPeerPassword is returned when there is no configuration and nothing
// says what password to write into it.
//
// **A server that stops and explains itself is kinder than one that starts and
// is wrong.** Generating a password nobody knows would produce a running
// instance that refuses every hotspot, with the cause invisible from the
// console.
var errNoPeerPassword = errors.New("no peer password")

// errNoAllowedPeers is returned when nothing says which repeaters may register.
//
// The alternative to asking is an open master, and an open master reachable
// from the internet is a problem for the people it relays to as much as for
// whoever installed it.
var errNoAllowedPeers = errors.New("no allowed peers")

// bootstrapConfig writes a starting configuration and password file when none
// exists, reporting whether it did.
//
// **It never touches a file that already exists.** After the first run the
// configuration is the operator's, including any mistake in it, and a program
// that rewrites what somebody edited is a program nobody can configure.
func bootstrapConfig(path string, env func(string) string) (written bool, err error) {
	if path == "" {
		return false, nil
	}
	switch _, err := os.Stat(path); {
	case err == nil:
		return false, nil
	case !errors.Is(err, os.ErrNotExist):
		return false, fmt.Errorf("cannot examine %q: %w", path, err)
	}

	password := strings.TrimSpace(env(peerPasswordEnv))
	if password == "" {
		return false, errNoPeerPassword
	}
	allowed := splitIDs(env(allowedPeersEnv))
	if len(allowed) == 0 {
		return false, errNoAllowedPeers
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("cannot create %q: %w", dir, err)
	}

	passwordPath := filepath.Join(dir, "peer-password")
	if err := writePrivate(passwordPath, []byte(password+"\n")); err != nil {
		return false, err
	}

	cfg := starterConfig(passwordPath, allowed)
	if err := cfg.Validate(); err != nil {
		// A starting configuration that does not validate is a defect here,
		// not in anything the operator did, and it must not reach them as a
		// puzzling complaint about their own setup.
		return false, fmt.Errorf("the built-in starting configuration is not valid "+
			"(this is a bug in QSP rather than anything you did): %w", err)
	}

	var buf strings.Builder
	if err := config.Save(&buf, cfg); err != nil {
		return false, err
	}
	if err := writePrivate(path, []byte(buf.String())); err != nil {
		return false, err
	}
	return true, nil
}

// writePrivate writes a file only its owner can read, through a temporary file
// so that a crash or a full disk leaves nothing half-written for the next start
// to choke on.
func writePrivate(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".qsp-*")
	if err != nil {
		return fmt.Errorf("cannot write %q: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// 0600: this is either a password or a file that will come to hold one.
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// splitIDs turns a comma-separated list into ACL entries, dropping empty ones
// so a trailing comma is not an error somebody has to hunt for.
func splitIDs(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// explainFirstRun writes what somebody sees when there is no configuration and
// nothing to write one with.
//
// **It is the only documentation some operators will ever read**, so it names
// the file to edit and the value to put in it, rather than reporting that a
// required setting is missing and leaving them to work out which.
func explainFirstRun(w io.Writer, path string) {
	fmt.Fprintf(w, `QSP has no configuration at %s and needs two things to write one.

A peer password. The shared secret your hotspots use to log in — you
choose it, and you put the same one into Pi-Star or WPSD. There is no
default, because a master whose password everybody knows has no password.

The repeater IDs allowed to register. QSP will not start an open master
for you: a listener reachable from the internet that accepts anybody is a
problem for the people it relays to as much as for you. These are the DMR
IDs of your own hotspots and repeaters, from https://radioid.net.

  Docker    put both in the .env file beside docker-compose.yml

              %s=choose-something-long
              %s=3132910,3155413

            then: docker compose up -d

  systemd   in the unit file:

              Environment=%[2]s=choose-something-long
              Environment=%[3]s=3132910,3155413

            then: systemctl restart qsp

Everything else is configured for you: the Homebrew listener on 62031, the
console on 8080, all talkgroups carried, and IP Site Connect off until you
have a Motorola repeater to point at it.

QSP writes the configuration on its first run. After that it is yours, and
this will never overwrite it.
`, path, peerPasswordEnv, allowedPeersEnv)
}

// starterConfig is what a first run writes.
//
// # What is on, and what is not
//
// **Homebrew on, IP Site Connect off.** Most people arriving have a hotspot
// rather than a Motorola repeater, and IPSC needs a negotiated relationship at
// both ends — an idle listener on port 50000 is attack surface with no benefit
// to somebody who has just installed this. An operator with a repeater turns it
// on, and by then they are reading the file anyway.
//
// **The console binds to every interface.** Under the container's host
// networking 127.0.0.1 would mean a newcomer sees nothing until they build an
// SSH tunnel, which is a wall on minute one. The console authenticates, and
// /api/peers withholds peer addresses and drop reasons from callers who are not
// signed in, so a public view names stations without naming their home
// connections.
//
// **The access policy permits only what it is told to.** A permit list refuses
// anything it does not name, so an empty one carries nothing until its operator
// says what it may carry.
func starterConfig(passwordPath string, allowed []string) config.Config {
	cfg := config.Default()
	cfg.DMR.Enabled = true
	cfg.DMR.ListenAddress = "0.0.0.0:62031"
	cfg.DMR.PasswordFile = passwordPath
	cfg.DMR.Access = &config.Access{
		Registration: config.ACL{Mode: "permit", IDs: allowed},
		Subscribers:  config.ACL{Mode: "permit", IDs: allowed},
		// **Talkgroups carry everything; registration is the boundary.**
		// Which talkgroups an instance carries is a policy refinement an
		// operator makes once they know what their members use. Who may
		// connect at all is the safety question, and it is answered above.
		// A deny list with no entries is the documented way to say "all".
		Talkgroups: config.Talkgroups{
			Timeslot1: config.ACL{Mode: "deny", IDs: []string{}},
			Timeslot2: config.ACL{Mode: "deny", IDs: []string{}},
		},
	}
	cfg.IPSC.Enabled = false
	cfg.Server.ListenAddress = "0.0.0.0:8080"
	return cfg
}
