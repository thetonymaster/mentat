// Package mentat_test drives the library-mode entry point (mentat.Run) through
// the PUBLIC facade only — no internal/... imports — proving a third party can
// embed Mentat and read structured results using the published surface alone
// (spec 007 FR-003, US2 "Independent Test"; SC-002).
//
// Hermetic file-store setup is modelled on internal/steps/filestore_replay_test.go.
package mentat_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/thetonymaster/mentat"
)

// replayFixture is a saved run keyed on runScenario "r" (the file store keys on
// that field). The live mentat.Run path injects a FRESH UUID run id per run, so
// this fixture is deliberately unreachable by run id under the built-in
// correlator — see TestRunComposesAndReturnsStructuredResults for why that is the
// point.
const replayFixture = `{
  "runScenario": "r",
  "spans": [
    {"name":"invoke_agent researchbot","parentIndex":-1,"status":"Ok","attrs":{"gen_ai.operation.name":"invoke_agent","gen_ai.usage.input_tokens":"1200","gen_ai.usage.output_tokens":"600"}},
    {"name":"execute_tool search","parentIndex":0,"status":"Ok","attrs":{"gen_ai.operation.name":"execute_tool","gen_ai.tool.name":"search"}}
  ]
}`

// writeFile writes contents to name under dir and returns the absolute path.
func writeFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// echoTarget is a hermetic shell SUT: it prints "hi" and exits, so the drive step
// succeeds and the run fails only at trace resolution (the crux under test).
func echoTarget() map[string]mentat.Target {
	return map[string]mentat.Target{
		"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1},
	}
}

// TestRunComposesAndReturnsStructuredResults proves mentat.Run composes the whole
// engine (correlator + file store + engine + godog) and returns well-formed
// Results — WITHOUT requiring a green suite. The built-in correlator injects a
// UUID run id (engine.BuildCorrelator → uuid.NewString), which the file store —
// keyed on the fixed runScenario "r" — cannot resolve, so the single run fails at
// resolution and the scenario goes RED deterministically. A red suite is NOT a Run
// error: Run returns (Results{Failed>0}, nil).
func TestRunComposesAndReturnsStructuredResults(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "run-r.json", replayFixture)
	feature := `Feature: library run
  Scenario: drives a target and resolves its trace
    Given the agent target "bot"
    When I run scenario "happy"
    Then total tokens are under 5000
`
	featPath := writeFile(t, dir, "run.feature", feature)

	cfg := mentat.Config{Store: "file", StorePath: dir, Targets: echoTarget()}

	res, err := mentat.Run(context.Background(), cfg, mentat.WithFeatures(featPath))
	if err != nil {
		t.Fatalf("Run returned a harness error (scenario failure must not be one): %v", err)
	}
	if len(res.Scenarios) != 1 {
		t.Fatalf("got %d scenarios, want 1", len(res.Scenarios))
	}
	if res.Passed != 0 || res.Failed != 1 {
		t.Fatalf("tally passed=%d failed=%d, want 0/1", res.Passed, res.Failed)
	}
	if res.Interrupted {
		t.Fatal("run was not cancelled; Interrupted must be false")
	}
	sr := res.Scenarios[0]
	if sr.Pass {
		t.Fatalf("scenario unexpectedly passed: %+v", sr)
	}
	if len(sr.Reasons) == 0 {
		t.Fatal("a failed scenario must carry reasons")
	}
	// The reason proves resolution went through the FILE store and missed: the
	// injected UUID run id has no fixture. This is the crux evidence that the
	// built-in UUID correlator cannot green a file-store run.
	if joined := strings.Join(sr.Reasons, " "); !strings.Contains(joined, "no fixture") {
		t.Fatalf("reason should name the file-store miss, got %q", joined)
	}
	if len(sr.RunIDs) == 0 || sr.RunIDs[0] == "" {
		t.Fatalf("scenario must carry the injected run id, got %v", sr.RunIDs)
	}
	if res.JudgeTotal != nil {
		t.Fatalf("no judge call was made; JudgeTotal must be nil, got %+v", res.JudgeTotal)
	}
}

// TestRunRequiresFeaturePaths proves Run has no implicit "features" default: with
// no WithFeatures (or an empty one) it returns a descriptive error rather than
// silently running zero scenarios and reporting success (Constitution IV).
func TestRunRequiresFeaturePaths(t *testing.T) {
	tests := []struct {
		name string
		opts []mentat.Option
	}{
		{name: "no options at all"},
		{name: "empty WithFeatures", opts: []mentat.Option{mentat.WithFeatures()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := mentat.Config{Store: "file", StorePath: t.TempDir(), Targets: echoTarget()}
			res, err := mentat.Run(context.Background(), cfg, tt.opts...)
			if err == nil {
				t.Fatalf("Run without feature paths must error, got results %+v", res)
			}
			if !strings.Contains(err.Error(), "feature") {
				t.Fatalf("error should name the missing feature paths, got %q", err.Error())
			}
		})
	}
}

// TestRunFailsOnUnloadableFeatures proves Run does not silently succeed when its
// feature paths cannot be loaded: a nonexistent path or a malformed .feature makes
// godog's suite return a non-zero status with zero collected scenarios, which —
// if the status were discarded — would look like an empty green run. Run must
// instead surface a descriptive harness error with empty Results (Constitution IV:
// no silent fallback).
func TestRunFailsOnUnloadableFeatures(t *testing.T) {
	dir := t.TempDir()
	malformed := writeFile(t, dir, "bad.feature", "this is not valid gherkin\n:::\n")
	tests := []struct {
		name    string
		path    string
		wantSub string
	}{
		{name: "nonexistent feature path", path: filepath.Join(dir, "does-not-exist.feature"), wantSub: "feature"},
		{name: "malformed feature file", path: malformed, wantSub: "feature"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := mentat.Config{Store: "file", StorePath: dir, Targets: echoTarget()}
			res, err := mentat.Run(context.Background(), cfg, mentat.WithFeatures(tt.path))
			if err == nil {
				t.Fatalf("Run with unloadable features must error, got results %+v", res)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("error should name the feature-load failure, got %q", err.Error())
			}
			if len(res.Scenarios) != 0 || res.Passed != 0 || res.Failed != 0 {
				t.Fatalf("a harness error must return empty Results, got %+v", res)
			}
		})
	}
}

// TestRunRejectsNilFactories proves the public With* registration options reject a
// nil factory with a descriptive harness error instead of panicking deep in the
// composition root. The facade is the supported extension surface (FR-002/SC-002:
// "a third party can embed Mentat"), so a nil factory passed through it must fail
// loudly and unconditionally — like a name collision does — never nil-pointer
// dereference when the factory is invoked or wrapped (Constitution IV). This is the
// facade-level counterpart to the engine-seam nil guards: the facade wraps
// store/judge factories in a non-nil closure, so the engine guard cannot see the
// caller's nil — it must be caught here.
func TestRunRejectsNilFactories(t *testing.T) {
	dir := t.TempDir()
	feature := `Feature: f
  Scenario: s
    Given the agent target "bot"
    When I run scenario "x"
    Then total tokens are under 5000
`
	featPath := writeFile(t, dir, "f.feature", feature)
	cfg := mentat.Config{Store: "file", StorePath: dir, Targets: echoTarget()}

	tests := []struct {
		name    string
		opt     mentat.Option
		wantSub string
	}{
		{name: "nil driver factory", opt: mentat.WithDriver("xd", nil), wantSub: "WithDriver"},
		{name: "nil store factory", opt: mentat.WithStore("xs", nil), wantSub: "WithStore"},
		{name: "nil comparator factory", opt: mentat.WithComparator("xc", nil), wantSub: "WithComparator"},
		{name: "nil judge factory", opt: mentat.WithJudge("xj", nil), wantSub: "WithJudge"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := mentat.Run(context.Background(), cfg, mentat.WithFeatures(featPath), tt.opt)
			if err == nil {
				t.Fatalf("Run with a %s must error, got results %+v", tt.name, res)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("error should name the seam option, got %q", err.Error())
			}
			if len(res.Scenarios) != 0 || res.Passed != 0 || res.Failed != 0 {
				t.Fatalf("a harness error must return empty Results, got %+v", res)
			}
		})
	}
}

// TestRunHarnessErrors proves a composition failure is a wrapped, path-named Run
// error with empty Results — never a partial silent success (Constitution IV).
func TestRunHarnessErrors(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		cfg     mentat.Config
		wantSub string
	}{
		{
			name:    "unknown store",
			cfg:     mentat.Config{Store: "bogus", StorePath: dir, Targets: echoTarget()},
			wantSub: "build store",
		},
		{
			name:    "malformed poll interval",
			cfg:     mentat.Config{Store: "file", StorePath: dir, Targets: echoTarget(), Poll: mentat.PollSpec{Interval: "notaduration"}},
			wantSub: "build correlator",
		},
		{
			name:    "unknown judge backend",
			cfg:     mentat.Config{Store: "file", StorePath: dir, Targets: echoTarget(), Judge: mentat.JudgeConfig{Backend: "bogus"}},
			wantSub: "build engine",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := mentat.Run(context.Background(), tt.cfg, mentat.WithFeatures("x.feature"))
			if err == nil {
				t.Fatalf("Run must return a harness error, got results %+v", res)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("error should be wrapped with %q, got %q", tt.wantSub, err.Error())
			}
			if len(res.Scenarios) != 0 || res.Failed != 0 || res.Passed != 0 {
				t.Fatalf("a harness failure must return empty Results, got %+v", res)
			}
		})
	}
}

// TestLoadConfigMatchesInCodeConfig proves both construction paths FR-003 promises
// produce the same Config for the fields Run consumes: LoadConfig(mentat.yaml) and
// an in-code Config literal.
func TestLoadConfigMatchesInCodeConfig(t *testing.T) {
	dir := t.TempDir()
	yaml := "store: file\n" +
		"storePath: " + dir + "\n" +
		"targets:\n" +
		"  bot:\n" +
		"    adapter: shell\n" +
		"    command: [\"sh\", \"-c\", \"echo hi\"]\n" +
		"    max_concurrency: 1\n"
	path := writeFile(t, dir, "mentat.yaml", yaml)

	loaded, err := mentat.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	inCode := mentat.Config{Store: "file", StorePath: dir, Targets: echoTarget()}

	// LoadConfig applies defaults (Budget, judge, searchLimit) an in-code literal
	// omits, so compare the fields Run actually consumes, not the whole struct.
	if loaded.Store != inCode.Store {
		t.Fatalf("Store: loaded %q, in-code %q", loaded.Store, inCode.Store)
	}
	if loaded.StorePath != inCode.StorePath {
		t.Fatalf("StorePath: loaded %q, in-code %q", loaded.StorePath, inCode.StorePath)
	}
	lb, ib := loaded.Targets["bot"], inCode.Targets["bot"]
	if lb.Adapter != ib.Adapter {
		t.Fatalf("target adapter: loaded %q, in-code %q", lb.Adapter, ib.Adapter)
	}
	if !slices.Equal(lb.Command, ib.Command) {
		t.Fatalf("target command: loaded %v, in-code %v", lb.Command, ib.Command)
	}
	if lb.MaxConcurrency != ib.MaxConcurrency {
		t.Fatalf("target max_concurrency: loaded %d, in-code %d", lb.MaxConcurrency, ib.MaxConcurrency)
	}
}

// bus is a hermetic in-memory trace exchange shared between a custom driver and a
// custom store. It is the crux of the T004/T005 registration proof: the driver
// writes a forest keyed by the injected spec.RunID; the store serves that same id
// back to the correlator, so a fully custom driver+store pair yields a GREEN run
// under the default UUID correlator. This prototypes the examples/kafkaecho module.
// The mutex keeps the map race-free under -race (the correlator fetches in a
// goroutine).
type bus struct {
	mu     sync.Mutex
	traces map[string]*mentat.Trace
}

func newBus() *bus { return &bus{traces: map[string]*mentat.Trace{}} }

func (b *bus) put(id string, tr *mentat.Trace) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.traces[id] = tr
}

func (b *bus) get(id string) (*mentat.Trace, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	tr, ok := b.traces[id]
	return tr, ok
}

// busDriver is a custom SUT-driving seam implemented against the facade alone. It
// writes a minimal 1-root-span forest keyed by the injected run id and returns the
// run's answer as its Output.
type busDriver struct {
	bus    *bus
	answer string
}

func (d busDriver) Run(_ context.Context, spec mentat.RunSpec) (mentat.RunResult, error) {
	root := &mentat.Span{ID: "root", Name: "membus.run", Kind: mentat.KindServer, Status: mentat.StatusOk}
	d.bus.put(spec.RunID, &mentat.Trace{RunID: spec.RunID, Roots: []*mentat.Span{root}, Spans: []*mentat.Span{root}})
	return mentat.RunResult{RunID: spec.RunID, Output: mentat.Output{Answer: d.answer}}, nil
}

// busStore is a custom trace-backend seam serving the forest the busDriver wrote,
// keyed on the same run id. FetchPayload is deterministic (byte-identical per id)
// so the stability poll converges in two rounds; a missing id is a hard error, not
// (nil, nil) (Constitution IV).
type busStore struct{ bus *bus }

func (s busStore) FetchPayload(_ context.Context, id string) ([]byte, error) {
	if _, ok := s.bus.get(id); !ok {
		return nil, fmt.Errorf("busStore: no trace for id %q", id)
	}
	return []byte("membus:" + id), nil
}

func (s busStore) DecodePayload(id string, _ []byte) (*mentat.Trace, error) {
	tr, ok := s.bus.get(id)
	if !ok {
		return nil, fmt.Errorf("busStore: decode: no trace for id %q", id)
	}
	return tr, nil
}

func (s busStore) Query(_ context.Context, q mentat.TraceQuery) ([]mentat.TraceRef, error) {
	if _, ok := s.bus.get(q.Value); !ok {
		return nil, nil
	}
	return []mentat.TraceRef{{TraceID: q.Value}}, nil
}

func (s busStore) Caps() mentat.StoreCaps { return mentat.StoreCaps{} }

// --- Parallelism note for the Run tests below ---------------------------------
//
// The Run tests in this file call t.Parallel(). That is verified, not assumed:
//
//   - Seam registration is PER-ENGINE, not package-global: engine.Build mints a
//     fresh registry.Registry per call and Seal()s it, so two Runs never share
//     driver/store/comparator/judge state (spec 007 T010/T011). Reusing one custom
//     name across CONCURRENT Runs is proven green by TestRunConcurrentIndependent
//     in mentat_run_reentrancy_test.go.
//   - The only package-global Run writes is the POST-run reporter map, via
//     engine.Build → report.RegisterBuiltins. That write is idempotent (three fixed
//     reporters under three fixed keys) and guarded by registry's own reporterMu, so
//     concurrent Builds neither race nor disagree on a value.
//   - Each test owns its bus (newBus) and its fixture dir (t.TempDir); no test here
//     touches shared mutable package state or a shared path.
//   - No test here uses t.Setenv or t.Chdir — those panic under t.Parallel().
//   - Run narrates to io.Discard unless WithOutput is passed, and no test in this
//     package captures os.Stdout.
//
// The payoff is -race pressure on the registry-isolation invariant these tests
// guard, NOT wall clock: each run is ~0.00s (CLAUDE.md — t.Parallel is a soft
// correctness default for unit tests, not a CI-speed measure).

// TestRunCustomDriverAndStoreGreen is the KEY proof of the whole registration MVP:
// a custom driver (registered via WithDriver) writes its trace keyed by the injected
// spec.RunID into a shared bus, a custom store (WithStore) serves that same id, and
// the default UUID correlator resolves it — so a fully custom driver+store pair
// drives a scenario GREEN end to end. No built-in adapter, no Tempo, no network.
func TestRunCustomDriverAndStoreGreen(t *testing.T) {
	t.Parallel()
	b := newBus()
	dir := t.TempDir()
	feature := `Feature: custom driver and store
  Scenario: a custom driver writes a trace a custom store serves
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result contains "membus ok"
`
	featPath := writeFile(t, dir, "membus.feature", feature)

	cfg := mentat.Config{
		Store: "membus",
		Targets: map[string]mentat.Target{
			"bot": {Adapter: "membus", Command: []string{"noop"}, MaxConcurrency: 1},
		},
		Poll: mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}

	res, err := mentat.Run(context.Background(), cfg,
		mentat.WithFeatures(featPath),
		mentat.WithDriver("membus", func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "membus ok"}, nil
		}),
		mentat.WithStore("membus", func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
	)
	if err != nil {
		t.Fatalf("Run returned a harness error (a custom driver+store green run must not error): %v", err)
	}
	if res.Passed != 1 || res.Failed != 0 {
		t.Fatalf("tally passed=%d failed=%d, want 1/0; scenarios=%+v", res.Passed, res.Failed, res.Scenarios)
	}
	if len(res.Scenarios) != 1 || !res.Scenarios[0].Pass {
		t.Fatalf("custom driver+store scenario did not pass green: %+v", res.Scenarios)
	}
	if len(res.Scenarios[0].RunIDs) == 0 || res.Scenarios[0].RunIDs[0] == "" {
		t.Fatalf("green scenario must carry the injected run id, got %v", res.Scenarios[0].RunIDs)
	}
}

// TestRunScenarioResultCarriesFeatureFile proves a library consumer can tell which
// .feature file each scenario came from (US2, FR-003): ScenarioResult.FeatureFile is
// the source feature path, captured from godog's scenario Uri through the report
// collector — Name alone can collide across files.
func TestRunScenarioResultCarriesFeatureFile(t *testing.T) {
	t.Parallel()
	b := newBus()
	dir := t.TempDir()
	feature := `Feature: feature-file field
  Scenario: a scenario reports its source file
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result contains "ff ok"
`
	featPath := writeFile(t, dir, "source.feature", feature)
	cfg := mentat.Config{
		Store: "membus",
		Targets: map[string]mentat.Target{
			"bot": {Adapter: "membus", Command: []string{"noop"}, MaxConcurrency: 1},
		},
		Poll: mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	res, err := mentat.Run(context.Background(), cfg,
		mentat.WithFeatures(featPath),
		mentat.WithDriver("membus", func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "ff ok"}, nil
		}),
		mentat.WithStore("membus", func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Scenarios) != 1 {
		t.Fatalf("want 1 scenario, got %d", len(res.Scenarios))
	}
	ff := res.Scenarios[0].FeatureFile
	if ff == "" {
		t.Fatal("ScenarioResult.FeatureFile is empty; want the source .feature path")
	}
	if !strings.HasSuffix(ff, "source.feature") {
		t.Fatalf("FeatureFile %q should name the source feature file", ff)
	}
}

// TestRunCustomComparatorAndJudgeCompose proves WithComparator and WithJudge are
// consumed at the composition root without error, alongside a custom driver+store,
// yielding a GREEN run. The custom judge is selected by cfg.Judge.Backend, so Build
// actually RESOLVES it (a path that would fail "unknown judge backend" without the
// judge funneling) — proving the seam is wired, not merely accepted.
//
// The custom comparator is registered and composes, but is NOT invoked from a
// feature step: the built-in Gherkin grammar maps steps onto the built-in comparator
// names only, and first-class custom-comparator steps are planned as spec 010 (not
// started yet) — out of 007's registration-surface scope. This test therefore asserts
// registration/composition success for the comparator, and behavioural resolution
// for the judge.
func TestRunCustomComparatorAndJudgeCompose(t *testing.T) {
	t.Parallel()
	b := newBus()
	dir := t.TempDir()
	feature := `Feature: custom comparator and judge compose
  Scenario: a run composes with a custom comparator and judge registered
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result contains "cbus ok"
`
	featPath := writeFile(t, dir, "cbus.feature", feature)

	cfg := mentat.Config{
		Store: "cbus",
		Targets: map[string]mentat.Target{
			"bot": {Adapter: "cbus", Command: []string{"noop"}, MaxConcurrency: 1},
		},
		Poll:  mentat.PollSpec{Interval: "1ms", StableFor: 1},
		Judge: mentat.JudgeConfig{Backend: "cbusjudge"},
	}

	res, err := mentat.Run(context.Background(), cfg,
		mentat.WithFeatures(featPath),
		mentat.WithDriver("cbus", func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "cbus ok"}, nil
		}),
		mentat.WithStore("cbus", func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
		mentat.WithComparator("cbuscomp", func(mentat.Config) (mentat.Comparator, error) {
			return toyComparator{}, nil
		}),
		mentat.WithJudge("cbusjudge", func(mentat.Config) (mentat.Judge, error) {
			return toyJudge{}, nil
		}),
	)
	if err != nil {
		t.Fatalf("Run must compose with a custom comparator and judge without error: %v", err)
	}
	if res.Passed != 1 || res.Failed != 0 {
		t.Fatalf("tally passed=%d failed=%d, want 1/0; scenarios=%+v", res.Passed, res.Failed, res.Scenarios)
	}
	if len(res.Scenarios) != 1 || !res.Scenarios[0].Pass {
		t.Fatalf("composed scenario did not pass green: %+v", res.Scenarios)
	}
}

// TestRunDuplicateRegistrationFailsLoudly proves FR-002 / US1 acceptance #3: a
// custom adapter name that collides with a built-in OR with an earlier registration
// is a loud Run error naming the seam and the conflicting name — never a silent
// last-wins overwrite (Constitution IV). All four registrable seams are covered,
// each for both collision shapes (vs built-in, and dup vs dup).
//
// The subtests are parallel too: they only READ the shared dir/featPath and the four
// stateless ok* factories, and every collision is contained in its own Run's registry.
func TestRunDuplicateRegistrationFailsLoudly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	featPath := writeFile(t, dir, "dup.feature", "Feature: x\n  Scenario: y\n    Given the agent target \"bot\"\n")
	okDriver := func(mentat.Config) (mentat.Driver, error) { return busDriver{}, nil }
	okStore := func(mentat.Config) (mentat.TraceStore, error) { return busStore{}, nil }
	okComparator := func(mentat.Config) (mentat.Comparator, error) { return toyComparator{}, nil }
	okJudge := func(mentat.Config) (mentat.Judge, error) { return toyJudge{}, nil }

	tests := []struct {
		name    string
		opts    []mentat.Option
		wantSub []string
	}{
		{
			name:    "driver vs built-in",
			opts:    []mentat.Option{mentat.WithDriver("shell", okDriver)},
			wantSub: []string{"WithDriver", "shell"},
		},
		{
			name:    "driver dup vs dup",
			opts:    []mentat.Option{mentat.WithDriver("dupdrv", okDriver), mentat.WithDriver("dupdrv", okDriver)},
			wantSub: []string{"WithDriver", "dupdrv"},
		},
		{
			name:    "store vs built-in",
			opts:    []mentat.Option{mentat.WithStore("file", okStore)},
			wantSub: []string{"WithStore", "file"},
		},
		{
			name:    "store dup vs dup",
			opts:    []mentat.Option{mentat.WithStore("dupstore", okStore), mentat.WithStore("dupstore", okStore)},
			wantSub: []string{"WithStore", "dupstore"},
		},
		{
			name:    "comparator vs built-in",
			opts:    []mentat.Option{mentat.WithComparator("result", okComparator)},
			wantSub: []string{"WithComparator", "result"},
		},
		{
			name:    "comparator dup vs dup",
			opts:    []mentat.Option{mentat.WithComparator("dupcmp", okComparator), mentat.WithComparator("dupcmp", okComparator)},
			wantSub: []string{"WithComparator", "dupcmp"},
		},
		{
			name:    "judge vs built-in",
			opts:    []mentat.Option{mentat.WithJudge("claude", okJudge)},
			wantSub: []string{"WithJudge", "claude"},
		},
		{
			name:    "judge dup vs dup",
			opts:    []mentat.Option{mentat.WithJudge("dupjudge", okJudge), mentat.WithJudge("dupjudge", okJudge)},
			wantSub: []string{"WithJudge", "dupjudge"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := mentat.Config{Store: "file", StorePath: dir, Targets: echoTarget()}
			opts := append([]mentat.Option{mentat.WithFeatures(featPath)}, tt.opts...)
			res, err := mentat.Run(context.Background(), cfg, opts...)
			if err == nil {
				t.Fatalf("duplicate %s registration must error, got results %+v", tt.name, res)
			}
			for _, sub := range tt.wantSub {
				if !strings.Contains(err.Error(), sub) {
					t.Fatalf("collision error should name %q, got %q", sub, err.Error())
				}
			}
			if len(res.Scenarios) != 0 || res.Passed != 0 || res.Failed != 0 {
				t.Fatalf("a collision must return empty Results, got %+v", res)
			}
		})
	}
}

// TestRunGoesRedOnComparatorViolation is the mandatory L3 meta-test (constitution
// Principle V, NON-NEGOTIABLE): a scenario that VIOLATES its comparator, driven
// through the PUBLIC mentat.Run surface, must go RED — Results.Failed>0, the scenario
// Pass==false carrying a non-empty Reasons, and Results.ExitCode()==1 (the CLI's FAIL
// exit). This proves the library entry point fails loudly on bad behaviour, not only
// on cancellation; a framework that cannot be shown to go red on a violation is not
// proven to work.
//
// The custom driver+store resolve the trace GREEN, so the run reaches the comparator;
// the comparator then fails because the scenario asserts a substring the driver's
// answer ("meta ok") never contains.
func TestRunGoesRedOnComparatorViolation(t *testing.T) {
	t.Parallel()
	b := newBus()
	dir := t.TempDir()
	feature := `Feature: comparator violation goes red
  Scenario: asserting an answer the driver never returns fails
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result contains "ANSWER-NEVER-RETURNED"
`
	featPath := writeFile(t, dir, "violation.feature", feature)
	cfg := mentat.Config{
		Store: "membus",
		Targets: map[string]mentat.Target{
			"bot": {Adapter: "membus", Command: []string{"noop"}, MaxConcurrency: 1},
		},
		Poll: mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	res, err := mentat.Run(context.Background(), cfg,
		mentat.WithFeatures(featPath),
		mentat.WithDriver("membus", func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "meta ok"}, nil
		}),
		mentat.WithStore("membus", func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
	)
	if err != nil {
		t.Fatalf("a comparator violation is not a harness error; Run must reflect it in Results: %v", err)
	}
	if res.Failed == 0 {
		t.Fatalf("a violating scenario must fail the suite; got %+v", res)
	}
	if len(res.Scenarios) != 1 || res.Scenarios[0].Pass {
		t.Fatalf("the violating scenario must not pass: %+v", res.Scenarios)
	}
	if len(res.Scenarios[0].Reasons) == 0 {
		t.Fatal("a failed scenario must carry a non-empty reason")
	}
	// Prove the redness is the COMPARATOR verdict, not a resolve/harness error: the
	// reason must name the substring the driver never returns. A resolve miss would
	// carry a "no trace"/"no fixture" reason and satisfy the non-empty check above —
	// this assertion is what makes the L3 meta-test bite (the comparator, not the
	// harness, is what failed).
	if joined := strings.Join(res.Scenarios[0].Reasons, " "); !strings.Contains(joined, "ANSWER-NEVER-RETURNED") {
		t.Fatalf("reason must name the asserted-but-absent substring (comparator verdict, not a resolve error), got %q", joined)
	}
	if got := res.ExitCode(); got != 1 {
		t.Fatalf("a red suite must map to exit 1 (CLI FAIL), got %d", got)
	}
}

// TestLoadConfigErrors proves LoadConfig names the path on both failure modes — an
// unreadable file and a malformed document — never a silent zero-value Config
// (Constitution IV).
func TestLoadConfigErrors(t *testing.T) {
	dir := t.TempDir()
	// Unknown key `nope` trips the strict-decode path in config.Load.
	badPath := writeFile(t, dir, "bad.yaml", "store: file\nstorePath: "+dir+"\nnope: 1\n")
	tests := []struct {
		name string
		path string
	}{
		{name: "missing file", path: filepath.Join(dir, "does-not-exist.yaml")},
		{name: "malformed document", path: badPath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := mentat.LoadConfig(tt.path)
			if err == nil {
				t.Fatal("LoadConfig must error")
			}
			if !strings.Contains(err.Error(), tt.path) {
				t.Fatalf("error should name the path %q, got %q", tt.path, err.Error())
			}
		})
	}
}
