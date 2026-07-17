package steps

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/cucumber/godog"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/registry"
	"github.com/thetonymaster/mentat/internal/report"
)

// usageJudgeEng builds a real engine whose judge backend is a gomock MockJudge
// returning a fixed match verdict AND the given fixed token usage on its single
// expected call (votes=1), so the ledger-threading tests can assert the usage flows
// from the semantic matcher through the After hook and report.Derive into the
// collector. Per CLAUDE.md the core Judge interface is mocked with uber gomock (not a
// hand fake) because the call count matters: Times(1) proves the semantic check
// issues exactly one judge call — and, for the budget test, that the aborted second
// scenario starts none. Hermetic: mock store serving happyTrace, shell target echoing
// an answer the judge ignores. Registers into the global judge registry, so callers
// must be serial.
func usageJudgeEng(t *testing.T, u core.JudgeUsage) *engine.Engine {
	t.Helper()
	registry.ResetForTest(t)
	ctrl := gomock.NewController(t)
	j := mocks.NewMockJudge(ctrl)
	j.EXPECT().Judge(gomock.Any(), gomock.Any()).
		Return(core.JudgeVerdict{Match: true, Reason: "usage judge match", Usage: u}, nil).Times(1)
	registry.RegisterJudge("fake-usage", func(config.Config) (core.Judge, error) {
		return j, nil
	})
	cfg := config.Config{
		OTLPEndpoint: "x",
		Judge:        config.JudgeConfig{Backend: "fake-usage", Votes: 1},
		Targets: map[string]config.Target{
			"svc": {Adapter: "shell", Command: []string{"sh", "-c", "echo answer"}, MaxConcurrency: 1},
		},
	}
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "r"}}, nil).AnyTimes()
	stubStoredTrace(st, happyTrace())
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}
	return eng
}

// TestInitializer_CollectsJudgeUsage proves the semantic matcher's per-verdict judge
// usage threads all the way to the ScenarioResult: check() records Verdict.Judge, the
// After hook folds it into the scenario Verdict, and report.Derive carries it into
// ScenarioResult.Judge. The collector then sums the suite JudgeTotal (US6, FR-006).
func TestInitializer_CollectsJudgeUsage(t *testing.T) {
	eng := usageJudgeEng(t, core.JudgeUsage{Calls: 1, InputTokens: 2000, OutputTokens: 100, Model: "judge-model"})
	col := report.NewCollector()

	feature := `Feature: judge-ledger
  Scenario: means with usage
    Given the agent target "svc"
    When I run scenario "x"
    Then the result means "the answer"
`
	if status := runLedgerSuite(t, InitializerWithCollector(eng, col), feature); status != 0 {
		t.Fatalf("expected passing suite, status=%d", status)
	}

	rep := col.Report(time.Unix(0, 0), 0, false)
	if len(rep.Scenarios) != 1 {
		t.Fatalf("got %d scenarios, want 1", len(rep.Scenarios))
	}
	sr := rep.Scenarios[0]
	if sr.Judge == nil {
		t.Fatal("ScenarioResult.Judge is nil; want the judge usage threaded from the matcher")
	}
	if sr.Judge.Calls != 1 || sr.Judge.InputTokens != 2000 || sr.Judge.OutputTokens != 100 {
		t.Errorf("ScenarioResult.Judge = %+v, want calls=1 in=2000 out=100", *sr.Judge)
	}
	if sr.Judge.Model != "judge-model" {
		t.Errorf("ScenarioResult.Judge.Model = %q, want judge-model", sr.Judge.Model)
	}
	if rep.JudgeTotal == nil || rep.JudgeTotal.Calls != 1 {
		t.Fatalf("JudgeTotal = %+v, want a non-nil total with 1 call", rep.JudgeTotal)
	}
}

// TestInitializer_BudgetAbortsAndStillCollects proves the post-scenario budget check
// wired into the After hook: a 1-cent ceiling that the first scenario's judge spend
// crosses trips the budget (naming spent/budget/scenario), invokes the abort handle
// so the suite context is cancelled (no NEW judge call starts — the second scenario
// fails fast at drive), and the completed scenario's judge ledger still survives in
// the collector so reports still emit with the ledger.
func TestInitializer_BudgetAbortsAndStillCollects(t *testing.T) {
	eng := usageJudgeEng(t, core.JudgeUsage{Calls: 1, InputTokens: 2000, OutputTokens: 0, Model: "judge-model"})
	col := report.NewCollector()
	pricing := core.Pricing{"judge-model": {InputPerMTok: 10}} // 2000 in tok => $0.02
	budget := report.NewBudget(0.01, pricing)                  // 1-cent ceiling
	abortCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	feature := `Feature: judge-budget
  Scenario: first crosses the budget
    Given the agent target "svc"
    When I run scenario "x"
    Then the result means "the answer"

  Scenario: second must not start a new judge call
    Given the agent target "svc"
    When I run scenario "y"
    Then the result means "the answer"
`
	suite := godog.TestSuite{
		ScenarioInitializer: InitializerWithBudget(eng, col, budget, cancel),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &testDiscard{},
			FeatureContents: []godog.Feature{{Name: "judge-budget", Contents: []byte(feature)}},
			DefaultContext:  abortCtx,
		},
	}
	_ = suite.Run()

	if budget.Err() == nil {
		t.Fatal("budget did not trip; want an over-budget error")
	}
	for _, want := range []string{"first crosses the budget", "0.0200", "0.0100"} {
		if !strings.Contains(budget.Err().Error(), want) {
			t.Errorf("budget error %q should name %q (spent/budget/scenario)", budget.Err(), want)
		}
	}
	if abortCtx.Err() == nil {
		t.Error("abort context not cancelled; the budget trip did not abort the suite")
	}

	// Reports still emit with the ledger: the completed scenario's usage survives, only
	// one judge call was accounted (no new call after the trip), and it prices cleanly.
	rep := col.Report(time.Unix(0, 0), 0, false)
	if rep.JudgeTotal == nil || rep.JudgeTotal.Calls != 1 {
		t.Fatalf("JudgeTotal = %+v, want exactly 1 completed judge call", rep.JudgeTotal)
	}
	if err := report.Price(&rep, pricing); err != nil {
		t.Fatalf("Price after abort: %v (report must still render with the ledger)", err)
	}
}

// runLedgerSuite runs a single inline feature against init and returns the exit status.
func runLedgerSuite(t *testing.T, init func(*godog.ScenarioContext), feature string) int {
	t.Helper()
	return godog.TestSuite{
		ScenarioInitializer: init,
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &testDiscard{},
			Strict:          true,
			FeatureContents: []godog.Feature{{Name: "judge-ledger", Contents: []byte(feature)}},
		},
	}.Run()
}

// testDiscard is an io.Writer sink for godog's pretty output in these tests.
type testDiscard struct{}

func (testDiscard) Write(p []byte) (int, error) { return len(p), nil }
