package cel

import (
	"fmt"
	"math"
	"reflect"
	"sort"

	celgo "github.com/google/cel-go/cel"
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
	return out.([]float64), nil
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
	if rank < 1 {
		rank = 1
	}
	if rank > len(s) {
		rank = len(s)
	}
	return s[rank-1]
}

// aggMacros is implemented in Task 3 and Task 4; start with an empty set so the
// engine compiles. Replace this stub when adding the macros.
func aggMacros() []celgo.EnvOption { return nil }
