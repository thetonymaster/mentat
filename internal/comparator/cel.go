package comparator

import (
	"context"
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
	mu       sync.RWMutex
	programs map[string]*celengine.Program
}

// NewCEL returns the standalone, trace-aware CEL comparator (Name() == "cel").
// It consumes full Evidence — unlike result, which is contractually output-only.
func NewCEL() core.Comparator {
	engine, err := celengine.NewEngine()
	if err != nil {
		// The schema is a compile-time constant; a build failure is a true,
		// caller-unreachable invariant violation, not a runtime condition.
		panic(fmt.Sprintf("cel: static schema failed to build: %v", err))
	}
	return &celComparator{engine: engine, programs: map[string]*celengine.Program{}}
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
	vars, err := bindVars(refs, ev)
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

// bindVars binds ONLY the referenced variables, so a variable an expression does
// not mention is never computed. Trace aggregates and body JSON are added in
// later tasks.
func bindVars(refs []string, ev core.Evidence) (map[string]any, error) {
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
		default:
			return nil, fmt.Errorf("cel: unknown variable %q in references", name)
		}
	}
	return vars, nil
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
