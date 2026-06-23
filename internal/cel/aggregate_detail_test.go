package cel

import (
	"testing"
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

	tests := []struct {
		name           string
		expr           string
		wantCanonical  bool      // Detail ok: a single canonical "macro(r,proj) op const"
		wantMacro      string    // checked when wantCanonical
		wantOp         string    // normalized so computed is on the left
		assertComputed bool      // p95 interpolation is impl-defined; skip its computed value
		wantComputed   float64   // checked when assertComputed
		wantExpected   float64   // checked when wantCanonical
		wantPerRun     []float64 // checked when wantCanonical
	}{
		{
			// rate is a predicate macro: per-run is 1.0 for !r.failed, 0.0 otherwise.
			name: "rate predicate", expr: `rate(r, !r.failed) >= 0.8`,
			wantCanonical: true, wantMacro: "rate", wantOp: ">=",
			assertComputed: true, wantComputed: 2.0 / 3.0, wantExpected: 0.8,
			wantPerRun: []float64{1, 0, 1},
		},
		{
			// count(r, r.failed): only run[1] failed -> computed=1, per-run [0,1,0].
			name: "count predicate", expr: `count(r, r.failed) == 1`,
			wantCanonical: true, wantMacro: "count", wantOp: "==",
			assertComputed: true, wantComputed: 1.0, wantExpected: 1.0,
			wantPerRun: []float64{0, 1, 0},
		},
		{
			// mean is a numeric macro: per-run is the raw projected cost values.
			name: "mean numeric", expr: `mean(r, r.cost) < 0.05`,
			wantCanonical: true, wantMacro: "mean", wantOp: "<",
			assertComputed: true, wantComputed: (0.01 + 0.05 + 0.02) / 3.0, wantExpected: 0.05,
			wantPerRun: []float64{0.01, 0.05, 0.02},
		},
		{
			// reversed operands: the literal on the left normalizes op to "<=".
			// per-run are the raw latencyMs values (double() coerces int64).
			name: "p95 reversed", expr: `1500 >= p95(r, r.latencyMs)`,
			wantCanonical: true, wantMacro: "p95", wantOp: "<=",
			assertComputed: false, wantExpected: 1500,
			wantPerRun: []float64{100, 300, 200},
		},
		{
			// count over an int64-field predicate: latencyMs > 150 holds for 300 and
			// 200 -> computed=2, per-run [0,1,1]. Exercises the int64 projection path.
			name: "count int64 field", expr: `count(r, r.latencyMs > 150) == 2`,
			wantCanonical: true, wantMacro: "count", wantOp: "==",
			assertComputed: true, wantComputed: 2.0, wantExpected: 2.0,
			wantPerRun: []float64{0, 1, 1},
		},
		{
			// a compound (&&) expression is not a single canonical comparison.
			name: "non-canonical -> not ok",
			expr: `rate(r, !r.failed) >= 0.8 && p95(r, r.latencyMs) <= 1500`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := eng.Compile(tt.expr)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			d, ok, err := p.Detail(vars)
			if err != nil {
				t.Fatalf("Detail err=%v", err)
			}
			if ok != tt.wantCanonical {
				t.Fatalf("Detail ok=%v, want %v", ok, tt.wantCanonical)
			}
			if !tt.wantCanonical {
				return
			}
			if d.Macro != tt.wantMacro || d.Op != tt.wantOp {
				t.Errorf("macro/op = %q/%q, want %q/%q", d.Macro, d.Op, tt.wantMacro, tt.wantOp)
			}
			if tt.assertComputed && !almostEqual(d.Computed, tt.wantComputed) {
				t.Errorf("computed = %v, want %v", d.Computed, tt.wantComputed)
			}
			if !almostEqual(d.Expected, tt.wantExpected) {
				t.Errorf("expected = %v, want %v", d.Expected, tt.wantExpected)
			}
			if !equalFloats(d.PerRun, tt.wantPerRun) {
				t.Errorf("perRun = %v, want %v", d.PerRun, tt.wantPerRun)
			}
		})
	}
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
		// != is symmetric: a literal-on-the-left "!=" still normalizes to "!=" with
		// the macro on the computed side.
		{"reversed not-equals", `0 != count(r, r.failed)`, true, "count", "!="},
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
