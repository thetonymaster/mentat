package cel

import (
	"testing"

	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
)

// Spike: prove we can (a) reach the top-level comparison, (b) build a runnable
// program from the computed operand sub-expr, (c) recover the macro call + its args.
func TestSpike_SubProgramAndMacroRecovery(t *testing.T) {
	eng, err := NewAggregateEngine()
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	checked, iss := eng.env.Compile(`rate(r, !r.failed) >= 0.8`)
	if iss != nil && iss.Err() != nil {
		t.Fatalf("compile: %v", iss.Err())
	}
	native := checked.NativeRep()

	// (a) top-level comparison
	root := native.Expr()
	if root.Kind() != ast.CallKind || root.AsCall().FunctionName() != operators.GreaterEquals {
		t.Fatalf("want top-level %s, got kind=%v fn=%q", operators.GreaterEquals, root.Kind(), callFn(root))
	}
	args := root.AsCall().Args()
	if len(args) != 2 {
		t.Fatalf("want 2 args, got %d", len(args))
	}

	// (b) which operand references `runs`
	computed, expected := args[0], args[1]
	if !refsRuns(computed) || refsRuns(expected) {
		t.Fatalf("operand classification wrong")
	}

	// (c) build a program from the computed operand and eval it
	prg, err := subProgram(eng.env, native, computed)
	if err != nil {
		t.Fatalf("subProgram: %v", err)
	}
	records := []any{
		map[string]any{"failed": false},
		map[string]any{"failed": true},
	}
	out, _, err := prg.Eval(map[string]any{VarRuns: records})
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got, ok := out.Value().(float64); !ok || got != 0.5 {
		t.Fatalf("want rate 0.5, got %v (%T)", out.Value(), out.Value())
	}

	// (d) recover the macro call + its projection argument
	mc, ok := native.SourceInfo().GetMacroCall(computed.ID())
	if !ok || mc.AsCall().FunctionName() != "rate" {
		t.Fatalf("macro recovery failed: ok=%v fn=%q", ok, callFn(mc))
	}
	if got := len(mc.AsCall().Args()); got != 2 {
		t.Fatalf("want 2 macro args, got %d", got)
	}
}
