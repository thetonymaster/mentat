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
func BuildStore(cfg config.Config, opts ...Option) (core.TraceStore, error) {
	o := resolveOptions(opts)
	// Owns a fresh registry per call (spec 007 US2, T010/T011): two BuildStores never
	// share store-factory state, so a custom store name reused across Runs cannot leak
	// a false collision (sequential) or race (concurrent). Register the store
	// factories, seal once the store is resolved so post-build store registration
	// fails loudly (FR-009). The registry is local — discarded once st is built.
	reg := registry.New()
	reg.RegisterStore("tempo", func(c config.Config) (core.TraceStore, error) {
		return store.NewTempo(c.Tempo.Endpoint, nil, c.Poll.SearchLimit), nil
	})
	// The "file" store replays captured fixtures from c.StorePath (US5, invariant §3):
	// the second built-in TraceStore, wired here at the single composition root. A bad
	// storePath surfaces as a build error, never a silent empty store.
	reg.RegisterStore("file", func(c config.Config) (core.TraceStore, error) {
		fs, err := store.NewFileStore(c.StorePath)
		if err != nil {
			return nil, err
		}
		return fs, nil
	})

	// Funnel custom store factories registered through the public facade (spec 007
	// FR-002), after the built-ins and before cfg.Store resolution and Seal. Each is
	// collision-checked so a name clashing with a built-in (tempo/file) or an earlier
	// registration is a loud, seam-and-name error, never a silent last-wins overwrite.
	for _, es := range o.extraStores {
		if es.factory == nil {
			return nil, fmt.Errorf("engine: WithStore: store %q has a nil factory; a registered store factory must be non-nil", es.name)
		}
		if _, exists := reg.Store(es.name); exists {
			return nil, fmt.Errorf("engine: WithStore: store %q is already registered (a built-in or an earlier registration); store names must be unique", es.name)
		}
		reg.RegisterStore(es.name, es.factory)
	}

	f, ok := reg.Store(cfg.Store)
	if !ok {
		return nil, fmt.Errorf("unknown store %q", cfg.Store)
	}
	st, err := f(cfg)
	if err != nil {
		return nil, fmt.Errorf("building store %q: %w", cfg.Store, err)
	}
	if isNilSeam(st) {
		return nil, fmt.Errorf("building store %q: factory returned a nil store with no error", cfg.Store)
	}
	reg.Seal()
	return st, nil
}
