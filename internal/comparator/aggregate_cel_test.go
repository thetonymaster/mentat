package comparator

import (
	"context"
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
