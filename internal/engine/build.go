package engine

import (
	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/driver"
	"github.com/thetonymaster/mentat/internal/registry"
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
	registry.RegisterAggregateComparator("aggregate-cel", comparator.NewAggregateCEL(pricing))
	comparator.RegisterBuiltinMatchers()

	sems := map[string]chan struct{}{}
	for name, t := range cfg.Targets {
		n := t.MaxConcurrency
		if n < 1 {
			n = 1
		}
		sems[name] = make(chan struct{}, n)
	}
	return &Engine{cfg: cfg, cor: cor, st: st, sems: sems, pricing: pricing}, nil
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
