package mentat

import (
	"context"
	"errors"
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
	// output is the destination for the godog pretty report. nil means library mode:
	// narrate nothing (io.Discard). Set via WithOutput.
	output io.Writer
	// logWriter is the destination for the slog run narration. nil means library mode:
	// a silent discard logger (unchanged default). Set via WithVerbosity.
	logWriter io.Writer
	// verbose/debug map to the -v/-vv level of the injected logger. Both false (the
	// default) yield a discard handler regardless of logWriter. Set via WithVerbosity.
	verbose bool
	debug   bool
	// concurrency is the godog scenario-concurrency level. <1 means the default (1).
	// Set via WithConcurrency.
	concurrency int
	// tags is the godog tag expression selecting which scenarios run. "" means no
	// filter (all scenarios). Set via WithTags.
	tags string
	// failFast maps to godog's StopOnFailure: stop the suite after the first failing
	// scenario. false (run everything) is the default. Set via WithFailFast.
	failFast bool
	// reports maps a reporter name (json/html/junit) to an output path; empty (the
	// default) writes no report files. Set via WithReports.
	reports map[string]string
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

// WithOutput narrates the godog pretty report to w. The default is library mode:
// with no WithOutput, Run narrates nothing (io.Discard), so embedding Mentat never
// writes to stdout unless the caller asks. A nil w is treated as the default
// (Constitution IV: a zero value behaves as the documented default, never a crash).
func WithOutput(w io.Writer) Option {
	return func(o *runOptions) { o.output = w }
}

// WithVerbosity narrates the run to w via a log/slog text handler at the given
// level: verbose maps to Info, debug (which implies verbose) to Debug — the same
// -v/-vv mapping the CLI exposes (engine.NewLogger). The default is library mode:
// with no WithVerbosity (or both flags false) Run injects a silent discard logger, so
// embedding Mentat narrates nothing unless the caller asks. A nil w is treated as the
// silent default (Constitution IV: a zero value behaves as the documented default,
// never a nil-writer panic).
func WithVerbosity(w io.Writer, verbose, debug bool) Option {
	return func(o *runOptions) {
		o.logWriter = w
		o.verbose = verbose
		o.debug = debug
	}
}

// WithConcurrency sets the godog scenario-concurrency level. A value < 1 means the
// default (1, unchanged); a value > 1 runs scenarios in parallel (godog wraps the
// pretty formatter so per-scenario output stays segregated).
func WithConcurrency(n int) Option {
	return func(o *runOptions) { o.concurrency = n }
}

// WithTags sets the godog tag expression selecting which scenarios run (e.g.
// "@smoke", "@a && ~@wip"). The empty string means no filter (all scenarios run) —
// the default.
func WithTags(expr string) Option {
	return func(o *runOptions) { o.tags = expr }
}

// WithFailFast stops the suite after the first failing scenario (godog's
// StopOnFailure). The default is false: run every scenario and report the full
// tally.
func WithFailFast(stop bool) Option {
	return func(o *runOptions) { o.failFast = stop }
}

// WithReports writes each requested report after the suite runs and its judge ledger
// is priced. targets maps a built-in reporter name (json/html/junit) to an output
// path; each file is written atomically (temp+rename). The default is no reports (an
// empty map): embedding Mentat writes no files unless the caller asks. A write failure
// is a Run error that preserves the underlying reporter wording (e.g. "writing junit
// report") and still returns the populated Results — the suite ran, only emission
// failed (Constitution IV: no silent fallback).
func WithReports(targets map[string]string) Option {
	return func(o *runOptions) { o.reports = targets }
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

// ExitCode maps Results onto the process exit code the CLI uses, so a library
// consumer (and the CLI as "consumer zero") can turn a Run into an os.Exit code with
// one call: an interrupted run is 130 (128 + SIGINT, and it wins over a red suite so
// CI can tell cancellation from a plain failure), else any failed scenario is 1, else
// 0. This is the Results ⇔ CLI exit-semantics contract (public-surface.md).
func (r Results) ExitCode() int {
	switch {
	case r.Interrupted:
		return 130
	case r.Failed > 0:
		return 1
	default:
		return 0
	}
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

	// Narration: the same logger is injected into every seam (correlator + engine +
	// drivers) — no package-global logger, no slog.SetDefault. With no WithVerbosity
	// the writer is nil and both flags false, so NewLogger returns a discard handler:
	// library mode narrates nothing, byte-identical to the pre-Batch-2 default. A nil
	// writer with a level set narrates to io.Discard (Constitution IV: no nil-writer
	// panic).
	logWriter := ro.logWriter
	if logWriter == nil {
		logWriter = io.Discard
	}
	logger := engine.NewLogger(logWriter, ro.verbose, ro.debug)

	cor, err := engine.BuildCorrelator(cfg, logger)
	if err != nil {
		return Results{}, fmt.Errorf("mentat: build correlator: %w", err)
	}

	// Funnel custom store factories into the store composition root. The factory is
	// passed through untouched (Config/TraceStore are aliases to the internal types),
	// so a duplicate store name surfaces as a loud collision error from BuildStore.
	var storeOpts []engine.Option
	for _, s := range ro.stores {
		if s.factory == nil {
			return Results{}, fmt.Errorf("mentat: WithStore %q: nil factory; register a non-nil StoreFactory", s.name)
		}
		storeOpts = append(storeOpts, engine.WithExtraStore(s.name, func(c config.Config) (core.TraceStore, error) {
			return s.factory(c)
		}))
	}
	st, err := engine.BuildStore(cfg, storeOpts...)
	if err != nil {
		return Results{}, fmt.Errorf("mentat: build store: %w", err)
	}

	// Build the engine composition options: the silent logger first, then the custom
	// seams. Drivers/comparators are funneled as factories (like stores/judges), so the
	// engine defers construction until AFTER its collision check — a name clashing with
	// a built-in surfaces a loud collision error and never runs the factory (FR-002),
	// and a factory error surfaces at Build (build engine), wrapped and named there
	// (Constitution IV: never a silent nil seam).
	buildOpts := []engine.Option{engine.WithLogger(logger)}
	for _, d := range ro.drivers {
		if d.factory == nil {
			return Results{}, fmt.Errorf("mentat: WithDriver %q: nil factory; register a non-nil DriverFactory", d.name)
		}
		buildOpts = append(buildOpts, engine.WithExtraDriver(d.name, func(conf config.Config) (core.Driver, error) {
			return d.factory(conf)
		}))
	}
	for _, c := range ro.comparators {
		if c.factory == nil {
			return Results{}, fmt.Errorf("mentat: WithComparator %q: nil factory; register a non-nil ComparatorFactory", c.name)
		}
		buildOpts = append(buildOpts, engine.WithExtraComparator(c.name, func(conf config.Config) (core.Comparator, error) {
			return c.factory(conf)
		}))
	}
	// Judges are passed through as factories (like the built-in "claude" backend):
	// the engine resolves one only when cfg.Judge.Backend names it, so a factory
	// error surfaces at Build (build engine), not here.
	for _, j := range ro.judges {
		if j.factory == nil {
			return Results{}, fmt.Errorf("mentat: WithJudge %q: nil factory; register a non-nil JudgeFactory", j.name)
		}
		buildOpts = append(buildOpts, engine.WithExtraJudge(j.name, func(c config.Config) (core.Judge, error) {
			return j.factory(c)
		}))
	}
	eng, err := engine.Build(cfg, st, cor, buildOpts...)
	if err != nil {
		return Results{}, fmt.Errorf("mentat: build engine: %w", err)
	}

	// Library mode narrates nothing by default: an unset WithOutput leaves the godog
	// pretty report going to io.Discard (Constitution IV: a nil writer behaves as the
	// documented default, never a nil-writer panic in godog).
	out := ro.output
	if out == nil {
		out = io.Discard
	}
	// Default concurrency stays 1: an unset or non-positive value is the documented
	// default, not a zero passed through to godog (Constitution IV).
	concurrency := ro.concurrency
	if concurrency < 1 {
		concurrency = 1
	}

	// Judge-spend budget (US6): once completed judge cost crosses cfg.Judge.MaxCostUSD,
	// the After hook cancels budgetCtx so no NEW scenario starts a judge call. budgetCtx
	// is a CHILD of the passed ctx (a caller cancellation still cancels everything), but
	// `interrupted` keys off the PASSED ctx alone below — a budget trip cancels budgetCtx
	// yet must NOT mark the run interrupted. An unset/0 ceiling disables accounting:
	// report.NewBudget with max<=0 makes Add a no-op, so an unbudgeted run stays
	// byte-identical to the pre-Batch-2 flow.
	budget := report.NewBudget(cfg.Judge.MaxCostUSD, eng.Pricing())
	budgetCtx, budgetCancel := context.WithCancel(ctx)
	defer budgetCancel()

	col := report.NewCollector()
	suite := godog.TestSuite{
		ScenarioInitializer: steps.InitializerWithBudget(eng, col, budget, budgetCancel),
		Options: &godog.Options{
			Format:         "pretty",
			Paths:          ro.featurePaths,
			Output:         out,
			DefaultContext: budgetCtx,
			Concurrency:    concurrency,
			Tags:           ro.tags,
			StopOnFailure:  ro.failFast,
		},
	}

	// Validate feature loading up front: suite.Run folds a load/parse failure into a
	// non-zero exit code with zero collected scenarios, which would otherwise surface
	// as an empty green Results — a silent fallback (Constitution IV). RetrieveFeatures
	// parses the same Options.Paths, so a missing or malformed .feature is a loud,
	// path-named harness error here instead of a fake success (and godog does not leak
	// the parse error to stderr on the discarded Run path).
	if _, ferr := suite.RetrieveFeatures(); ferr != nil {
		return Results{}, fmt.Errorf("mentat: load features %v: %w", ro.featurePaths, ferr)
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

	// Emit the requested report files AFTER pricing (so the judge ledger cost is
	// rendered), mirroring the CLI. A write failure preserves the reporter wording
	// (e.g. "writing junit report") and still returns the completed Results — the run
	// happened, only emission failed (Constitution IV). No WithReports ⇒ no files. The
	// error is captured (not early-returned) so a simultaneous budget trip is not masked.
	var emitErr error
	if len(ro.reports) > 0 {
		if e := report.EmitReports(rep, ro.reports); e != nil {
			emitErr = fmt.Errorf("mentat: emit reports: %w", e)
		}
	}

	// A tripped judge budget is a hard Run error (US6), read AFTER pricing and report
	// emission so the operator still gets the full accounting first (mirrors the CLI
	// ordering). The wrapped Budget error names spend, ceiling, and the crossing
	// scenario. An unset/0 ceiling never trips, so Err() is nil.
	var budgetErr error
	if berr := budget.Err(); berr != nil {
		budgetErr = fmt.Errorf("mentat: judge budget: %w", berr)
	}

	// Surface BOTH failures when they coincide: neither the emit error nor the budget
	// trip masks the other (the pre-recompose CLI printed both). errors.Join drops nils,
	// so an emit-only or budget-only run keeps its single, original message intact.
	if emitErr != nil || budgetErr != nil {
		return toResults(rep), errors.Join(emitErr, budgetErr)
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
