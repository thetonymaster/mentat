package cel

import (
	"math"
	"testing"

	"github.com/google/cel-go/common/ast"
)

func TestAggregateProgram_Detail(t *testing.T) {
	eng, err := NewAggregateEngine()
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	records := []any{
		map[string]any{"failed": false, "latencyMs": int64(100), "cost": 0.01},
		map[string]any{"failed": true, "latencyMs": int64(300), "cost": 0.05},
		map[string]any{"failed": false, "latencyMs": int64(200), "cost": 0.02},
	}
	vars := map[string]any{VarRuns: records}

	t.Run("rate", func(t *testing.T) {
		p, err := eng.Compile(`rate(r, !r.failed) >= 0.8`)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		d, ok, err := p.Detail(vars)
		if err != nil || !ok {
			t.Fatalf("Detail ok=%v err=%v", ok, err)
		}
		if d.Macro != "rate" || d.Op != ">=" {
			t.Errorf("macro/op = %q/%q", d.Macro, d.Op)
		}
		if !almostEqual(d.Computed, 2.0/3.0) || !almostEqual(d.Expected, 0.8) {
			t.Errorf("computed/expected = %v/%v", d.Computed, d.Expected)
		}
		// rate: !r.failed -> 1.0 for not-failed, 0.0 for failed
		if want := []float64{1, 0, 1}; !equalFloats(d.PerRun, want) {
			t.Errorf("perRun = %v, want %v", d.PerRun, want)
		}
	})

	// count is a predicate macro: proj is bool, per-run should be 1.0 for matching, 0.0 otherwise.
	t.Run("count predicate", func(t *testing.T) {
		p, err := eng.Compile(`count(r, r.failed) == 1`)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		d, ok, err := p.Detail(vars)
		if err != nil || !ok {
			t.Fatalf("Detail ok=%v err=%v", ok, err)
		}
		if d.Macro != "count" || d.Op != "==" {
			t.Errorf("macro/op = %q/%q", d.Macro, d.Op)
		}
		// count(r, r.failed): only run[1] has failed=true -> computed=1, expected=1
		if !almostEqual(d.Computed, 1.0) || !almostEqual(d.Expected, 1.0) {
			t.Errorf("computed/expected = %v/%v", d.Computed, d.Expected)
		}
		// per-run: 1.0 for r.failed=true, 0.0 for r.failed=false
		if want := []float64{0, 1, 0}; !equalFloats(d.PerRun, want) {
			t.Errorf("perRun = %v, want %v", d.PerRun, want)
		}
	})

	// mean is a numeric macro: per-run should be the actual projected values.
	t.Run("mean numeric", func(t *testing.T) {
		p, err := eng.Compile(`mean(r, r.cost) < 0.05`)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		d, ok, err := p.Detail(vars)
		if err != nil || !ok {
			t.Fatalf("Detail ok=%v err=%v", ok, err)
		}
		if d.Macro != "mean" || d.Op != "<" {
			t.Errorf("macro/op = %q/%q", d.Macro, d.Op)
		}
		// mean(0.01, 0.05, 0.02) = 0.08/3
		wantComputed := (0.01 + 0.05 + 0.02) / 3.0
		if !almostEqual(d.Computed, wantComputed) || !almostEqual(d.Expected, 0.05) {
			t.Errorf("computed/expected = %v/%v", d.Computed, d.Expected)
		}
		// per-run: the raw cost values
		if want := []float64{0.01, 0.05, 0.02}; !equalFloats(d.PerRun, want) {
			t.Errorf("perRun = %v, want %v", d.PerRun, want)
		}
	})

	t.Run("p95 reversed", func(t *testing.T) {
		p, err := eng.Compile(`1500 >= p95(r, r.latencyMs)`)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		d, ok, err := p.Detail(vars)
		if err != nil || !ok {
			t.Fatalf("Detail ok=%v err=%v", ok, err)
		}
		if d.Op != "<=" || d.Expected != 1500 {
			t.Errorf("op/expected = %q/%v", d.Op, d.Expected)
		}
		// per-run: the raw latencyMs values (as float64 since double() coerces int64)
		if want := []float64{100, 300, 200}; !equalFloats(d.PerRun, want) {
			t.Errorf("perRun = %v, want %v", d.PerRun, want)
		}
	})

	t.Run("non-canonical -> ok=false", func(t *testing.T) {
		p, err := eng.Compile(`rate(r, !r.failed) >= 0.8 && p95(r, r.latencyMs) <= 1500`)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		_, ok, err := p.Detail(vars)
		if err != nil || ok {
			t.Fatalf("want ok=false err=nil, got ok=%v err=%v", ok, err)
		}
	})
}

func equalFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !almostEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func almostEqual(a, b float64) bool {
	const eps = 1e-9
	return math.Abs(a-b) <= eps
}

func mustCompile(t *testing.T, expr string) *ast.AST {
	t.Helper()
	eng, err := NewAggregateEngine()
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	checked, iss := eng.env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		t.Fatalf("compile %q: %v", expr, iss.Err())
	}
	return checked.NativeRep()
}

func TestBuildDetailPlan(t *testing.T) {
	eng, err := NewAggregateEngine()
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	tests := []struct {
		name     string
		expr     string
		wantPlan bool
	}{
		{"canonical", `p95(r, r.latencyMs) <= 1500`, true},
		{"non-canonical", `rate(r, !r.failed) >= 0.8 && p95(r, r.latencyMs) <= 1500`, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			p, err := eng.Compile(tt.expr)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if (p.plan != nil) != tt.wantPlan {
				t.Fatalf("plan present = %v, want %v", p.plan != nil, tt.wantPlan)
			}
		})
	}
}

func TestAnalyze(t *testing.T) {
	tests := []struct {
		name      string
		expr      string
		wantOK    bool
		wantMacro string
		wantOp    string
	}{
		{"rate ge literal", `rate(r, !r.failed) >= 0.8`, true, "rate", ">="},
		{"p95 le literal", `p95(r, r.latencyMs) <= 1500`, true, "p95", "<="},
		{"count eq zero", `count(r, r.failed) == 0`, true, "count", "=="},
		{"reversed operands", `0.8 <= rate(r, !r.failed)`, true, "rate", ">="},
		{"mean lt", `mean(r, r.cost) < 0.01`, true, "mean", "<"},
		{"compound and", `rate(r, !r.failed) >= 0.8 && p95(r, r.latencyMs) <= 1500`, false, "", ""},
		{"no comparison", `rate(r, !r.failed) > 0.5 ? true : false`, false, "", ""},
		{"composite computed", `mean(r, r.cost) + mean(r, r.tokens) <= 1.0`, false, "", ""},
		{"both sides runs", `mean(r, r.cost) <= mean(r, r.tokens)`, false, "", ""},
		{"neither side runs", `1 <= 2`, false, "", ""},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			sh, ok := analyze(mustCompile(t, tt.expr))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if sh.macro != tt.wantMacro {
				t.Errorf("macro = %q, want %q", sh.macro, tt.wantMacro)
			}
			if sh.op != tt.wantOp {
				t.Errorf("op = %q, want %q", sh.op, tt.wantOp)
			}
			if !refsRuns(sh.computed) || refsRuns(sh.expected) {
				t.Errorf("operand classification wrong")
			}
		})
	}
}
