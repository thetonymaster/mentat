package cel

import (
	"fmt"

	celgo "github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
)

// refsRuns reports whether expr's subtree references the `runs` identifier.
func refsRuns(expr ast.Expr) bool {
	nav := ast.NavigateAST(ast.NewAST(expr, nil))
	for _, n := range ast.MatchDescendants(nav, func(e ast.NavigableExpr) bool {
		return e.Kind() == ast.IdentKind && e.AsIdent() == VarRuns
	}) {
		_ = n
		return true
	}
	// the root itself may be the ident
	return expr.Kind() == ast.IdentKind && expr.AsIdent() == VarRuns
}

// shape holds the pieces of the canonical aggregate comparison
// "‹macro›(r, proj) ‹op› ‹runs-free const›" after normalization.
type shape struct {
	op       string   // normalized so computed is on the left, e.g. ">="
	macro    string   // "rate", "p95", ...
	computed ast.Expr // the runs-referencing operand
	expected ast.Expr // the runs-free operand
	iterVar  string   // iteration variable name from the macro, e.g. "r"
	proj     ast.Expr // the projection/predicate argument of the macro
}

// knownMacros is the aggregate macro set whose projection we can recover.
var knownMacros = map[string]bool{
	"rate": true, "count": true, "sum": true, "mean": true, "min": true,
	"max": true, "stddev": true, "p50": true, "p95": true, "p99": true,
}

// flipOp maps a comparison operator to its mirror, so an "expected OP computed"
// expression normalizes to "computed flip(OP) expected".
var flipOp = map[string]string{
	operators.Greater:       "<",
	operators.GreaterEquals: "<=",
	operators.Less:          ">",
	operators.LessEquals:    ">=",
	operators.Equals:        "==",
	operators.NotEquals:     "!=",
}

// readOp maps the internal operator name to its display form (computed on left).
var readOp = map[string]string{
	operators.Greater:       ">",
	operators.GreaterEquals: ">=",
	operators.Less:          "<",
	operators.LessEquals:    "<=",
	operators.Equals:        "==",
	operators.NotEquals:     "!=",
}

// analyze recognizes the canonical "‹macro›(r, proj) ‹op› ‹runs-free const›" shape
// (either operand order) and returns its pieces. (nil, false) for anything else.
// This is a legitimate classification outcome — not a silent fallback.
func analyze(src *ast.AST) (*shape, bool) {
	root := src.Expr()
	if root.Kind() != ast.CallKind {
		return nil, false
	}
	fn := root.AsCall().FunctionName()
	if _, isCmp := readOp[fn]; !isCmp {
		return nil, false
	}
	args := root.AsCall().Args()
	if len(args) != 2 {
		return nil, false
	}
	left, right := args[0], args[1]
	lRuns, rRuns := refsRuns(left), refsRuns(right)
	var computed, expected ast.Expr
	var op string
	switch {
	case lRuns && !rRuns:
		computed, expected, op = left, right, readOp[fn]
	case rRuns && !lRuns:
		computed, expected, op = right, left, flipOp[fn]
	default:
		return nil, false // both or neither reference runs
	}
	mc, ok := src.SourceInfo().GetMacroCall(computed.ID())
	if !ok || mc.Kind() != ast.CallKind || !knownMacros[mc.AsCall().FunctionName()] {
		return nil, false // computed side is not a single recognized macro call
	}
	margs := mc.AsCall().Args()
	if len(margs) != 2 || margs[0].Kind() != ast.IdentKind {
		return nil, false
	}
	return &shape{
		op:       op,
		macro:    mc.AsCall().FunctionName(),
		computed: computed,
		expected: expected,
		iterVar:  margs[0].AsIdent(),
		proj:     margs[1],
	}, true
}

// subProgram compiles a single sub-expression of src into a runnable program,
// re-checking it in env so typed functions resolve. Primary path: native AST ->
// proto -> cel.Ast -> Program. See Task 1 spike for the fallback if this misbehaves.
func subProgram(env *celgo.Env, src *ast.AST, expr ast.Expr) (celgo.Program, error) {
	sub := ast.NewAST(expr, src.SourceInfo())
	pexpr, err := ast.ToProto(sub)
	if err != nil {
		return nil, fmt.Errorf("cel: sub-expr to proto: %w", err)
	}
	celAst := celgo.CheckedExprToAst(pexpr)
	checked, iss := env.Check(celAst)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("cel: re-checking sub-expr: %w", iss.Err())
	}
	prg, err := env.Program(checked)
	if err != nil {
		return nil, fmt.Errorf("cel: building sub-program: %w", err)
	}
	return prg, nil
}
