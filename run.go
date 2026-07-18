package mentat

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/cucumber/godog"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/report"
	"github.com/thetonymaster/mentat/internal/steps"
)

// LoadConfig reads and parses a mentat.yaml file into a Config. The path is named
// in every error (a missing/unreadable file or a malformed document) so a failure
// is diagnosable from the message alone — never a silent zero-value Config
// (Constitution IV).
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("mentat: read config %q: %w", path, err)
	}
	cfg, err := config.Load(data)
	if err != nil {
		return Config{}, fmt.Errorf("mentat: parse config %q: %w", path, err)
	}
	return cfg, nil
}

// Option configures a Run. The concrete option state is an unexported struct, so the
// option set stays opaque and extensible: later tasks add custom-adapter registration
// options (WithDriver/WithStore/…) without a breaking surface change.
type Option func(*runOptions)

// runOptions is the resolved, unexported Run configuration built from the Options.
type runOptions struct {
	// featurePaths are the feature files/directories the suite runs (godog Paths).
	featurePaths []string
	// drivers/stores/comparators/judges are custom-adapter registrations funneled into
	// the composition root by Run (spec 007 FR-002); each is a name + a factory built
	// from Config.
	drivers     []driverReg
	stores      []storeReg
	comparators []comparatorReg
	judges      []judgeReg
}

// driverReg / storeReg / comparatorReg / judgeReg pair a registration name with its
// factory. The name is what config and feature files reference; it must be unique
// across the built-ins and any earlier registration, else Run returns a loud
// collision error.
type (
	driverReg struct {
		name    string
		factory DriverFactory
	}
	storeReg struct {
		name    string
		factory StoreFactory
	}
	comparatorReg struct {
		name    string
		factory ComparatorFactory
	}
	judgeReg struct {
		name    string
		factory JudgeFactory
	}
)

// DriverFactory builds a custom Driver from the resolved Config. Registered under a
// name via WithDriver; the name then works in config/feature files like a built-in
// adapter. A factory that cannot build its driver returns an error, never a nil
// driver (Constitution IV).
type DriverFactory = func(Config) (Driver, error)

// StoreFactory builds a custom TraceStore from the resolved Config. Registered
// under a name via WithStore; cfg.Store then selects it like a built-in store.
type StoreFactory = func(Config) (TraceStore, error)

// ComparatorFactory builds a custom Comparator from the resolved Config. Registered
// under a name via WithComparator. The comparator reads Evidence only (Constitution
// I). NOTE: the built-in Gherkin grammar maps steps onto the built-in comparator
// names, so a custom comparator is registered and composable today but is not yet
// invokable from a .feature step without new grammar. First-class custom-comparator
// Gherkin steps are deliberately OUT of scope for feature 007 (which publishes the
// registration surface); they are deferred to a dedicated future spec (008).
type ComparatorFactory = func(Config) (Comparator, error)

// JudgeFactory builds a custom Judge from the resolved Config. Registered under a
// name via WithJudge; the judge is USED only when cfg.Judge.Backend names it (like
// the built-in "claude" backend), but the name collision check runs unconditionally.
type JudgeFactory = func(Config) (Judge, error)

// WithFeatures adds feature files or directories the suite runs. It is additive
// across calls, so WithFeatures("a") and WithFeatures("b") together run both. At
// least one feature path is required — Run has no implicit "features" default
// (Constitution IV: no silent fallback).
func WithFeatures(paths ...string) Option {
	return func(o *runOptions) { o.featurePaths = append(o.featurePaths, paths...) }
}

// WithDriver registers a custom Driver factory under name, funneling it into this
// Run's composition root before sealing. The name works in config (Target.Adapter)
// and feature files exactly like a built-in adapter (US1). A name already taken by
// a built-in or an earlier registration is a loud collision error from Run
// (FR-002); registration cannot happen after the root is sealed (options exist only
// here).
func WithDriver(name string, f DriverFactory) Option {
	return func(o *runOptions) { o.drivers = append(o.drivers, driverReg{name: name, factory: f}) }
}

// WithStore registers a custom TraceStore factory under name; cfg.Store then selects
// it. Same collision discipline as WithDriver.
func WithStore(name string, f StoreFactory) Option {
	return func(o *runOptions) { o.stores = append(o.stores, storeReg{name: name, factory: f}) }
}

// WithComparator registers a custom Comparator factory under name, funneled into
// this Run's composition root. Same collision discipline as WithDriver.
func WithComparator(name string, f ComparatorFactory) Option {
	return func(o *runOptions) { o.comparators = append(o.comparators, comparatorReg{name: name, factory: f}) }
}

// WithJudge registers a custom Judge factory under name. The judge is USED only when
// cfg.Judge.Backend names it; same collision discipline as WithDriver otherwise.
func WithJudge(name string, f JudgeFactory) Option {
	return func(o *runOptions) { o.judges = append(o.judges, judgeReg{name: name, factory: f}) }
}

// Results is the structured outcome of a Run — the library-mode equivalent of the
// CLI's report + exit status (FR-003). A red suite is reflected here (Failed > 0),
// not as a Run error: Run returns a non-nil error only for harness/composition
// failures. TotalCost and JudgeTotal mirror core.RunReport's suite aggregates;
// JudgeTotal is nil unless a scenario actually made a judge call (no fabricated
// zeros).
type Results struct {
	Scenarios   []ScenarioResult
	Passed      int
	Failed      int
	Interrupted bool
	TotalCost   float64
	JudgeTotal  *JudgeUsage
}

// ScenarioResult is one scenario's outcome. It is a facade-owned struct (not an
// alias) so the internal report record types (RunRecord etc.) never leak through the
// public surface. RunIDs are the injected run ids of the scenario's runs (>1 for a
// @runs(N) scenario). Judge is this scenario's judge ledger, nil when it made no
// judge call.
type ScenarioResult struct {
	Name string
	// FeatureFile is the source .feature file this scenario was parsed from (godog's
	// scenario Uri), so a consumer running several feature files can tell scenarios
	// apart by origin, not just by Name (which may collide across files).
	FeatureFile    string
	Pass           bool
	Reasons        []string
	Cost           float64
	RunIDs         []string
	DerivationNote string
	Judge          *JudgeUsage
}

// Run executes the behaviour suite in-process against cfg and returns structured
// Results. It mirrors the CLI's `run` composition (engine.BuildCorrelator →
// BuildStore → Build → godog) minus process concerns (no os.Exit, no stdout, no
// signal handling): the run narrates nothing (library mode) and honours ctx
// cancellation via godog's DefaultContext.
//
// Run returns a non-nil error ONLY for a harness/composition failure (missing
// feature paths, a bad store/correlator/engine build, or a judge-pricing failure).
// A scenario failing its comparators is NOT an error — it is Results{Failed>0}.
func Run(ctx context.Context, cfg Config, opts ...Option) (Results, error) {
	var ro runOptions
	for _, opt := range opts {
		opt(&ro)
	}
	// No implicit "features" default: godog silently defaults an empty Paths to the
	// ./features directory, which would run an unrelated corpus. Refuse loudly
	// instead (Constitution IV: no silent fallback).
	if len(ro.featurePaths) == 0 {
		return Results{}, fmt.Errorf("mentat: no feature paths; pass at least one via WithFeatures")
	}

	// Library mode narrates nothing: a silent discard logger is injected into every
	// seam (no package-global logger, no slog.SetDefault) — mirrors the CLI's no-flag
	// default.
	logger := engine.NewLogger(io.Discard, false, false)

	cor, err := engine.BuildCorrelator(cfg, logger)
	if err != nil {
		return Results{}, fmt.Errorf("mentat: build correlator: %w", err)
	}

	// Funnel custom store factories into the store composition root. The factory is
	// passed through untouched (Config/TraceStore are aliases to the internal types),
	// so a duplicate store name surfaces as a loud collision error from BuildStore.
	var storeOpts []engine.Option
	for _, s := range ro.stores {
		storeOpts = append(storeOpts, engine.WithExtraStore(s.name, func(c config.Config) (core.TraceStore, error) {
			return s.factory(c)
		}))
	}
	st, err := engine.BuildStore(cfg, storeOpts...)
	if err != nil {
		return Results{}, fmt.Errorf("mentat: build store: %w", err)
	}

	// Build the engine composition options: the silent logger first, then the custom
	// drivers. A driver factory is CALLED here (with cfg) to get the instance the
	// engine registers; a factory error is a wrapped, named harness error, never a
	// silent nil driver (Constitution IV).
	buildOpts := []engine.Option{engine.WithLogger(logger)}
	for _, d := range ro.drivers {
		inst, ferr := d.factory(cfg)
		if ferr != nil {
			return Results{}, fmt.Errorf("mentat: build driver %q: %w", d.name, ferr)
		}
		buildOpts = append(buildOpts, engine.WithExtraDriver(d.name, inst))
	}
	for _, c := range ro.comparators {
		inst, ferr := c.factory(cfg)
		if ferr != nil {
			return Results{}, fmt.Errorf("mentat: build comparator %q: %w", c.name, ferr)
		}
		buildOpts = append(buildOpts, engine.WithExtraComparator(c.name, inst))
	}
	// Judges are passed through as factories (like the built-in "claude" backend):
	// the engine resolves one only when cfg.Judge.Backend names it, so a factory
	// error surfaces at Build (build engine), not here.
	for _, j := range ro.judges {
		buildOpts = append(buildOpts, engine.WithExtraJudge(j.name, func(c config.Config) (core.Judge, error) {
			return j.factory(c)
		}))
	}
	eng, err := engine.Build(cfg, st, cor, buildOpts...)
	if err != nil {
		return Results{}, fmt.Errorf("mentat: build engine: %w", err)
	}

	col := report.NewCollector()
	suite := godog.TestSuite{
		ScenarioInitializer: steps.InitializerWithCollector(eng, col),
		Options: &godog.Options{
			Format:         "pretty",
			Paths:          ro.featurePaths,
			Output:         io.Discard,
			DefaultContext: ctx,
			Concurrency:    1,
		},
	}

	started := time.Now()
	_ = suite.Run() // the exit code is folded into Results via the collector, not returned
	interrupted := ctx.Err() != nil

	rep := col.Report(started, time.Since(started), interrupted)
	// Price fills the judge-ledger cost (a no-op with no judge calls). An
	// unknown/ambiguous judge model is a hard Run error — never a report carrying a
	// fabricated $0 for a real call (Constitution IV).
	if err := report.Price(&rep, eng.Pricing()); err != nil {
		return Results{}, fmt.Errorf("mentat: price judge ledger: %w", err)
	}

	return toResults(rep), nil
}

// toResults maps the internal RunReport onto the facade-owned Results, carrying the
// suite aggregates and JudgeTotal verbatim (nil stays nil — no fabricated zeros).
func toResults(rep core.RunReport) Results {
	res := Results{
		Passed:      rep.Passed,
		Failed:      rep.Failed,
		Interrupted: rep.Interrupted,
		TotalCost:   rep.TotalCost,
		JudgeTotal:  rep.JudgeTotal,
	}
	for _, sc := range rep.Scenarios {
		runIDs := make([]string, 0, len(sc.Runs))
		for _, r := range sc.Runs {
			runIDs = append(runIDs, r.RunID)
		}
		res.Scenarios = append(res.Scenarios, ScenarioResult{
			Name:           sc.Name,
			FeatureFile:    sc.FeatureFile,
			Pass:           sc.Pass,
			Reasons:        sc.Reasons,
			Cost:           sc.Cost,
			RunIDs:         runIDs,
			DerivationNote: sc.DerivationNote,
			Judge:          sc.Judge,
		})
	}
	return res
}
