package comparator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	celengine "github.com/thetonymaster/mentat/internal/cel"
	"github.com/thetonymaster/mentat/internal/core"
)

// CELExpectation carries a single boolean CEL expression over a run's Evidence.
type CELExpectation struct {
	Expr string
}

type celComparator struct {
	engine   *celengine.Engine
	pricing  core.Pricing
	mu       sync.RWMutex
	programs map[string]*celengine.Program
}

// NewCEL returns the standalone, trace-aware CEL comparator (Name() == "cel").
// pricing is reused by the `cost` variable so it derives identically to budgets
// (§5, single source of truth).
func NewCEL(pricing core.Pricing) core.Comparator {
	engine, err := celengine.NewEngine()
	if err != nil {
		panic(fmt.Sprintf("cel: static schema failed to build: %v", err))
	}
	return &celComparator{engine: engine, pricing: pricing, programs: map[string]*celengine.Program{}}
}

func (c *celComparator) Name() string { return "cel" }

// Compile type-checks and caches expr's program. It is called at scenario-init
// (§7) so a malformed expression fails before any SUT is driven. Safe for
// concurrent scenarios.
func (c *celComparator) Compile(expr string) error {
	c.mu.RLock()
	_, ok := c.programs[expr]
	c.mu.RUnlock()
	if ok {
		return nil
	}
	prg, err := c.engine.Compile(expr)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.programs[expr] = prg
	c.mu.Unlock()
	return nil
}

func (c *celComparator) program(expr string) (*celengine.Program, error) {
	c.mu.RLock()
	prg, ok := c.programs[expr]
	c.mu.RUnlock()
	if ok {
		return prg, nil
	}
	if err := c.Compile(expr); err != nil {
		return nil, err
	}
	c.mu.RLock()
	prg = c.programs[expr]
	c.mu.RUnlock()
	return prg, nil
}

// Compare binds only the schema variables the expression references (§6),
// evaluates the program, and on a false result reports the expression plus a
// snapshot of the referenced bound values (§9).
func (c *celComparator) Compare(_ context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error) {
	exp, ok := e.(CELExpectation)
	if !ok {
		return core.Verdict{}, fmt.Errorf("cel: expectation must be CELExpectation, got %T", e)
	}
	prg, err := c.program(exp.Expr)
	if err != nil {
		return core.Verdict{}, err
	}
	refs := prg.References()
	vars, err := bindVars(refs, ev, c.pricing)
	if err != nil {
		return core.Verdict{}, err
	}
	pass, err := prg.Eval(vars)
	if err != nil {
		return core.Verdict{}, err
	}
	if pass {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{reason(exp.Expr, refs, vars)}}, nil
}

// traceVars are the schema variables that require ev.Trace.
var traceVars = map[string]bool{
	celengine.VarTokens:    true,
	celengine.VarCost:      true,
	celengine.VarErrors:    true,
	celengine.VarLatencyMs: true,
	celengine.VarTools:     true,
	celengine.VarServices:  true,
}

// bindVars binds ONLY the referenced variables, so a variable an expression does
// not mention is never computed. Trace aggregates and body JSON are added in
// later tasks.
func bindVars(refs []string, ev core.Evidence, pricing core.Pricing) (map[string]any, error) {
	for _, name := range refs {
		if traceVars[name] && ev.Trace == nil {
			return nil, fmt.Errorf("cel: binding %q: evidence has no trace", name)
		}
	}
	vars := make(map[string]any, len(refs))
	for _, name := range refs {
		switch name {
		case celengine.VarStatus:
			vars[name] = int64(ev.Output.Status)
		case celengine.VarExitCode:
			vars[name] = int64(ev.Output.ExitCode)
		case celengine.VarBodyText:
			vars[name] = string(ev.Output.Body)
		case celengine.VarAnswer:
			vars[name] = ev.Output.Answer
		case celengine.VarTokens:
			n, err := tokenSum(ev.Trace)
			if err != nil {
				return nil, fmt.Errorf("cel: binding tokens: %w", err)
			}
			vars[name] = int64(n)
		case celengine.VarCost:
			v, err := costSum(ev.Trace, pricing)
			if err != nil {
				return nil, fmt.Errorf("cel: binding cost: %w", err)
			}
			vars[name] = v
		case celengine.VarErrors:
			vars[name] = int64(errorCount(ev.Trace))
		case celengine.VarLatencyMs:
			vars[name] = ev.Trace.Envelope().Milliseconds() // already int64
		case celengine.VarTools:
			seq, err := toolSequence(ev.Trace)
			if err != nil {
				return nil, fmt.Errorf("cel: binding tools: %w", err)
			}
			vars[name] = seq
		case celengine.VarServices:
			seq, err := serviceSequence(ev.Trace)
			if err != nil {
				return nil, fmt.Errorf("cel: binding services: %w", err)
			}
			vars[name] = seq
		case celengine.VarBody:
			v, err := parseBody(ev.Output.Body)
			if err != nil {
				return nil, err
			}
			vars[name] = v
		default:
			return nil, fmt.Errorf("cel: unknown variable %q in references", name)
		}
	}
	return vars, nil
}

// parseBody implements §6: body is parsed as JSON only when referenced.
//
//	empty   → null (binds to nil)
//	valid   → parsed value (dyn)
//	invalid → hard, descriptive error (never a guessed empty object)
//
// JSON numbers decode to float64 (CEL double); compare with a double literal
// (body.count == 3.0) or int(body.count) == 3.
func parseBody(body []byte) (any, error) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("cel: response body is not valid JSON: %w", err)
	}
	return v, nil
}

// reason renders the §9 failure message: the expression plus a snapshot of the
// referenced bound values, in the deterministic (sorted) reference order.
func reason(expr string, refs []string, vars map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cel false: %q", expr)
	if len(refs) > 0 {
		b.WriteString("  [")
		for i, name := range refs {
			if i > 0 {
				b.WriteString(" ")
			}
			fmt.Fprintf(&b, "%s=%v", name, vars[name])
		}
		b.WriteString("]")
	}
	return b.String()
}
