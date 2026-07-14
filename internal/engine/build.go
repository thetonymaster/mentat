package engine

import (
	"fmt"
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
	for name, t := range cfg.Targets {
		if _, ok := registry.Driver(t.Adapter); !ok {
			return nil, fmt.Errorf("engine: target %q: adapter %q has no registered driver (registered: %s)", name, t.Adapter, strings.Join(registry.Drivers(), ", "))
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
