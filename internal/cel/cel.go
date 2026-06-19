package cel

import (
	"fmt"
	"sort"

	celgo "github.com/google/cel-go/cel"
)

// Variable names declared in the CEL environment. They are the contract between
// the engine (which declares their CEL types) and the comparator (which binds
// their values from Evidence). Keep the two in sync via these constants.
const (
	VarStatus    = "status"
	VarExitCode  = "exitCode"
	VarBody      = "body"
	VarBodyText  = "bodyText"
	VarAnswer    = "answer"
	VarTokens    = "tokens"
	VarCost      = "cost"
	VarLatencyMs = "latencyMs"
	VarErrors    = "errors"
	VarTools     = "tools"
	VarServices  = "services"
)

// Engine is a compiled CEL environment carrying the Mentat variable schema (§5).
// It imports neither core nor trace: it owns only variable names + CEL types,
// which keeps it independently testable and reusable.
type Engine struct {
	env      *celgo.Env
	declared map[string]bool
}

// NewEngine builds the CEL environment with the declared variable schema.
func NewEngine() (*Engine, error) {
	env, err := celgo.NewEnv(
		celgo.Variable(VarStatus, celgo.IntType),
		celgo.Variable(VarExitCode, celgo.IntType),
		celgo.Variable(VarBody, celgo.DynType),
		celgo.Variable(VarBodyText, celgo.StringType),
		celgo.Variable(VarAnswer, celgo.StringType),
		celgo.Variable(VarTokens, celgo.IntType),
		celgo.Variable(VarCost, celgo.DoubleType),
		celgo.Variable(VarLatencyMs, celgo.IntType),
		celgo.Variable(VarErrors, celgo.IntType),
		celgo.Variable(VarTools, celgo.ListType(celgo.StringType)),
		celgo.Variable(VarServices, celgo.ListType(celgo.StringType)),
	)
	if err != nil {
		return nil, fmt.Errorf("cel: building environment: %w", err)
	}
	declared := map[string]bool{
		VarStatus: true, VarExitCode: true, VarBody: true, VarBodyText: true,
		VarAnswer: true, VarTokens: true, VarCost: true, VarLatencyMs: true,
		VarErrors: true, VarTools: true, VarServices: true,
	}
	return &Engine{env: env, declared: declared}, nil
}

// Program is a type-checked, compiled CEL expression whose result type is bool.
type Program struct {
	expr string
	prg  celgo.Program
	refs []string
}

// Compile type-checks expr against the schema. It returns a descriptive error on
// a syntax error, an unknown variable, a type error, or a non-bool result type.
func (e *Engine) Compile(expr string) (*Program, error) {
	ast, iss := e.env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("cel: compiling %q: %w", expr, iss.Err())
	}
	// Result type must be bool (no silent fallback — invariant 4).
	if !ast.OutputType().IsExactType(celgo.BoolType) {
		return nil, fmt.Errorf("cel: expression %q must evaluate to bool, got %s", expr, ast.OutputType())
	}
	refs, err := references(ast, e.declared)
	if err != nil {
		return nil, fmt.Errorf("cel: extracting references from %q: %w", expr, err)
	}
	prg, err := e.env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("cel: building program for %q: %w", expr, err)
	}
	return &Program{expr: expr, prg: prg, refs: refs}, nil
}

// references returns the declared schema variables the expression uses, sorted.
// It filters the checked AST's reference map to names present in the schema, so
// operators, macros, and function names are excluded.
func references(ast *celgo.Ast, declared map[string]bool) ([]string, error) {
	checked, err := celgo.AstToCheckedExpr(ast)
	if err != nil {
		return nil, fmt.Errorf("converting to checked expr: %w", err)
	}
	seen := map[string]bool{}
	for _, ref := range checked.GetReferenceMap() {
		if name := ref.GetName(); declared[name] {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// References returns the schema variables the expression actually uses (sorted),
// so the caller binds and parses only what is needed (§6).
func (p *Program) References() []string {
	out := make([]string, len(p.refs))
	copy(out, p.refs)
	return out
}

// Eval runs the program against a bound variable map and returns the bool result.
func (p *Program) Eval(vars map[string]any) (bool, error) {
	out, _, err := p.prg.Eval(vars)
	if err != nil {
		return false, fmt.Errorf("cel: evaluating %q: %w", p.expr, err)
	}
	b, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("cel: expression %q did not return bool, got %T", p.expr, out.Value())
	}
	return b, nil
}
