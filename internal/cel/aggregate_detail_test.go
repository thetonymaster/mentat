package cel

import (
	"testing"

	"github.com/google/cel-go/common/ast"
)

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
