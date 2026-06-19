package cel

import (
	"testing"
)

func evalAgg(t *testing.T, expr string, vars map[string]any) bool {
	t.Helper()
	eng, err := NewAggregateEngine()
	if err != nil {
		t.Fatalf("NewAggregateEngine: %v", err)
	}
	prg, err := eng.Compile(expr)
	if err != nil {
		t.Fatalf("Compile(%q): %v", expr, err)
	}
	got, err := prg.Eval(vars)
	if err != nil {
		t.Fatalf("Eval(%q): %v", expr, err)
	}
	return got
}

func TestAggregateBackingFunctions(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want bool
	}{
		{"sum", "__sum__([1.0, 2.0, 3.0]) == 6.0", true},
		{"mean", "__mean__([2.0, 4.0]) == 3.0", true},
		{"min", "__min__([5.0, 1.0, 3.0]) == 1.0", true},
		{"max", "__max__([5.0, 1.0, 3.0]) == 5.0", true},
		{"percentile nearest-rank p95 of 1..10 is 10", "__percentile__([1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0,9.0,10.0], 0.95) == 10.0", true},
		{"percentile p50 of 1..10 is 5 (ceil(0.5*10)=5)", "__percentile__([1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0,9.0,10.0], 0.5) == 5.0", true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := evalAgg(t, tt.expr, map[string]any{"runs": []any{}}); got != tt.want {
				t.Fatalf("%q = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

func TestAggregateStddev(t *testing.T) {
	// population stddev of [2,4,4,4,5,5,7,9] is 2.0
	expr := "__stddev__([2.0,4.0,4.0,4.0,5.0,5.0,7.0,9.0]) == 2.0"
	if !evalAgg(t, expr, map[string]any{"runs": []any{}}) {
		t.Fatalf("stddev mismatch")
	}
}

func TestAggregateNonBoolRejected(t *testing.T) {
	eng, err := NewAggregateEngine()
	if err != nil {
		t.Fatalf("NewAggregateEngine: %v", err)
	}
	if _, err := eng.Compile("__sum__([1.0])"); err == nil {
		t.Fatalf("expected non-bool expression to be rejected")
	}
}

func TestAggregateEmptySampleErrors(t *testing.T) {
	eng, err := NewAggregateEngine()
	if err != nil {
		t.Fatalf("NewAggregateEngine: %v", err)
	}
	tests := []struct {
		name string
		expr string
	}{
		{"sum empty", "__sum__([]) == 0.0"},
		{"percentile empty", "__percentile__([], 0.5) == 0.0"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			prg, cErr := eng.Compile(tt.expr)
			if cErr != nil {
				t.Fatalf("Compile(%q): %v", tt.expr, cErr)
			}
			if _, eErr := prg.Eval(map[string]any{"runs": []any{}}); eErr == nil {
				t.Fatalf("%q: expected eval error for empty sample, got nil", tt.expr)
			}
		})
	}
}

func runsFixture() map[string]any {
	// 4 runs; "search" present in 3; latencyMs 100,200,300,400.
	mk := func(tools []any, lat int64, failed bool) map[string]any {
		return map[string]any{"tools": tools, "latencyMs": lat, "failed": failed}
	}
	return map[string]any{"runs": []any{
		mk([]any{"search", "summarize"}, 100, false),
		mk([]any{"summarize"}, 200, false),
		mk([]any{"search"}, 300, false),
		mk([]any{"search", "summarize"}, 400, false),
	}}
}

func TestRateCountMacros(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want bool
	}{
		{"count present", `count(r, "search" in r.tools) == 3`, true},
		{"count absent predicate", `count(r, r.failed) == 0`, true},
		{"rate threshold met", `rate(r, "search" in r.tools) >= 0.75`, true},
		{"rate threshold missed", `rate(r, "search" in r.tools) >= 0.8`, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := evalAgg(t, tt.expr, runsFixture()); got != tt.want {
				t.Fatalf("%q = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}
