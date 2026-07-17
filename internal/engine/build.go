package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/driver"
	"github.com/thetonymaster/mentat/internal/expectations"
	"github.com/thetonymaster/mentat/internal/judge"
	"github.com/thetonymaster/mentat/internal/registry"
	"github.com/thetonymaster/mentat/internal/report"
)

// Build is the single composition root: it registers built-in drivers and
// comparators into the registry, then wires and returns a ready *Engine.
//
// Concurrency note: the Register* calls below write into the registry's
// package-global maps under the registry's own RWMutex (FR-009). Build reopens
// the registry, registers every seam, then seals it — after which any stray
// Register* outside a Build panics loudly. Concurrent readers (Engine.Drive,
// Engine.Comparator) take the read lock, so the maps are safe to read while the
// godog suite executes scenarios concurrently.
func Build(cfg config.Config, st core.TraceStore, cor core.Correlator, opts ...Option) (*Engine, error) {
	// Resolve the injected logger (silent discard-handler default) and hand it to
	// the drivers Build constructs. The correlator is a parameter Build never
	// invokes (nil in tests), so its logger is injected upstream via
	// correlate.WithLogger, not here.
	o := resolveOptions(opts)

	// Build is the composition root and is re-entrant: reopen the registry so a
	// rebuild (tests build many engines) is not blocked by a previous seal, then
	// seal again once wiring completes (FR-009). A stray Register* after this seal —
	// outside a Build — panics loudly.
	registry.Reopen()
	registry.RegisterDriver("shell", driver.NewShell(driver.WithLogger(o.logger)))
	registry.RegisterDriver("http", driver.NewHTTP(driver.WithLogger(o.logger)))
	pricing := toPricing(cfg.Pricing)
	registry.RegisterComparator("sequence", comparator.NewSequence())
	registry.RegisterComparator("budgets", comparator.NewBudgets(pricing))
	registry.RegisterComparator("result", comparator.NewResult())
	registry.RegisterComparator("cel", comparator.NewCEL(pricing))
	registry.RegisterComparator("shape", comparator.NewShape())
	registry.RegisterComparator("retries", comparator.NewRetries())
	registry.RegisterAggregateComparator("aggregate-cel", comparator.NewAggregateCEL(pricing))
	comparator.RegisterBuiltinMatchers()
	report.RegisterBuiltins()

	// Wire the LLM-judge seam and the "semantic" result matcher. The judge
	// backend defaults to "claude" when unset (the documented default, resolved
	// here) so zero-value/struct-literal cfgs keep building; only a non-empty,
	// unregistered backend is the FR-005 hard error. Build is an ingestion
	// boundary (callable directly with a raw config.Config, bypassing
	// config.Load's validateJudge), so votes are validated the same way here:
	// unset (0) defaults to 1, but a negative or even value is a loud error
	// rather than a silently-coerced guess (Constitution IV, no silent fallbacks).
	judge.RegisterBuiltins()

	// Funnel custom driver/comparator/judge registrations from the public facade
	// (spec 007 FR-002). They land AFTER the built-ins and BEFORE judge resolution,
	// adapter validation, and Seal — so a custom driver is a first-class adapter
	// validated like any other, and a custom judge is resolvable by name.
	//
	// Each is collision-checked against the registry first: the registry keys by name
	// only, so provenance is tracked to the extent of "a built-in or an earlier
	// registration". Built-ins register first and extras apply in order, so the 2nd of
	// a duplicate pair sees the 1st already present — covering both custom-vs-built-in
	// and custom-vs-custom. A collision is a loud, seam-and-name error, never a silent
	// last-wins overwrite (Constitution IV).
	if err := applyExtras(o); err != nil {
		return nil, err
	}

	backend := cfg.Judge.Backend
	if backend == "" {
		backend = "claude"
	}
	jf, ok := registry.Judge(backend)
	if !ok {
		return nil, fmt.Errorf("unknown judge backend %q", backend)
	}
	j, err := jf(cfg)
	if err != nil {
		return nil, fmt.Errorf("build judge %q: %w", backend, err)
	}
	votes := cfg.Judge.Votes
	if votes == 0 {
		votes = 1
	}
	if votes < 1 || votes%2 == 0 {
		return nil, fmt.Errorf("judge.votes must be a positive odd integer, got %d", votes)
	}
	registry.RegisterMatcher("semantic", comparator.NewSemantic(j, votes))

	pats, err := expectations.Load(cfg.Expectations)
	if err != nil {
		return nil, fmt.Errorf("load expectations from %q: %w", cfg.Expectations, err)
	}

	// Validate every target's adapter against the driver registry — the single
	// runtime source of truth for which adapters exist (feature 005, D3/FR-005).
	// This runs at Build (startup), before any scenario, so a phantom adapter (one
	// with no registered driver) fails loudly here naming the target, the adapter,
	// and the registered set, rather than as a silent no-op at drive time. The
	// load-time concurrency allowlist is deliberately NOT a second, driftable truth.
	// Targets is a map, so validate in sorted name order — when more than one target
	// has a phantom adapter the surfaced error is deterministic run-to-run (SC-005).
	names := make([]string, 0, len(cfg.Targets))
	for name := range cfg.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := registry.Driver(cfg.Targets[name].Adapter); !ok {
			return nil, fmt.Errorf("engine: target %q: adapter %q has no registered driver (registered: %s)", name, cfg.Targets[name].Adapter, strings.Join(registry.Drivers(), ", "))
		}
	}

	sems := map[string]chan struct{}{}
	for name, t := range cfg.Targets {
		n := t.MaxConcurrency
		if n < 1 {
			n = 1
		}
		sems[name] = make(chan struct{}, n)
	}
	registry.Seal() // wiring complete: post-build registration now fails loudly (FR-009)
	return &Engine{cfg: cfg, cor: cor, st: st, sems: sems, pricing: pricing, patterns: pats, logger: o.logger}, nil
}

// applyExtras registers the facade-funneled driver/comparator/judge seams into the
// (open) registry, each guarded by a collision check that fails loudly naming the
// seam and the conflicting name (FR-002). It runs inside Build, between built-in
// registration and Seal, so post-seal registration stays unrepresentable.
func applyExtras(o options) error {
	for _, ed := range o.extraDrivers {
		if _, exists := registry.Driver(ed.name); exists {
			return fmt.Errorf("engine: WithDriver: adapter %q is already registered (a built-in or an earlier registration); adapter names must be unique", ed.name)
		}
		registry.RegisterDriver(ed.name, ed.driver)
	}
	for _, ec := range o.extraComparators {
		if _, exists := registry.Comparator(ec.name); exists {
			return fmt.Errorf("engine: WithComparator: comparator %q is already registered (a built-in or an earlier registration); comparator names must be unique", ec.name)
		}
		registry.RegisterComparator(ec.name, ec.comparator)
	}
	for _, ej := range o.extraJudges {
		if _, exists := registry.Judge(ej.name); exists {
			return fmt.Errorf("engine: WithJudge: judge %q is already registered (a built-in or an earlier registration); judge names must be unique", ej.name)
		}
		registry.RegisterJudge(ej.name, ej.factory)
	}
	return nil
}

// toPricing converts the YAML pricing table into the transport-free core.Pricing
// the comparator layer consumes. An empty/absent table maps to nil, which the
// comparators treat as "no derivation" (legacy emitted-cost-only behaviour).
func toPricing(p config.Pricing) core.Pricing {
	if len(p) == 0 {
		return nil
	}
	out := make(core.Pricing, len(p))
	for model, r := range p {
		out[model] = core.ModelRate{InputPerMTok: r.InputPerMTok, OutputPerMTok: r.OutputPerMTok}
	}
	return out
}
