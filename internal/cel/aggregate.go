package cel

import (
	"fmt"
	"math"
	"reflect"
	"sort"

	celgo "github.com/google/cel-go/cel"
	"github.com/google/cel-go/common"
	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

// VarRuns is the list binding the aggregate grammar evaluates over: one record
// per run of an @runs(N) scenario.
const VarRuns = "runs"

// AggregateEngine is a CEL environment for cross-run assertions. It declares the
// `runs` list plus the aggregate macros and their backing functions.
type AggregateEngine struct{ env *celgo.Env }

// AggregateProgram is a type-checked aggregate expression whose result is bool.
type AggregateProgram struct {
	expr string
	prg  celgo.Program
}

// NewAggregateEngine builds the aggregate CEL environment.
func NewAggregateEngine() (*AggregateEngine, error) {
	opts := []celgo.EnvOption{
		celgo.Variable(VarRuns, celgo.ListType(celgo.DynType)),
	}
	opts = append(opts, aggFuncs()...)
	opts = append(opts, aggMacros()...)
	env, err := celgo.NewEnv(opts...)
	if err != nil {
		return nil, fmt.Errorf("cel: building aggregate environment: %w", err)
	}
	return &AggregateEngine{env: env}, nil
}

// Compile type-checks expr; the result type must be bool (no silent fallback).
func (e *AggregateEngine) Compile(expr string) (*AggregateProgram, error) {
	ast, iss := e.env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("cel: compiling aggregate %q: %w", expr, iss.Err())
	}
	if !ast.OutputType().IsExactType(celgo.BoolType) {
		return nil, fmt.Errorf("cel: aggregate %q must evaluate to bool, got %s", expr, ast.OutputType())
	}
	prg, err := e.env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("cel: building aggregate program for %q: %w", expr, err)
	}
	return &AggregateProgram{expr: expr, prg: prg}, nil
}

// Eval runs the program against bound vars (must include "runs").
func (p *AggregateProgram) Eval(vars map[string]any) (bool, error) {
	out, _, err := p.prg.Eval(vars)
	if err != nil {
		return false, fmt.Errorf("cel: evaluating aggregate %q: %w", p.expr, err)
	}
	b, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("cel: aggregate %q did not return bool, got %T", p.expr, out.Value())
	}
	return b, nil
}

// toFloats converts a CEL list value into a []float64.
func toFloats(v ref.Val) ([]float64, error) {
	out, err := v.ConvertToNative(reflect.TypeOf([]float64{}))
	if err != nil {
		return nil, fmt.Errorf("cel: aggregate list is not numeric: %w", err)
	}
	fs, ok := out.([]float64)
	if !ok {
		return nil, fmt.Errorf("cel: aggregate list: ConvertToNative returned unexpected type %T", out)
	}
	return fs, nil
}

// aggFuncs registers the list-reducing functions the macros expand into.
func aggFuncs() []celgo.EnvOption {
	listDouble := celgo.ListType(celgo.DoubleType)
	unary := func(name string, fn func([]float64) float64) celgo.EnvOption {
		return celgo.Function(name, celgo.Overload(name+"_list",
			[]*celgo.Type{listDouble}, celgo.DoubleType,
			celgo.UnaryBinding(func(v ref.Val) ref.Val {
				xs, err := toFloats(v)
				if err != nil {
					return types.WrapErr(err)
				}
				if len(xs) == 0 {
					return types.NewErr("cel: %s over an empty sample", name)
				}
				return types.Double(fn(xs))
			})))
	}
	return []celgo.EnvOption{
		unary("__sum__", sum),
		unary("__mean__", mean),
		unary("__min__", minOf),
		unary("__max__", maxOf),
		unary("__stddev__", stddev),
		celgo.Function("__percentile__", celgo.Overload("__percentile___list_double",
			[]*celgo.Type{listDouble, celgo.DoubleType}, celgo.DoubleType,
			celgo.BinaryBinding(func(l, r ref.Val) ref.Val {
				xs, err := toFloats(l)
				if err != nil {
					return types.WrapErr(err)
				}
				if len(xs) == 0 {
					return types.NewErr("cel: percentile over an empty sample")
				}
				q, ok := r.Value().(float64)
				if !ok {
					return types.NewErr("cel: percentile quantile must be double, got %T", r.Value())
				}
				return types.Double(percentile(xs, q))
			}))),
	}
}

func sum(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s
}

func mean(xs []float64) float64 { return sum(xs) / float64(len(xs)) }

func minOf(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs {
		if x < m {
			m = x
		}
	}
	return m
}

func maxOf(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs {
		if x > m {
			m = x
		}
	}
	return m
}

func stddev(xs []float64) float64 {
	m := mean(xs)
	var ss float64
	for _, x := range xs {
		d := x - m
		ss += d * d
	}
	return math.Sqrt(ss / float64(len(xs)))
}

// percentile is nearest-rank (no interpolation): rank = ceil(q*N), 1-indexed.
func percentile(xs []float64, q float64) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	switch {
	case q <= 0:
		return s[0]
	case q >= 1:
		return s[len(s)-1]
	}
	rank := int(math.Ceil(q * float64(len(s))))
	return s[rank-1]
}

// aggMacros registers the aggregate readability macros. Each is a global macro
// over the implicit `runs` binding; they expand into ordinary comprehensions (the
// same machinery CEL's own filter/map use) plus the backing __*__ functions.
func aggMacros() []celgo.EnvOption {
	return []celgo.EnvOption{
		celgo.Macros(
			celgo.GlobalMacro("count", 2, countExpander),
			celgo.GlobalMacro("rate", 2, rateExpander),
		),
	}
}

// iterVar extracts the iteration-variable name from a macro's first argument.
func iterVar(eh celgo.MacroExprFactory, arg ast.Expr) (string, *common.Error) {
	if arg.Kind() != ast.IdentKind {
		return "", eh.NewError(arg.ID(), "first argument must be an identifier (e.g. r)")
	}
	return arg.AsIdent(), nil
}

// filterList builds `runs.filter(<v>, <pred>)` as a comprehension yielding a list.
func filterList(eh celgo.MacroExprFactory, v string, pred ast.Expr) ast.Expr {
	accu := eh.AccuIdentName()
	init := eh.NewList()
	cond := eh.NewLiteral(types.True)
	step := eh.NewCall(operators.Add, eh.NewAccuIdent(), eh.NewList(eh.NewIdent(v)))
	step = eh.NewCall(operators.Conditional, pred, step, eh.NewAccuIdent())
	return eh.NewComprehension(eh.NewIdent(VarRuns), v, accu, init, cond, step, eh.NewAccuIdent())
}

func countExpander(eh celgo.MacroExprFactory, _ ast.Expr, args []ast.Expr) (ast.Expr, *common.Error) {
	v, err := iterVar(eh, args[0])
	if err != nil {
		return nil, err
	}
	return eh.NewCall("size", filterList(eh, v, args[1])), nil
}

func rateExpander(eh celgo.MacroExprFactory, _ ast.Expr, args []ast.Expr) (ast.Expr, *common.Error) {
	v, err := iterVar(eh, args[0])
	if err != nil {
		return nil, err
	}
	num := eh.NewCall("double", eh.NewCall("size", filterList(eh, v, args[1])))
	den := eh.NewCall("double", eh.NewCall("size", eh.NewIdent(VarRuns)))
	return eh.NewCall(operators.Divide, num, den), nil
}
