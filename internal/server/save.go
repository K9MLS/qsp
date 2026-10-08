package server

import (
	"context"
	"errors"

	"github.com/k9mls/qsp/internal/config"
)

// save writes a configuration, and answers one question the same way for
// every page: was it saved.
//
// **Saved and not applied is saved.** The configuration manager reports a
// configuration that reached the file and could not be handed to the running
// listeners as an error, config.ErrSavedNotApplied, because it is one. Twelve
// handlers save a configuration and eleven of them read any error as "nothing
// was saved": they answered 400, recorded a failure in the audit trail, and
// two of them then deleted the password file the saved configuration named,
// leaving a link that a restart would bring up with no password (found
// 2026-10-07, G10). One handler had it right, and this is that handler's
// rule in the one place all twelve now go through.
//
// late is why the save is not in force yet, and empty when it is. It belongs
// wherever the answer says what needs a restart: see notApplied.
func (s *Server) save(ctx context.Context, cfg config.Config, author, summary string) (version config.Version, late string, err error) {
	version, err = s.opts.Config.Save(ctx, cfg, author, summary)
	if errors.Is(err, config.ErrSavedNotApplied) {
		s.log.Warn("configuration saved but not applied to the running instance",
			"author", author, "version", version.Number, "error", err)
		return version, unwrapApply(err), nil
	}
	return version, "", err
}

// notApplied adds, to the settings a save needs a restart for, the save
// itself when it could not be applied.
func notApplied(needsRestart []string, late string) []string {
	if late == "" {
		return needsRestart
	}
	return append(needsRestart, "everything in this save: it could not be applied "+
		"while running ("+late+")")
}

// editing holds every other save off until the function it returns is called,
// which is when the handler that took it returns.
//
// **A save is a read, a change and a write, and nothing held the three
// together.** Two saves landing within milliseconds each read the same
// configuration, each changed its own part, and the second written undid the
// first: the merge that protects a page from a stale document cannot protect
// it from a current one that goes stale while it is being merged (found
// 2026-10-07, H5). Every handler that saves takes this before it reads.
//
// Saves are rare and short, so one lock for all of them costs nothing an
// operator could notice; two locks would be two orders to get wrong.
func (s *Server) editing() func() {
	s.configMu.Lock()
	return s.configMu.Unlock
}
