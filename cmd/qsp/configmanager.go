package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/k9mls/qsp/internal/config"
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
	apply func(cfg config.Config) error

	// mu guards current, and serialises saves. Two administrators pressing
	// save at the same instant would otherwise interleave a read of the
	// running configuration with the other's write, and the diff each was
	// shown would describe a state neither of them saw.
	mu      sync.Mutex
	current config.Config
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
	if m.apply != nil {
		if err := m.apply(cfg); err != nil {
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
func applyToListener(listener *peers.Listener, author, summary string) func(config.Config) error {
	return func(cfg config.Config) error {
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
