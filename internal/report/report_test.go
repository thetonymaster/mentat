package report

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

// judgePricing is a small pricing table used across the ledger-rendering tests.
// input 3 USD/Mtok, output 15 USD/Mtok → a 1000-in/100-out usage costs
// 1000/1e6*3 + 100/1e6*15 = 0.003 + 0.0015 = 0.0045.
var judgePricing = core.Pricing{"judge-model": {InputPerMTok: 3, OutputPerMTok: 15}}

const wantJudgeCost = 0.0045

// TestJSONRendersJudgeLedger proves the JSON report carries the per-scenario judge
// object AND the suite judgeTotal (judge-ledger contract): each present only where
// judge calls happened (a scenario that made no call has no judge key — no fabricated
// zeros), each priced via the pricing table, and the total omitting the model key.
func TestJSONRendersJudgeLedger(t *testing.T) {
	t.Parallel()

	rep := core.RunReport{
		Total: 2, Passed: 2,
		Scenarios: []core.ScenarioResult{
			{Name: "with-judge", Pass: true, Judge: &core.JudgeUsage{Calls: 3, InputTokens: 1000, OutputTokens: 100, Model: "judge-model"}},
			{Name: "no-judge", Pass: true},
		},
		JudgeTotal: &core.JudgeUsage{Calls: 3, InputTokens: 1000, OutputTokens: 100},
	}
	if err := Price(&rep, judgePricing); err != nil {
		t.Fatalf("Price: %v", err)
	}

	var buf bytes.Buffer
	if err := (jsonReporter{}).Report(rep, &buf); err != nil {
		t.Fatalf("Report: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("json: %v\n%s", err, buf.String())
	}
	scen, ok := m["Scenarios"].([]any)
	if !ok || len(scen) != 2 {
		t.Fatalf("Scenarios shape = %v", m["Scenarios"])
	}

	withJudge := scen[0].(map[string]any)
	j, ok := withJudge["judge"].(map[string]any)
	if !ok {
		t.Fatalf("with-judge scenario missing a judge object; got %v", withJudge)
	}
	if j["calls"].(float64) != 3 {
		t.Errorf("judge.calls = %v, want 3", j["calls"])
	}
	if j["inputTokens"].(float64) != 1000 {
		t.Errorf("judge.inputTokens = %v, want 1000", j["inputTokens"])
	}
	if j["outputTokens"].(float64) != 100 {
		t.Errorf("judge.outputTokens = %v, want 100", j["outputTokens"])
	}
	if j["model"] != "judge-model" {
		t.Errorf("judge.model = %v, want judge-model", j["model"])
	}
	if math.Abs(j["costUsd"].(float64)-wantJudgeCost) > 1e-9 {
		t.Errorf("judge.costUsd = %v, want ~%v", j["costUsd"], wantJudgeCost)
	}

	// Absence, not zeros: a scenario that made no judge call has no judge key.
	if _, present := scen[1].(map[string]any)["judge"]; present {
		t.Errorf("no-judge scenario fabricated a judge object")
	}

	tot, ok := m["judgeTotal"].(map[string]any)
	if !ok {
		t.Fatalf("missing judgeTotal; got %v", m)
	}
	if tot["calls"].(float64) != 3 {
		t.Errorf("judgeTotal.calls = %v, want 3", tot["calls"])
	}
	if math.Abs(tot["costUsd"].(float64)-wantJudgeCost) > 1e-9 {
		t.Errorf("judgeTotal.costUsd = %v, want ~%v", tot["costUsd"], wantJudgeCost)
	}
	if _, hasModel := tot["model"]; hasModel {
		t.Errorf("judgeTotal must omit the model key, got %v", tot)
	}
}

// TestJSONOmitsJudgeWhenNoCalls is the byte-stability guard: a run with no judge
// calls and no total must render exactly today's shape — no judge/judgeTotal keys.
func TestJSONOmitsJudgeWhenNoCalls(t *testing.T) {
	t.Parallel()
	rep := core.RunReport{Total: 1, Passed: 1, Scenarios: []core.ScenarioResult{{Name: "plain", Pass: true}}}
	var buf bytes.Buffer
	if err := (jsonReporter{}).Report(rep, &buf); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if strings.Contains(buf.String(), "judge") {
		t.Errorf("clean report must not mention judge, got:\n%s", buf.String())
	}
}

// TestHTMLRendersJudgeLedger proves the HTML report renders the same data: a
// per-scenario judge section and a suite judge-total row, present only when calls
// happened.
func TestHTMLRendersJudgeLedger(t *testing.T) {
	t.Parallel()

	rep := core.RunReport{
		Total: 1, Passed: 1,
		Scenarios: []core.ScenarioResult{
			{Name: "means", Pass: true, Judge: &core.JudgeUsage{Calls: 3, InputTokens: 1000, OutputTokens: 100, Model: "judge-model"}},
		},
		JudgeTotal: &core.JudgeUsage{Calls: 3, InputTokens: 1000, OutputTokens: 100},
	}
	if err := Price(&rep, judgePricing); err != nil {
		t.Fatalf("Price: %v", err)
	}
	var withBuf bytes.Buffer
	if err := (htmlReporter{}).Report(rep, &withBuf); err != nil {
		t.Fatalf("Report(with judge): %v", err)
	}
	out := withBuf.String()
	for _, want := range []string{`class="judge"`, `class="judge-total"`, "judge-model", "0.0045"} {
		if !strings.Contains(out, want) {
			t.Errorf("html missing %q, got:\n%s", want, out)
		}
	}

	clean := core.RunReport{Total: 1, Passed: 1, Scenarios: []core.ScenarioResult{{Name: "plain", Pass: true}}}
	var cleanBuf bytes.Buffer
	if err := (htmlReporter{}).Report(clean, &cleanBuf); err != nil {
		t.Fatalf("Report(clean): %v", err)
	}
	if strings.Contains(cleanBuf.String(), `class="judge`) {
		t.Errorf("clean report rendered a judge section, got:\n%s", cleanBuf.String())
	}
}

// TestJudgeCost pins the priced-function rules, mirroring the SUT-cost contract:
// no calls or no pricing table → 0 with no error; a table present with a real call
// but an empty (ambiguous) or unknown model → a hard error (never $0 for a real call).
func TestJudgeCost(t *testing.T) {
	t.Parallel()
	table := core.Pricing{"judge-model": {InputPerMTok: 3, OutputPerMTok: 15}}
	tests := []struct {
		name     string
		usage    core.JudgeUsage
		pricing  core.Pricing
		wantErr  bool
		errSub   string
		wantCost float64
	}{
		{name: "no calls is free", usage: core.JudgeUsage{}, pricing: table, wantCost: 0},
		{name: "no pricing table is zero cost", usage: core.JudgeUsage{Calls: 1, InputTokens: 1000, OutputTokens: 100, Model: "judge-model"}, pricing: nil, wantCost: 0},
		{name: "known model prices", usage: core.JudgeUsage{Calls: 3, InputTokens: 1000, OutputTokens: 100, Model: "judge-model"}, pricing: table, wantCost: wantJudgeCost},
		{name: "unknown model with a table is a hard error", usage: core.JudgeUsage{Calls: 1, InputTokens: 1000, OutputTokens: 100, Model: "mystery"}, pricing: table, wantErr: true, errSub: "mystery"},
		{name: "empty model with a table is an ambiguous hard error", usage: core.JudgeUsage{Calls: 1, InputTokens: 1000, OutputTokens: 100}, pricing: table, wantErr: true, errSub: "ambiguous"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := JudgeCost(tt.usage, tt.pricing)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				if !strings.Contains(err.Error(), tt.errSub) {
					t.Errorf("error %q missing %q", err, tt.errSub)
				}
				return
			}
			if math.Abs(got-tt.wantCost) > 1e-9 {
				t.Errorf("JudgeCost = %v, want ~%v", got, tt.wantCost)
			}
		})
	}
}

// budgetUsage builds a judge usage whose cost, under budgetPricing, is exactly
// inTokens/1e6*10 USD (output rate is 0). 2000 input tokens => $0.02.
func budgetUsage(inTokens int64) *core.JudgeUsage {
	return &core.JudgeUsage{Calls: 1, InputTokens: inTokens, Model: "judge-model"}
}

var budgetPricing = core.Pricing{"judge-model": {InputPerMTok: 10}}

// TestBudget_TripsAndNamesScenario proves the post-scenario budget accounts only
// completed calls, checks after each scenario, and on the scenario that crosses the
// ceiling returns an error naming the spent amount, the budget, and that scenario
// (judge-ledger budget contract). Earlier under-budget scenarios do not trip.
func TestBudget_TripsAndNamesScenario(t *testing.T) {
	t.Parallel()
	b := NewBudget(0.01, budgetPricing) // 1-cent ceiling

	// $0.005 — under budget, no trip.
	if err := b.Add(core.ScenarioResult{Name: "cheap", Judge: budgetUsage(500)}); err != nil {
		t.Fatalf("cheap scenario tripped early: %v", err)
	}
	// cumulative $0.005 + $0.02 = $0.025 > $0.01 — this scenario crosses it.
	err := b.Add(core.ScenarioResult{Name: "expensive", Judge: budgetUsage(2000)})
	if err == nil {
		t.Fatal("expected the budget to trip on the expensive scenario, got nil")
	}
	for _, want := range []string{"expensive", "0.0250", "0.0100"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("trip error %q should name %q (spent/budget/scenario)", err, want)
		}
	}
	if b.Err() == nil {
		t.Error("Budget.Err() is nil after a trip, want the trip error retained")
	}
	if math.Abs(b.Spent()-0.025) > 1e-9 {
		t.Errorf("Spent() = %v, want ~0.025", b.Spent())
	}
}

// TestBudget_ContinuesAccountingAfterTrip proves that once the budget trips on one
// scenario, a later scenario whose judge call ALSO completed still has its cost folded
// into Spent() (the "completed calls" accounting must not underreport actual usage),
// while Err() keeps the ORIGINAL trip error unchanged — the first cause is retained,
// never overwritten by a scenario that slips through after the abort signal.
func TestBudget_ContinuesAccountingAfterTrip(t *testing.T) {
	t.Parallel()
	b := NewBudget(0.01, budgetPricing) // 1-cent ceiling

	// $0.02 > $0.01 — trips here, naming this scenario.
	tripErr := b.Add(core.ScenarioResult{Name: "tripper", Judge: budgetUsage(2000)})
	if tripErr == nil {
		t.Fatal("expected the budget to trip on the tripper scenario, got nil")
	}

	// A later scenario that ALSO completed its judge call: its $0.01 must still land in
	// Spent() even though the budget already tripped.
	if err := b.Add(core.ScenarioResult{Name: "later", Judge: budgetUsage(1000)}); err == nil {
		t.Fatal("Add after a trip returned nil, want the retained trip error")
	}
	if math.Abs(b.Spent()-0.03) > 1e-9 {
		t.Errorf("Spent() = %v, want ~0.03 (later scenario's $0.01 still accounted after the trip)", b.Spent())
	}
	// Err() retains the ORIGINAL (tripper) error unchanged — not overwritten by "later".
	if got := b.Err(); got == nil || got.Error() != tripErr.Error() {
		t.Errorf("Err() = %v, want the original trip error %v (unchanged)", got, tripErr)
	}
	if !strings.Contains(b.Err().Error(), "tripper") {
		t.Errorf("Err() %q should still name the original scenario tripper", b.Err())
	}
}

// TestBudget_CleanPaths covers the non-tripping and hard-error paths: an unlimited
// budget (max 0) never accounts or trips; a scenario with no judge call contributes
// nothing; and an ambiguous/unknown model is a hard error even under a budget.
func TestBudget_CleanPaths(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		max      float64
		sr       core.ScenarioResult
		wantErr  bool
		errSub   string
		disabled bool // max <= 0: Add must not account — Spent()==0 and Err()==nil after Add
	}{
		{
			name:     "unlimited budget never trips even on a costly scenario",
			max:      0,
			sr:       core.ScenarioResult{Name: "big", Judge: budgetUsage(1_000_000)},
			disabled: true,
		},
		{
			name: "scenario with no judge call contributes nothing",
			max:  0.01,
			sr:   core.ScenarioResult{Name: "no-judge"},
		},
		{
			name:    "ambiguous model under a budget is a hard error",
			max:     0.01,
			sr:      core.ScenarioResult{Name: "ambiguous", Judge: &core.JudgeUsage{Calls: 1, InputTokens: 100}},
			wantErr: true,
			errSub:  "ambiguous",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := NewBudget(tt.max, budgetPricing)
			err := b.Add(tt.sr)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errSub) {
				t.Errorf("error %q missing %q", err, tt.errSub)
			}
			if tt.disabled {
				// Disabled-budget contract: max <= 0 does no accounting at all.
				if b.Spent() != 0 {
					t.Errorf("Spent() = %v, want 0 (disabled budget must not account)", b.Spent())
				}
				if b.Err() != nil {
					t.Errorf("Err() = %v, want nil (disabled budget never trips)", b.Err())
				}
			}
		})
	}
}

// TestPriceNamesScenarioOnBadModel proves that when Price cannot price a scenario's
// judge usage it fails with the offending scenario named (so the operator can fix
// the pricing table), never a silent $0 for a real call.
func TestPriceNamesScenarioOnBadModel(t *testing.T) {
	t.Parallel()
	rep := core.RunReport{Scenarios: []core.ScenarioResult{
		{Name: "priced-ok", Pass: true, Judge: &core.JudgeUsage{Calls: 1, InputTokens: 10, OutputTokens: 1, Model: "judge-model"}},
		{Name: "unpriceable", Pass: true, Judge: &core.JudgeUsage{Calls: 1, InputTokens: 10, OutputTokens: 1, Model: "mystery"}},
	}}
	err := Price(&rep, judgePricing)
	if err == nil {
		t.Fatal("Price returned nil, want a hard error for the unknown judge model")
	}
	if !strings.Contains(err.Error(), "unpriceable") || !strings.Contains(err.Error(), "mystery") {
		t.Errorf("error %q should name the scenario (unpriceable) and the model (mystery)", err)
	}
}

// TestReportRendersDerivationNote proves the audit-A8 note stays visible in the
// rendered artifacts (research R5 / constitution IV: surfaced, not swallowed). A
// scenario carrying a DerivationNote must show it in BOTH the JSON and the HTML
// report; a clean scenario must not clutter either output with it.
func TestReportRendersDerivationNote(t *testing.T) {
	t.Parallel()

	// A slice of the note that survives HTML escaping (no quotes/angle brackets),
	// so one substring works for both renderers.
	const noteFragment = "missing service.name"
	const note = `sequence unavailable for run "r1": sequence: span[0] ("fetch") ` + noteFragment

	withNote := core.RunReport{Total: 1, Passed: 1, Scenarios: []core.ScenarioResult{
		{Name: "degraded", Pass: true, DerivationNote: note},
	}}
	clean := core.RunReport{Total: 1, Passed: 1, Scenarios: []core.ScenarioResult{
		{Name: "healthy", Pass: true},
	}}

	tests := []struct {
		name     string
		reporter core.Reporter
	}{
		{"json", jsonReporter{}},
		{"html", htmlReporter{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var withBuf bytes.Buffer
			if err := tt.reporter.Report(withNote, &withBuf); err != nil {
				t.Fatalf("Report(withNote): %v", err)
			}
			if !strings.Contains(withBuf.String(), noteFragment) {
				t.Errorf("%s output does not render DerivationNote; want it to contain %q, got:\n%s",
					tt.name, noteFragment, withBuf.String())
			}

			var cleanBuf bytes.Buffer
			if err := tt.reporter.Report(clean, &cleanBuf); err != nil {
				t.Fatalf("Report(clean): %v", err)
			}
			if strings.Contains(cleanBuf.String(), noteFragment) {
				t.Errorf("%s output rendered a note for a clean scenario; got:\n%s", tt.name, cleanBuf.String())
			}
		})
	}
}
