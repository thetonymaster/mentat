package engine

import (
	"fmt"

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
// package-global maps WITHOUT a mutex. This is intentional and safe because
// Build runs exactly once at composition-root startup, before the godog suite
// executes scenarios concurrently. All concurrent readers (Engine.Drive,
// Engine.Comparator) only ever read those maps after Build returns.
func Build(cfg config.Config, st core.TraceStore, cor core.Correlator) (*Engine, error) {
	registry.RegisterDriver("shell", driver.NewShell())
	registry.RegisterDriver("http", driver.NewHTTP())
	pricing := toPricing(cfg.Pricing)
	registry.RegisterComparator("sequence", comparator.NewSequence())
	registry.RegisterComparator("budgets", comparator.NewBudgets(pricing))
	registry.RegisterComparator("result", comparator.NewResult())
	registry.RegisterComparator("cel", comparator.NewCEL(pricing))
	registry.RegisterComparator("shape", comparator.NewShape())
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

	sems := map[string]chan struct{}{}
	for name, t := range cfg.Targets {
		n := t.MaxConcurrency
		if n < 1 {
			n = 1
		}
		sems[name] = make(chan struct{}, n)
	}
	return &Engine{cfg: cfg, cor: cor, st: st, sems: sems, pricing: pricing, patterns: pats}, nil
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
