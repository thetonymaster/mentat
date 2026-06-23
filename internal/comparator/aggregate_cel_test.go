package comparator

import (
	"context"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// traceWithTools builds a minimal agent trace whose tool sequence is `tools`.
func traceWithTools(tools ...string) *trace.Trace {
	root := &trace.Span{Name: "invoke_agent", Attrs: map[string]string{genai.Op: genai.OpInvokeAgent}}
	spans := []*trace.Span{root}
	for _, tl := range tools {
		spans = append(spans, &trace.Span{Name: "tool " + tl, Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: tl}})
	}
	return &trace.Trace{Roots: []*trace.Span{root}, Spans: spans}
}

func evidence(tools ...string) core.Evidence {
	return core.Evidence{RunID: "r", Trace: traceWithTools(tools...), Output: core.Output{}}
}

func TestAggregateRateHappyPath(t *testing.T) {
	c := NewAggregateCEL(nil)
	evs := []core.Evidence{
		evidence("search", "summarize"),
		evidence("summarize"),
		evidence("search"),
		evidence("search", "summarize"),
	}
	tests := []struct {
		name     string
		expr     string
		wantPass bool
	}{
		{"rate met", `rate(r, "search" in r.tools) >= 0.75`, true},
		{"rate missed", `rate(r, "search" in r.tools) >= 0.8`, false},
		{"count failed zero", `count(r, r.failed) == 0`, true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			v, err := c.Aggregate(context.Background(), evs, AggregateCELExpectation{Expr: tt.expr})
			if err != nil {
				t.Fatalf("Aggregate: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Fatalf("Pass = %v, want %v (reasons %v)", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}

func TestAggregateReferenceGating(t *testing.T) {
	c := NewAggregateCEL(nil) // nil pricing
	evs := []core.Evidence{evidence("search"), evidence("search")}
	// tools-only expression must NOT compute cost/services (which would hard-error).
	v, err := c.Aggregate(context.Background(), evs, AggregateCELExpectation{Expr: `rate(r, "search" in r.tools) >= 0.9`})
	if err != nil {
		t.Fatalf("tools-only aggregate must not error on absent cost/services: %v", err)
	}
	if !v.Pass {
		t.Fatalf("expected pass, reasons=%v", v.Reasons)
	}
	// referencing cost with no pricing + no cost data must be a HARD error (no silent fallback).
	if _, err := c.Aggregate(context.Background(), evs, AggregateCELExpectation{Expr: `mean(r, r.cost) < 1.0`}); err == nil {
		t.Fatalf("expected a hard error binding cost when there is no pricing/cost data")
	}
}

func TestAggregateWrongExpectationType(t *testing.T) {
	c := NewAggregateCEL(nil)
	if _, err := c.Aggregate(context.Background(), nil, "nope"); err == nil {
		t.Fatalf("expected error for wrong expectation type")
	}
}

func failedEvidence(runID, kind string) core.Evidence {
	return core.Evidence{RunID: runID, Failed: true, FailureKind: kind}
}

func TestAggregateFailedSamples(t *testing.T) {
	c := NewAggregateCEL(nil)
	evs := []core.Evidence{
		evidence("search"),
		failedEvidence("r-bad", core.FailureKindResolve),
		evidence("search"),
	}

	// rate over r.failed works even though a run has no trace.
	v, err := c.Aggregate(context.Background(), evs, AggregateCELExpectation{Expr: `rate(r, r.failed) < 0.5`})
	if err != nil {
		t.Fatalf("Aggregate(rate failed): %v", err)
	}
	if !v.Pass {
		t.Fatalf("rate(r, r.failed) < 0.5 should pass with 1/3 failed")
	}

	// scoped metric skips the failed run via short-circuit &&.
	v, err = c.Aggregate(context.Background(), evs, AggregateCELExpectation{Expr: `rate(r, !r.failed && "search" in r.tools) >= 0.66`})
	if err != nil {
		t.Fatalf("Aggregate(scoped): %v", err)
	}
	if !v.Pass {
		t.Fatalf("scoped rate should pass: 2/3 runs have search")
	}

	// UNscoped metric over a failed run is a hard error (missing key), not a guess.
	if _, err := c.Aggregate(context.Background(), evs, AggregateCELExpectation{Expr: `mean(r, r.latencyMs) < 9999.0`}); err == nil {
		t.Fatalf("expected hard error for metric over a failed run")
	}
}

func TestAggregateReasonHasPerRunTable(t *testing.T) {
	c := NewAggregateCEL(nil)
	evs := []core.Evidence{evidence("summarize"), failedEvidence("r-2", core.FailureKindDriver)}
	v, err := c.Aggregate(context.Background(), evs, AggregateCELExpectation{Expr: `rate(r, !r.failed && "search" in r.tools) >= 0.9`})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if v.Pass {
		t.Fatalf("expected fail")
	}
	reason := v.Reasons[0]
	// canonical expression: new message shows computed-vs-expected + value column, not the raw expr.
	for _, sub := range []string{"r-2", "driver", "run", "value", "rate = ", "want >="} {
		if !strings.Contains(reason, sub) {
			t.Fatalf("reason %q missing %q", reason, sub)
		}
	}
}

func TestAggregateCELName(t *testing.T) {
	if n := NewAggregateCEL(nil).Name(); n != "aggregate-cel" {
		t.Fatalf("Name() = %q, want aggregate-cel", n)
	}
}

func TestAggregateCELCompileMalformed(t *testing.T) {
	c, ok := NewAggregateCEL(nil).(interface{ Compile(string) error })
	if !ok {
		t.Fatalf("aggregate-cel must expose Compile")
	}
	if err := c.Compile("this is not valid cel +++"); err == nil {
		t.Fatalf("expected a compile error for a malformed expression")
	}
}

func TestAggregateCEL_Detail(t *testing.T) {
	c := NewAggregateCEL(nil)
	evs := []core.Evidence{
		{RunID: "a", Output: core.Output{Status: 200}},
		{RunID: "b", Failed: true, FailureKind: core.FailureKindResolve},
		{RunID: "c", Output: core.Output{Status: 200}},
	}
	tests := []struct {
		name          string
		expr          string
		wantPass      bool
		wantDetailNil bool
		wantMacro     string
		wantOp        string
		wantExpected  float64
	}{
		// rate(r, !r.failed) = 2/3 ≈ 0.667; assertion >= 0.8 -> FAIL, but Detail still set.
		{"failing canonical carries detail", `rate(r, !r.failed) >= 0.8`, false, false, "rate", ">=", 0.8},
		// Passing canonical assertion also carries Detail.
		{"passing canonical carries detail", `rate(r, !r.failed) >= 0.5`, true, false, "rate", ">=", 0.5},
		// Non-canonical -> Detail nil.
		{"non-canonical -> nil detail", `count(r, r.failed) == 0 && count(r, !r.failed) == 3`, false, true, "", "", 0},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			v, err := c.Aggregate(context.Background(), evs, AggregateCELExpectation{Expr: tt.expr})
			if err != nil {
				t.Fatalf("aggregate: %v", err)
			}
			if tt.wantDetailNil {
				if v.Detail != nil {
					t.Errorf("want nil Detail, got %+v", v.Detail)
				}
				return
			}
			if v.Pass != tt.wantPass {
				t.Fatalf("Pass = %v, want %v (reasons %v)", v.Pass, tt.wantPass, v.Reasons)
			}
			if v.Detail == nil {
				t.Fatalf("want Detail populated")
			}
			if v.Detail.Macro != tt.wantMacro || v.Detail.Op != tt.wantOp || v.Detail.Expected != tt.wantExpected {
				t.Errorf("detail = %+v", v.Detail)
			}
			if v.Detail.Expr != tt.expr {
				t.Errorf("expr = %q, want %q", v.Detail.Expr, tt.expr)
			}
		})
	}
}

func TestAggregateReason(t *testing.T) {
	evs := []core.Evidence{
		{RunID: "aaa", Output: core.Output{Status: 200}},
		{RunID: "bbb", Failed: true, FailureKind: core.FailureKindResolve},
	}
	tests := []struct {
		name        string
		expr        string
		detail      *core.AggregateDetail
		wantContain []string
		wantAbsent  []string
	}{
		{
			name: "canonical -> computed-vs-expected + value column",
			expr: `rate(r, !r.failed) >= 0.8`,
			detail: &core.AggregateDetail{
				Expr: `rate(r, !r.failed) >= 0.8`, Macro: "rate", Op: ">=",
				Computed: 0.5, Expected: 0.8, PerRun: []float64{1, 0},
			},
			wantContain: []string{"rate = 0.50, want >= 0.80", "value"},
		},
		{
			name:        "nil detail -> legacy message",
			expr:        `count(r, r.failed) == 0 && true`,
			detail:      nil,
			wantContain: []string{"aggregate false:"},
			wantAbsent:  []string{"want"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := aggregateReason(tt.expr, evs, tt.detail)
			for _, sub := range tt.wantContain {
				if !strings.Contains(got, sub) {
					t.Errorf("missing %q:\n%s", sub, got)
				}
			}
			for _, sub := range tt.wantAbsent {
				if strings.Contains(got, sub) {
					t.Errorf("unexpected %q present:\n%s", sub, got)
				}
			}
		})
	}
}

func TestAggregateToolBindingError(t *testing.T) {
	// an execute_tool span with no tool name makes toolSequence hard-error when
	// `tools` is referenced — a binding error must propagate, not be swallowed.
	badTrace := &trace.Trace{
		Roots: []*trace.Span{{Name: "invoke_agent", Attrs: map[string]string{genai.Op: genai.OpInvokeAgent}}},
		Spans: []*trace.Span{
			{Name: "invoke_agent", Attrs: map[string]string{genai.Op: genai.OpInvokeAgent}},
			{Name: "tool", Attrs: map[string]string{genai.Op: genai.OpExecuteTool}}, // missing tool name
		},
	}
	c := NewAggregateCEL(nil)
	evs := []core.Evidence{{RunID: "r", Trace: badTrace}}
	if _, err := c.Aggregate(context.Background(), evs, AggregateCELExpectation{Expr: `count(r, "x" in r.tools) == 0`}); err == nil {
		t.Fatalf("expected a hard error from toolSequence on a tool span missing its name")
	}
}
