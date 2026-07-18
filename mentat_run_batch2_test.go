// Package mentat_test — Batch-2 option proofs for the library entry point
// (spec 007, T012 chain): WithVerbosity (narration), WithReports (report files),
// and in-Run judge-budget enforcement. These grow mentat.Run so the CLI
// ("consumer zero", research R7) can ride it for verbosity, reports and budget.
//
// The bus/busDriver/busStore harness, runMembusFeature, echoTarget, newBus and
// writeFile live in mentat_run_test.go / mentat_run_options_test.go — same package.
package mentat_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/thetonymaster/mentat"
)

// TestWithVerbosity proves WithVerbosity narrates the run to the given writer, and
// that the library default (no WithVerbosity) narrates nothing — the logger stays a
// silent discard handler, so a caller's buffer is untouched. A nil writer behaves as
// the silent default (Constitution IV: a zero value never panics).
func TestWithVerbosity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		regName     string
		withVerbose bool
		nilWriter   bool
		wantEmpty   bool
	}{
		{name: "verbose+debug narrates to the writer", regName: "verb-on", withVerbose: true, wantEmpty: false},
		{name: "default is silent", regName: "verb-off", withVerbose: false, wantEmpty: true},
		{name: "nil writer is the silent default", regName: "verb-nil", withVerbose: true, nilWriter: true, wantEmpty: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			answer := tt.regName + " ok"
			feature := fmt.Sprintf(`Feature: verbosity option
  Scenario: a narrated run
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result contains %q
`, answer)
			var extra []mentat.Option
			if tt.withVerbose {
				if tt.nilWriter {
					extra = append(extra, mentat.WithVerbosity(nil, true, true))
				} else {
					extra = append(extra, mentat.WithVerbosity(&buf, true, true))
				}
			}
			res, err := runMembusFeature(t, tt.regName, answer, feature, extra...)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if res.Passed != 1 || res.Failed != 0 {
				t.Fatalf("want a single green scenario, got %+v", res)
			}
			if tt.wantEmpty {
				if buf.Len() != 0 {
					t.Fatalf("silent default must narrate nothing, got:\n%s", buf.String())
				}
				return
			}
			out := buf.String()
			if !strings.Contains(out, "resolve") {
				t.Fatalf("verbose output must contain a narration marker (resolve.*), got:\n%s", out)
			}
		})
	}
}

// TestWithReports proves WithReports emits the requested report files after a green
// run, and that an unwritable target is a Run error PRESERVING the EmitReports wording
// while the returned Results still reflect the completed run — only emission failed,
// never a silent fallback (Constitution IV).
func TestWithReports(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		regName      string
		buildTargets func(dir string) map[string]string
		wantErrSub   string
	}{
		{
			name:    "writes json and junit on a green run",
			regName: "rep-ok",
			buildTargets: func(dir string) map[string]string {
				return map[string]string{
					"json":  filepath.Join(dir, "report.json"),
					"junit": filepath.Join(dir, "report.xml"),
				}
			},
		},
		{
			name:    "missing parent dir errors but Results survive",
			regName: "rep-baddir",
			buildTargets: func(dir string) map[string]string {
				return map[string]string{"junit": filepath.Join(dir, "no-such-dir", "report.xml")}
			},
			wantErrSub: "writing junit report",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			targets := tt.buildTargets(dir)
			answer := tt.regName + " ok"
			feature := fmt.Sprintf(`Feature: reports option
  Scenario: a run that emits reports
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result contains %q
`, answer)
			res, err := runMembusFeature(t, tt.regName, answer, feature, mentat.WithReports(targets))

			if tt.wantErrSub != "" {
				if err == nil {
					t.Fatalf("want a report-emission error, got nil (res=%+v)", res)
				}
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("error should preserve the EmitReports wording %q, got %q", tt.wantErrSub, err.Error())
				}
				// The run still completed: only emission failed (Constitution IV).
				if res.Passed < 1 {
					t.Fatalf("Results must reflect the completed run (Passed>=1), got %+v", res)
				}
				return
			}

			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if res.Passed != 1 || res.Failed != 0 {
				t.Fatalf("want a single green scenario, got %+v", res)
			}
			jsonData, jerr := os.ReadFile(targets["json"])
			if jerr != nil {
				t.Fatalf("json report not written: %v", jerr)
			}
			if len(jsonData) == 0 {
				t.Fatal("json report is empty")
			}
			junitData, uerr := os.ReadFile(targets["junit"])
			if uerr != nil {
				t.Fatalf("junit report not written: %v", uerr)
			}
			if !strings.Contains(string(junitData), "<testsuite") {
				t.Fatalf("junit report missing <testsuite marker, got:\n%s", junitData)
			}
		})
	}
}

// budgetJudge is a stub judge that always matches and reports a fixed token usage, so
// its priced cost is deterministic — enough to cross a low MaxCostUSD ceiling. It is
// implemented against the facade aliases only (Judge/JudgeRequest/JudgeVerdict/JudgeUsage).
type budgetJudge struct{ usage mentat.JudgeUsage }

func (j budgetJudge) Judge(_ context.Context, _ mentat.JudgeRequest) (mentat.JudgeVerdict, error) {
	return mentat.JudgeVerdict{Match: true, Reason: "budget judge match", Usage: j.usage}, nil
}

// runJudgeBudget drives one hermetic judged scenario (the semantic `the result means`
// step) through mentat.Run: a busDriver+busStore resolve the trace green, then the
// registered budgetJudge is invoked and priced against cfg.Pricing. maxUSD is the
// Judge.MaxCostUSD ceiling — the fixed 2000 input tokens at $10/MTok price to $0.02.
// extra options are appended after the driver/store/judge registrations, so a caller
// can add e.g. WithReports to exercise the emit path alongside the budget check.
func runJudgeBudget(t *testing.T, regName string, maxUSD float64, extra ...mentat.Option) (mentat.Results, error) {
	t.Helper()
	b := newBus()
	dir := t.TempDir()
	feature := `Feature: judge budget
  Scenario: a judged run
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result means "the answer"
`
	featPath := writeFile(t, dir, "budget.feature", feature)
	cfg := mentat.Config{
		Store: regName,
		Targets: map[string]mentat.Target{
			"bot": {Adapter: regName, Command: []string{"noop"}, MaxConcurrency: 1},
		},
		Poll:    mentat.PollSpec{Interval: "1ms", StableFor: 1},
		Pricing: mentat.Pricing{"judge-model": {InputPerMTok: 10}}, // 2000 in tok => $0.02
		Judge:   mentat.JudgeConfig{Backend: regName, Votes: 1, MaxCostUSD: maxUSD},
	}
	opts := []mentat.Option{
		mentat.WithFeatures(featPath),
		mentat.WithDriver(regName, func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "budget ok"}, nil
		}),
		mentat.WithStore(regName, func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
		mentat.WithJudge(regName, func(mentat.Config) (mentat.Judge, error) {
			return budgetJudge{usage: mentat.JudgeUsage{Calls: 1, InputTokens: 2000, OutputTokens: 0, Model: "judge-model"}}, nil
		}),
	}
	opts = append(opts, extra...)
	return mentat.Run(context.Background(), cfg, opts...)
}

// TestRunSurfacesEmitAndBudgetErrorsTogether proves that when a run BOTH fails to write
// a requested report AND trips the judge budget, Run surfaces BOTH errors (joined) — the
// emit failure must not mask the budget trip, and vice versa. The pre-recompose CLI
// printed both; the recomposed Run must not early-return on the emit error before reading
// budget.Err(). Each individual message keeps its original wrapping (so the emit-only and
// budget-only tests still match), and errors.Join carries both here.
//
// No t.Parallel(): reuses the shared runJudgeBudget harness (registry mutation).
func TestRunSurfacesEmitAndBudgetErrorsTogether(t *testing.T) {
	// A junit target whose parent dir is absent forces EmitReports to fail; the low
	// $0.01 ceiling against the fixed $0.02 spend trips the budget in the same run.
	badJunit := filepath.Join(t.TempDir(), "no-such-dir", "report.xml")
	res, err := runJudgeBudget(t, "budget-and-emit", 0.01,
		mentat.WithReports(map[string]string{"junit": badJunit}))

	if err == nil {
		t.Fatalf("a run that both fails to emit and trips the budget must error, got nil (res=%+v)", res)
	}
	msg := err.Error()
	// Both concerns must be named — neither masks the other.
	for _, want := range []string{"writing junit report", "judge budget"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("joined error must surface %q, got %q", want, msg)
		}
	}
	// The run still completed: Results reflect the scenario that ran (Constitution IV).
	if len(res.Scenarios) < 1 {
		t.Fatalf("both-error run must still return the scenarios that ran, got %+v", res)
	}
	// A budget trip is not an interruption: the passed ctx was never cancelled.
	if res.Interrupted {
		t.Fatalf("a budget trip must NOT mark the run interrupted, got %+v", res)
	}
}

// countingBudgetJudge counts its invocations via an atomic counter (so the test is
// -race clean) and reports a fixed token usage whose priced cost crosses a low
// MaxCostUSD ceiling. It is the instrument for proving the budget CANCELLATION
// prevents a subsequent scenario's judge call — not merely that budget.Err() is set
// after the run completes.
type countingBudgetJudge struct {
	calls *int64
	usage mentat.JudgeUsage
}

func (j countingBudgetJudge) Judge(_ context.Context, _ mentat.JudgeRequest) (mentat.JudgeVerdict, error) {
	atomic.AddInt64(j.calls, 1)
	return mentat.JudgeVerdict{Match: true, Reason: "counting judge match", Usage: j.usage}, nil
}

// runJudgeBudgetTwoScenarios drives TWO judged scenarios (concurrency 1) through
// mentat.Run with a counting judge, returning the number of judge calls actually made.
// Scenario 1's fixed $0.02 spend crosses the low maxUSD ceiling; its After hook then
// cancels the budget context so scenario 2's drive step aborts before any judge call.
// Both scenarios use the identical judged `the result means` step, so the ONLY thing
// keeping scenario 2's judge from firing is the budget cancellation under test.
func runJudgeBudgetTwoScenarios(t *testing.T, regName string, maxUSD float64) (mentat.Results, int64, error) {
	t.Helper()
	b := newBus()
	dir := t.TempDir()
	feature := `Feature: judge budget cancellation stops the next scenario
  Scenario: first judged run crosses the ceiling
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result means "the answer"
  Scenario: second judged run must be prevented
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result means "the answer"
`
	featPath := writeFile(t, dir, "budget2.feature", feature)
	var calls int64
	cfg := mentat.Config{
		Store: regName,
		Targets: map[string]mentat.Target{
			"bot": {Adapter: regName, Command: []string{"noop"}, MaxConcurrency: 1},
		},
		Poll:    mentat.PollSpec{Interval: "1ms", StableFor: 1},
		Pricing: mentat.Pricing{"judge-model": {InputPerMTok: 10}}, // 2000 in tok => $0.02
		Judge:   mentat.JudgeConfig{Backend: regName, Votes: 1, MaxCostUSD: maxUSD},
	}
	res, err := mentat.Run(context.Background(), cfg,
		mentat.WithFeatures(featPath),
		mentat.WithDriver(regName, func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "budget ok"}, nil
		}),
		mentat.WithStore(regName, func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
		mentat.WithJudge(regName, func(mentat.Config) (mentat.Judge, error) {
			return countingBudgetJudge{calls: &calls, usage: mentat.JudgeUsage{Calls: 1, InputTokens: 2000, OutputTokens: 0, Model: "judge-model"}}, nil
		}),
	)
	return res, atomic.LoadInt64(&calls), err
}

// TestRunBudgetCancellationStopsNextScenario pins the budget CANCELLATION wiring (US6)
// with teeth the single-scenario TestRunEnforcesJudgeBudget lacks: that test drives
// ONE judged scenario, so it stays green even if budgetCancel were never wired
// (budget.Err() is set post-run regardless). Here two identical judged scenarios run
// at concurrency 1; scenario 1's $0.02 spend crosses the $0.01 ceiling and its After
// hook cancels the budget context, so scenario 2's drive step aborts before its judge
// call — the counting judge must be invoked EXACTLY ONCE. Without the cancellation,
// scenario 2 would drive and judge too (count == 2).
//
// No t.Parallel(): reuses the shared registry-mutating Run path.
func TestRunBudgetCancellationStopsNextScenario(t *testing.T) {
	res, calls, err := runJudgeBudgetTwoScenarios(t, "budget-cancel-next", 0.01)

	if err == nil {
		t.Fatalf("an over-budget run must error, got nil (res=%+v)", res)
	}
	if !strings.Contains(err.Error(), "judge budget") {
		t.Fatalf("error should name the budget trip, got %q", err.Error())
	}
	if calls != 1 {
		t.Fatalf("budget cancellation must prevent scenario 2's judge call: judge invoked %d times, want exactly 1", calls)
	}
	// A budget trip is not an interruption: the passed ctx was never cancelled, only
	// the internal budget child context.
	if res.Interrupted {
		t.Fatalf("a budget trip must NOT mark the run interrupted, got %+v", res)
	}
}

// TestRunEnforcesJudgeBudget proves Run enforces the judge-spend ceiling (US6): a
// scenario whose priced judge cost ($0.02) crosses a low MaxCostUSD ($0.01) makes Run
// return a wrapped "judge budget" error naming the spend and ceiling, while Results
// still carry the scenario that ran and Interrupted stays false (a budget trip is NOT
// an interruption — the passed ctx was never cancelled). The negative row proves an
// unlimited ceiling (MaxCostUSD=0) never trips: byte-identical to today's behaviour.
func TestRunEnforcesJudgeBudget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		regName    string
		maxUSD     float64
		wantErrSub string
	}{
		{name: "spend over ceiling trips the budget", regName: "budget-trip", maxUSD: 0.01, wantErrSub: "judge budget"},
		{name: "unlimited ceiling never trips", regName: "budget-off", maxUSD: 0, wantErrSub: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := runJudgeBudget(t, tt.regName, tt.maxUSD)

			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("unlimited budget must not error: %v", err)
				}
				if res.Passed != 1 || res.Failed != 0 {
					t.Fatalf("want the judged scenario green, got %+v", res)
				}
				return
			}

			if err == nil {
				t.Fatalf("over-budget run must error, got nil (res=%+v)", res)
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("error should name the budget trip %q, got %q", tt.wantErrSub, err.Error())
			}
			// The wrapped Budget message should carry the spend/ceiling figures.
			for _, fig := range []string{"0.0200", "0.0100"} {
				if !strings.Contains(err.Error(), fig) {
					t.Fatalf("budget error should name the spend/ceiling figure %q, got %q", fig, err.Error())
				}
			}
			// Results still carry the scenario that ran and crossed the budget.
			if len(res.Scenarios) < 1 {
				t.Fatalf("budget error must still return the scenarios that ran, got %+v", res)
			}
			// A budget trip is not an interruption: the passed ctx was never cancelled.
			if res.Interrupted {
				t.Fatalf("a budget trip must NOT mark the run interrupted, got %+v", res)
			}
		})
	}
}
