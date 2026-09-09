package report

import (
	"time"

	"github.com/thetonymaster/mentat/internal/core"
)

// Fixtures for the report-format golden (spec 010, FR-013). They are FIXED values, not
// captured from a live run: every field is literal, so the rendered bytes are byte-stable
// without any normalization pass. None of the report types contains a map, so there is no
// iteration-order nondeterminism either — TestReportFormatGoldenIsDeterministic proves it
// rather than assuming it.
//
// Between them the two fixtures exercise every `omitempty` field on BOTH sides. That
// pairing is the point: dropping an `omitempty` is invisible when the field is set (the key
// renders either way) and only shows up when it is absent. A fixture that sets everything
// would prove nothing about the tags.
const (
	goldenFixtureModel = "claude-sonnet-5"
	goldenFixtureNote  = "sequence derivation incomplete: span missing service.name"
)

// goldenFixtureStart is the one value that would otherwise vary per run. Fixing it here
// (rather than normalizing it after the fact) keeps the golden a plain byte comparison.
var goldenFixtureStart = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// fullFixture is the rich case: a completed run (Interrupted absent) carrying a suite
// judge total (JudgeTotal present), with four scenarios that between them set and leave
// unset every per-scenario tagged field.
func fullFixture() core.RunReport {
	return core.RunReport{
		Scenarios: []core.ScenarioResult{
			// 1. Plain pass — every optional field absent. This row is what catches a
			// dropped `omitempty` on FeatureFile, Qualifiers, DerivationNote and Judge.
			{
				Name: "green-plain",
				Pass: true,
				Cost: 0.0125,
			},
			// 2. Every optional field present.
			{
				Name:        "green-qualified",
				FeatureFile: "features/budget.feature",
				Tags:        []string{"@smoke", "@budget"},
				Pass:        true,
				Qualifiers:  []string{qualifierText},
				Cost:        0.0250,
				Sequence:    []string{"search", "fetch", "summarize"},
				Judge: &core.JudgeUsage{
					Calls: 2, InputTokens: 1400, OutputTokens: 260,
					CostUsd: 0.0071, Model: goldenFixtureModel,
				},
			},
			// 3. Failing scenario with reasons, an aggregate detail and a derivation note.
			{
				Name:           "red-aggregate",
				FeatureFile:    "features/aggregate.feature",
				Pass:           false,
				Reasons:        []string{"rate = 0.50, want >= 0.80"},
				Cost:           0.0075,
				Aggregate:      &core.AggregateDetail{Expr: "rate(r, pass) >= 0.80", Macro: "rate", Op: ">=", Computed: 0.5, Expected: 0.8, PerRun: []float64{1, 0}},
				DerivationNote: goldenFixtureNote,
			},
			// 4. Multi-run scenario — the per-run table the HTML reporter renders.
			{
				Name:     "multi-run",
				Pass:     true,
				Cost:     0.0400,
				Sequence: []string{"search"},
				Runs: []core.RunRecord{
					{RunID: "run-0001", Passed: true, LatencyMS: 1240, Cost: 0.0200},
					{RunID: "run-0002", Passed: false, FailureKind: "budget", LatencyMS: 980, Cost: 0.0200},
				},
			},
		},
		Total:     4,
		Passed:    3,
		Failed:    1,
		TotalCost: 0.0850,
		StartedAt: goldenFixtureStart,
		Duration:  1500 * time.Millisecond,
		JudgeTotal: &core.JudgeUsage{
			Calls: 2, InputTokens: 1400, OutputTokens: 260, CostUsd: 0.0071,
			// Model intentionally empty: the suite total is not attributed to one
			// model, so `omitempty` must drop the key.
		},
	}
}

// interruptedFixture is the complement: a run cut short (Interrupted PRESENT) that made no
// judge call (JudgeTotal ABSENT). Together with fullFixture this covers both suite-level
// tags in both states — neither report can do it alone.
func interruptedFixture() core.RunReport {
	return core.RunReport{
		Scenarios: []core.ScenarioResult{
			{Name: "green-before-signal", Pass: true, Cost: 0.0100},
		},
		Total:       1,
		Passed:      1,
		Failed:      0,
		TotalCost:   0.0100,
		StartedAt:   goldenFixtureStart,
		Duration:    250 * time.Millisecond,
		Interrupted: true,
	}
}
