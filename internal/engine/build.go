package engine

import (
	"fmt"
	"reflect"
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

// Build is the single composition root: it constructs a fresh per-engine registry,
// registers built-in drivers and comparators into it, then wires and returns a ready
// *Engine that OWNS that registry.
//
// Concurrency note: each Build owns its own registry (spec 007 US2, T010/T011), so two
// Builds — sequential or concurrent — never share seam state: a custom registration in
// one Run cannot leak into or race another. The Register* calls below write into that
// registry's maps under its RWMutex (FR-009); Build registers every seam, then seals
// it, after which any stray Register* panics loudly. Concurrent readers (Engine.Drive,
// Engine.Comparator) take the read lock, so the maps are safe to read while the godog
// suite executes scenarios concurrently.
func Build(cfg config.Config, st core.TraceStore, cor core.Correlator, opts ...Option) (*Engine, error) {
	// Resolve the injected logger (silent discard-handler default) and hand it to
	// the drivers Build constructs. The correlator is a parameter Build never
	// invokes (nil in tests), so its logger is injected upstream via
	// correlate.WithLogger, not here.
	o := resolveOptions(opts)

	// Build is the composition root: it owns a fresh registry per call, so a rebuild
	// (tests build many engines) and concurrent builds are independent. Register every
	// seam, then seal once wiring completes (FR-009) — a stray Register* after the seal
	// panics loudly.
	reg := registry.New()
	reg.RegisterDriver("shell", driver.NewShell(driver.WithLogger(o.logger)))
	reg.RegisterDriver("http", driver.NewHTTP(driver.WithLogger(o.logger)))
	pricing := toPricing(cfg.Pricing)
	reg.RegisterComparator("sequence", comparator.NewSequence())
	reg.RegisterComparator("budgets", comparator.NewBudgets(pricing))
	reg.RegisterComparator("result", comparator.NewResult(reg))
	reg.RegisterComparator("cel", comparator.NewCEL(pricing))
	reg.RegisterComparator("shape", comparator.NewShape())
	reg.RegisterComparator("retries", comparator.NewRetries())
	reg.RegisterAggregateComparator("aggregate-cel", comparator.NewAggregateCEL(pricing))
	comparator.RegisterBuiltinMatchers(reg)
	// Reporters are a post-run rendering concern, but they are per-engine like every
	// other seam (feature 010, D4). The older comment here claimed cmd/mentat emitted
	// them after Run returned, so they could not be per-engine — that call path has not
	// existed since the 007 recompose put emission inside Run (run.go), and the stale
	// justification was the only thing keeping the seam on a package-global map.
	report.RegisterBuiltins(reg)

	// Wire the LLM-judge seam and the "semantic" result matcher. The judge
	// backend defaults to "claude" when unset (the documented default, resolved
	// here) so zero-value/struct-literal cfgs keep building; only a non-empty,
	// unregistered backend is the FR-005 hard error. Build is an ingestion
	// boundary (callable directly with a raw config.Config, bypassing
	// config.Load's validateJudge), so votes are validated the same way here:
	// unset (0) defaults to 1, but a negative or even value is a loud error
	// rather than a silently-coerced guess (Constitution IV, no silent fallbacks).
	judge.RegisterBuiltins(reg)

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
	if err := applyExtras(cfg, o, reg); err != nil {
		return nil, err
	}

	backend := cfg.Judge.Backend
	if backend == "" {
		backend = "claude"
	}
	jf, ok := reg.Judge(backend)
	if !ok {
		return nil, fmt.Errorf("unknown judge backend %q", backend)
	}
	j, err := jf(cfg)
	if err != nil {
		return nil, fmt.Errorf("build judge %q: %w", backend, err)
	}
	if isNilSeam(j) {
		return nil, fmt.Errorf("build judge %q: factory returned a nil judge with no error", backend)
	}
	votes := cfg.Judge.Votes
	if votes == 0 {
		votes = 1
	}
	if votes < 1 || votes%2 == 0 {
		return nil, fmt.Errorf("judge.votes must be a positive odd integer, got %d", votes)
	}
	reg.RegisterMatcher("semantic", comparator.NewSemantic(j, votes))

	// Apply internal/test matcher overrides AFTER the built-in and semantic matchers.
	// Matchers have no facade collision check (WithExtraMatcher is an internal hook),
	// so this is deliberately last-writer-wins: a test can substitute the "semantic"
	// matcher with a mock.
	for _, em := range o.extraMatchers {
		reg.RegisterMatcher(em.name, em.matcher)
	}

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
		if _, ok := reg.Driver(cfg.Targets[name].Adapter); !ok {
			return nil, fmt.Errorf("engine: target %q: adapter %q has no registered driver (registered: %s)", name, cfg.Targets[name].Adapter, strings.Join(reg.Drivers(), ", "))
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
	reg.Seal() // wiring complete: post-build registration now fails loudly (FR-009)

	// Capture this engine's contributed Gherkin phrases HERE, once, while the registry
	// is sealed and complete. Every surface that later asks reads that one snapshot, so
	// validation, the step reference and suite registration describe the same run
	// (FR-001/FR-005).
	phrases, err := capturePhrases(reg)
	if err != nil {
		return nil, err
	}
	return &Engine{cfg: cfg, cor: cor, st: st, sems: sems, pricing: pricing, patterns: pats, logger: o.logger, reg: reg, phrases: phrases}, nil
}

// capturePhrases walks reg's comparators in sorted name order and collects every
// Gherkin phrase they contribute, paired with its contributor.
//
// Sorted order is a CORRECTNESS requirement, not tidiness: the runner returns the first
// matching step definition, so registration order decides which pattern wins a
// collision, and map iteration order would make an unchanged suite resolve differently
// between runs. Registry.Comparators() already sorts; each comparator's own declaration
// order is preserved within its block.
//
// It runs exactly once per engine, inside Build and after the seal, which is what makes
// the snapshot sound: a stray post-seal Register* panics rather than mutating the set,
// so the captured answer cannot go stale.
//
// The []core.ContributedPhrase -> []PhraseBinding transform IS the snapshot's copy —
// range copies each element and the composite literal copies it again into out's own
// backing array, and every field in the graph is a string. A comparator that keeps the
// slice it returned and mutates it afterwards therefore cannot reach this answer
// (FR-002); no separate copying step is needed here.
func capturePhrases(reg *registry.Registry) ([]PhraseBinding, error) {
	var out []PhraseBinding
	for _, name := range reg.Comparators() {
		c, ok := reg.Comparator(name)
		if !ok {
			// Unreachable today: name came from this same sealed registry's own
			// listing. Reported rather than skipped anyway, because the failure mode
			// of skipping is nasty and silent — the comparator's phrases vanish and
			// every sentence using them reports as an unbound step, sending the author
			// to look at their feature file for a defect that is in the registry.
			return nil, fmt.Errorf("engine: comparator %q is listed by the registry but cannot be resolved from it", name)
		}
		pc, ok := c.(core.PhraseContributor)
		if !ok {
			// The seam is optional and discovered by type assertion. A comparator
			// that does not implement it contributes nothing — this is what keeps
			// every existing comparator working untouched.
			continue
		}
		for _, p := range pc.ContributedPhrases() {
			out = append(out, PhraseBinding{Comparator: name, Phrase: p})
		}
	}
	return out, nil
}

// isNilSeam reports whether v is a nil seam — either a direct nil interface or an
// interface holding a typed-nil pointer/func/map/etc (e.g. (*customDriver)(nil)). A
// factory that returns such a value with no error would otherwise register a landmine
// that panics when the seam is first invoked; rejecting it at Build keeps failures
// loud and early (Constitution IV: no zero-value success). A concrete non-pointer
// value (a struct seam) is never nil.
func isNilSeam(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

// applyExtras registers the facade-funneled driver/comparator/judge seams into the
// (open) per-engine registry reg, each guarded by a collision check that fails loudly
// naming the seam and the conflicting name (FR-002). It runs inside Build, between
// built-in registration and Seal, so post-seal registration stays unrepresentable.
func applyExtras(cfg config.Config, o options, reg *registry.Registry) error {
	for _, ed := range o.extraDrivers {
		if ed.factory == nil {
			return fmt.Errorf("engine: WithDriver: driver factory %q is nil; a registered driver factory must be non-nil", ed.name)
		}
		// Collision BEFORE construction: a colliding registration never runs ITS
		// factory, so the caller sees the collision, not a factory error. (In a
		// duplicate-name pair the first entry is not colliding and is still built.)
		if _, exists := reg.Driver(ed.name); exists {
			return fmt.Errorf("engine: WithDriver: adapter %q is already registered (a built-in or an earlier registration); adapter names must be unique", ed.name)
		}
		drv, err := ed.factory(cfg)
		if err != nil {
			return fmt.Errorf("engine: WithDriver: build driver %q: %w", ed.name, err)
		}
		if isNilSeam(drv) {
			return fmt.Errorf("engine: WithDriver: driver %q: factory returned a nil driver with no error", ed.name)
		}
		reg.RegisterDriver(ed.name, drv)
	}
	for _, ec := range o.extraComparators {
		if ec.factory == nil {
			return fmt.Errorf("engine: WithComparator: comparator factory %q is nil; a registered comparator factory must be non-nil", ec.name)
		}
		if _, exists := reg.Comparator(ec.name); exists {
			return fmt.Errorf("engine: WithComparator: comparator %q is already registered (a built-in or an earlier registration); comparator names must be unique", ec.name)
		}
		cmp, err := ec.factory(cfg)
		if err != nil {
			return fmt.Errorf("engine: WithComparator: build comparator %q: %w", ec.name, err)
		}
		if isNilSeam(cmp) {
			return fmt.Errorf("engine: WithComparator: comparator %q: factory returned a nil comparator with no error", ec.name)
		}
		reg.RegisterComparator(ec.name, cmp)
	}
	for _, er := range o.extraReporters {
		if er.factory == nil {
			return fmt.Errorf("engine: WithReporter: reporter factory %q is nil; a registered reporter factory must be non-nil", er.name)
		}
		if _, exists := reg.Reporter(er.name); exists {
			return fmt.Errorf("engine: WithReporter: reporter %q is already registered (a built-in or an earlier registration); reporter names must be unique", er.name)
		}
		rep, err := er.factory(cfg)
		if err != nil {
			return fmt.Errorf("engine: WithReporter: build reporter %q: %w", er.name, err)
		}
		if isNilSeam(rep) {
			return fmt.Errorf("engine: WithReporter: reporter %q: factory returned a nil reporter with no error", er.name)
		}
		reg.RegisterReporter(er.name, rep)
	}
	for _, ej := range o.extraJudges {
		if ej.factory == nil {
			return fmt.Errorf("engine: WithJudge: judge factory %q is nil; a registered judge factory must be non-nil", ej.name)
		}
		if _, exists := reg.Judge(ej.name); exists {
			return fmt.Errorf("engine: WithJudge: judge %q is already registered (a built-in or an earlier registration); judge names must be unique", ej.name)
		}
		reg.RegisterJudge(ej.name, ej.factory)
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
