package cel

import (
	"fmt"

	celgo "github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
	"github.com/google/cel-go/common/types"
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

// detailPlan holds the three cached sub-programs for a canonical aggregate comparison.
type detailPlan struct {
	macro       string
	op          string
	computedPrg celgo.Program
	expectedPrg celgo.Program
	perRunPrg   celgo.Program // runs.map(iterVar, double(proj)) -> list<double>
}

// buildDetailPlan returns a plan for a canonical aggregate comparison, or (nil, nil)
// when the expression is not canonical. A genuine sub-program build failure on a
// canonical expression is a hard error (invariant 4).
func buildDetailPlan(env *celgo.Env, src *ast.AST) (*detailPlan, error) {
	sh, ok := analyze(src)
	if !ok {
		return nil, nil
	}
	computedPrg, err := subProgram(env, src, sh.computed)
	if err != nil {
		return nil, fmt.Errorf("building computed sub-program: %w", err)
	}
	expectedPrg, err := subProgram(env, src, sh.expected)
	if err != nil {
		return nil, fmt.Errorf("building expected sub-program: %w", err)
	}
	perRunExpr := buildPerRunMap(sh.iterVar, sh.proj, sh.macro)
	// The perRunExpr is freshly constructed (all IDs=0); use nil SourceInfo to avoid
	// conflicts with the original expression's type/ref tables keyed by ID.
	perRunPrg, err := subProgram(env, nil, perRunExpr)
	if err != nil {
		return nil, fmt.Errorf("building per-run sub-program: %w", err)
	}
	return &detailPlan{
		macro:       sh.macro,
		op:          sh.op,
		computedPrg: computedPrg,
		expectedPrg: expectedPrg,
		perRunPrg:   perRunPrg,
	}, nil
}

// predicateMacros identifies macros whose projection is a bool predicate rather than a
// numeric expression; these require bool-to-double coercion via a conditional.
var predicateMacros = map[string]bool{
	"count": true,
	"rate":  true,
}

// buildPerRunMap constructs a `runs.map(iterVar, <elem>)` comprehension yielding
// list<double>. For predicate macros (count/rate) the element is `proj ? 1.0 : 0.0`;
// for numeric macros the element is `double(proj)`.
// Each node receives a distinct ID so the type-checker's per-ID maps don't collide.
// proj is deep-copied and renumbered so its IDs don't collide with the skeleton IDs
// (1–9) or with the original expression's type/ref tables.
func buildPerRunMap(iterVar string, proj ast.Expr, macro string) ast.Expr {
	fac := ast.NewExprFactory()
	accu := fac.AccuIdentName()

	// Deep-copy proj so that renumbering doesn't mutate the original AST.
	projCopy := fac.CopyExpr(proj)
	// Renumber proj's sub-tree starting at ID 100, safely above the skeleton range.
	nextID := int64(100)
	projCopy.RenumberIDs(func(_ int64) int64 {
		id := nextID
		nextID++
		return id
	})

	// Choose the element expression based on whether the macro projection is a predicate.
	var elem ast.Expr
	if predicateMacros[macro] {
		// proj ? 1.0 : 0.0  — coerce bool to double without a missing overload.
		elem = fac.NewCall(3, operators.Conditional, projCopy,
			fac.NewLiteral(10, types.Double(1.0)),
			fac.NewLiteral(11, types.Double(0.0)),
		)
	} else {
		elem = fac.NewCall(3, "double", projCopy)
	}

	// IDs 1–9 for the comprehension skeleton nodes.
	init := fac.NewList(1, []ast.Expr{}, []int32{})
	cond := fac.NewLiteral(2, types.True)
	step := fac.NewCall(4, operators.Add, fac.NewAccuIdent(5), fac.NewList(6, []ast.Expr{elem}, []int32{}))
	return fac.NewComprehension(7,
		fac.NewIdent(8, VarRuns),
		iterVar, accu,
		init, cond, step,
		fac.NewAccuIdent(9),
	)
}

// subProgram compiles a single sub-expression into a runnable program, re-checking it
// in env so typed functions resolve. src may be nil for freshly constructed expressions
// that carry no existing source info (all IDs=0). Primary path: native AST -> proto ->
// cel.Ast -> Program. See Task 1 spike for the fallback if this misbehaves.
func subProgram(env *celgo.Env, src *ast.AST, expr ast.Expr) (celgo.Program, error) {
	var srcInfo *ast.SourceInfo
	if src != nil {
		srcInfo = src.SourceInfo()
	}
	sub := ast.NewAST(expr, srcInfo)
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
