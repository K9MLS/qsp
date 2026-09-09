package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/k9mls/qsp/internal/config"
	"github.com/k9mls/qsp/internal/ipsclink"
	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/routing"
)

// configManager holds the running configuration and saves a new one.
//
// The order is validate, record, write, apply — ADR-0027. **Recording before
// writing is deliberate**: a version row describing a configuration that failed
// to reach the disk is a puzzle an operator can solve, while a configuration on
// disk that no version records is a change nobody can attribute or undo.
type configManager struct {
	writer *config.Writer
	store  config.VersionStore
	// apply hands the change to the goroutine that owns the routing core. Nil
	// on an instance with no listener, which is a working state.
	apply func(cfg config.Config, author, summary string) error
	// applyServer updates the settings the HTTP server can change under
	// itself. Separate from apply because the two have different owners: one
	// is the listener's goroutine, the other is every handler at once.
	applyServer func(cfg config.Config)

	// mu guards current, and serialises saves. Two administrators pressing
	// save at the same instant would otherwise interleave a read of the
	// running configuration with the other's write, and the diff each was
	// shown would describe a state neither of them saw.
	mu      sync.Mutex
	current config.Config
	// startup is the configuration this process was built from.
	//
	// **What "the running server" means, precisely.** Settings that take
	// effect live are applied to the listener as they are saved; settings that
	// do not need a restart, and a restart rebuilds from `current`. So the
	// difference between this and `current` is exactly the set of things the
	// running process is not yet doing — which is what an administrator is
	// asking when they ask whether the server matches its configuration.
	startup config.Config
}

// PendingRestart implements server.ConfigManager.
//
// **Compared against what the process started with, not against the previous
// save.** A server three changes behind its configuration has to say so once,
// rather than once per change that somebody happened to notice — which is how
// it was reported before ADR-0055: a sentence beside whichever link was saved
// last, and nothing anywhere that added them up.
func (m *configManager) PendingRestart() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	return config.NeedsRestart(m.startup, m.current)
}

// Current implements server.ConfigManager.
func (m *configManager) Current() config.Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

// Writable implements server.ConfigManager.
func (m *configManager) Writable() error {
	if m.writer == nil {
		return fmt.Errorf("%w: this instance was started without -config", config.ErrNotWritable)
	}
	return m.writer.Writable()
}

// Save implements server.ConfigManager.
func (m *configManager) Save(ctx context.Context, cfg config.Config, author, summary string) (config.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.writer == nil {
		return config.Version{}, fmt.Errorf("%w: this instance was started without -config",
			config.ErrNotWritable)
	}
	// Checked before anything is recorded, so the history never holds a
	// configuration that could not be restored.
	if err := cfg.Validate(); err != nil {
		return config.Version{}, err
	}

	version, err := config.NewVersion(0, author, summary, cfg, time.Now())
	if err != nil {
		return config.Version{}, err
	}
	if m.store != nil {
		version, err = m.store.Append(ctx, version)
		if err != nil {
			return config.Version{}, err
		}
	}

	if err := m.writer.Save(cfg); err != nil {
		// The version row survives. It describes a configuration this instance
		// tried and failed to persist, which is exactly what an operator
		// needs to see when the file turns out to be read-only.
		return config.Version{}, err
	}

	m.current = cfg
	// The server first: it holds settings that need no coordination, and a
	// failure to reach the listener should not leave the join page stale as
	// well as the routing table.
	if m.applyServer != nil {
		m.applyServer(cfg)
	}
	if m.apply != nil {
		if err := m.apply(cfg, author, summary); err != nil {
			// Written and not applied. Reporting success would leave the
			// operator believing a change is live when the next restart is
			// what will make it so.
			return version, fmt.Errorf("the configuration was saved but could not be applied "+
				"to the running instance; it will take effect on restart: %w", err)
		}
	}
	return version, nil
}

// Versions implements server.ConfigManager.
func (m *configManager) Versions(ctx context.Context, limit int) ([]config.Version, error) {
	if m.store == nil {
		// No database is a working state, not a failure: an instance without
		// one still runs, and an empty history says so more usefully than an
		// error about storage.
		return nil, nil
	}
	return m.store.List(ctx, limit)
}

// Version implements server.ConfigManager.
func (m *configManager) Version(ctx context.Context, number int64) (config.Version, bool, error) {
	if m.store == nil {
		return config.Version{}, false, nil
	}
	return m.store.Get(ctx, number)
}

// applyToListener builds the runtime pieces a configuration implies and queues
// them for the listener's own goroutine.
//
// **The IPSC listener is applied here too**, directly rather than through the
// Reload queue. peers.Reload exists because routing.Core is single-writer and
// owned by the socket goroutine; the IPSC allow list is behind an atomic
// pointer and has no such owner, so routing it through another listener's queue
// would add a hop and a dependency to buy nothing.
func applyToListener(listener *peers.Listener, ipsc *ipsclink.Listener) func(config.Config, string, string) error {
	return func(cfg config.Config, author, summary string) error {
		// Applied before the rest: a repeater removed from the list should
		// stop being answered as promptly as the save reports success, and
		// nothing below can fail in a way that should leave it answered.
		if ipsc != nil {
			ipsc.SetAllowedPeers(cfg.IPSC.AllowedPeers)
			ipsc.SetPeerNames(cfg.IPSC.PeerNames)
		}
		sched, err := buildSchedule(cfg)
		if err != nil {
			return err
		}
		triggers, err := buildTriggers(cfg)
		if err != nil {
			return err
		}
		table, err := buildTable(cfg, sched, triggers, time.Now())
		if err != nil {
			return err
		}
		lists, err := cfg.AccessLists()
		if err != nil {
			return err
		}

		listener.Apply(&peers.Reload{
			Table:  table,
			Access: lists,
			// The same two functions build() passes at startup, closed over
			// the new configuration. Building them here rather than reusing
			// the originals is the point: the originals hold the old bridges.
			Triggers:      triggers,
			ScheduleState: bridgeState(sched, triggers),
			Rebuild: func(now time.Time) (*routing.Table, error) {
				return buildTable(cfg, sched, triggers, now)
			},
			Subscription: subscriptionFrom(cfg),
			Author:       author,
			Summary:      summary,
		})
		return nil
	}
}

// ensureIdentifier gives a server an identifier if it has none, and saves it.
//
// # Why this runs at startup rather than only at first run
//
// The bootstrap writes a starting configuration only in a container with no
// configuration at all. Every server that already exists — including the two
// this project runs — has one, so an identifier added to the defaults would
// never reach them. ADR-0053 says a server has an identifier; a server that
// predates the field still has to get one.
//
// **It is written once and never rewritten.** A malformed one is refused loudly
// rather than replaced: replacing it would silently make the server a stranger
// to every neighbour that already knows it, which is the failure this exists to
// prevent.
func ensureIdentifier(ctx context.Context, store *configManager, log *slog.Logger) error {
	cfg := store.Current()

	if id := strings.TrimSpace(cfg.Server.Identifier); id != "" {
		if err := config.ValidIdentifier(id); err != nil {
			return fmt.Errorf("server.identifier is not usable, and QSP will not replace one: %w", err)
		}
		return nil
	}

	if err := store.Writable(); err != nil {
		// An instance that cannot write its configuration runs without an
		// identifier rather than refusing to start. A link still works; the
		// far end simply learns nothing about who this is, which is where
		// every QSP was before ADR-0053.
		log.Warn("this server has no identifier and cannot write one",
			slog.String("reason", err.Error()))
		return nil
	}

	id, err := config.NewIdentifier()
	if err != nil {
		return err
	}
	cfg.Server.Identifier = id
	if _, err := store.Save(ctx, cfg, "system", "generated this server's identifier"); err != nil {
		return fmt.Errorf("cannot save this server's identifier: %w", err)
	}
	log.Info("this server has an identifier",
		slog.String("identifier", config.ShortIdentifier(id)),
	)
	return nil
}
