package engine

import (
	"fmt"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
	"github.com/thetonymaster/mentat/internal/store"
)

// BuildStore is the store composition root: it registers the built-in store
// factories, then resolves the store named by cfg.Store. Unknown names are a
// hard error (no silent fallback). Engine.Build keeps taking a built TraceStore
// so hermetic tests can inject store.NewInMemStore directly.
func BuildStore(cfg config.Config) (core.TraceStore, error) {
	// Re-entrant like Build: reopen to register the store factories, seal once the
	// store is resolved so post-build store registration fails loudly (FR-009).
	registry.Reopen()
	registry.RegisterStore("tempo", func(c config.Config) (core.TraceStore, error) {
		return store.NewTempo(c.Tempo.Endpoint, nil, c.Poll.SearchLimit), nil
	})
	// The "file" store replays captured fixtures from c.StorePath (US5, invariant §3):
	// the second built-in TraceStore, wired here at the single composition root. A bad
	// storePath surfaces as a build error, never a silent empty store.
	registry.RegisterStore("file", func(c config.Config) (core.TraceStore, error) {
		fs, err := store.NewFileStore(c.StorePath)
		if err != nil {
			return nil, err
		}
		return fs, nil
	})
	f, ok := registry.Store(cfg.Store)
	if !ok {
		return nil, fmt.Errorf("unknown store %q", cfg.Store)
	}
	st, err := f(cfg)
	if err != nil {
		return nil, fmt.Errorf("building store %q: %w", cfg.Store, err)
	}
	registry.Seal()
	return st, nil
}
