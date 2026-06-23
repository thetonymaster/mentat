package cel

import (
	"math"
	"testing"

	"github.com/google/cel-go/common/ast"
)

// equalFloats reports whether two float slices match within almostEqual's epsilon.
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

// almostEqual reports whether a and b are within 1e-9 of each other.
func almostEqual(a, b float64) bool {
	const eps = 1e-9
	return math.Abs(a-b) <= eps
}

// mustCompile type-checks expr in a fresh aggregate engine and returns its native AST.
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

// callFn returns the function name when e is a call expression, else "".
func callFn(e ast.Expr) string {
	if e != nil && e.Kind() == ast.CallKind {
		return e.AsCall().FunctionName()
	}
	return ""
}
