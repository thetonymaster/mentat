package comparator

import (
	"context"
	"fmt"
	"strings"
	"sync"

	celengine "github.com/thetonymaster/mentat/internal/cel"
	"github.com/thetonymaster/mentat/internal/core"
)

// AggregateCELExpectation carries one boolean CEL expression over the `runs` sample.
type AggregateCELExpectation struct {
	Expr string
}

type aggregateCEL struct {
	engine   *celengine.AggregateEngine
	pricing  core.Pricing
	mu       sync.RWMutex
	programs map[string]*celengine.AggregateProgram
}

// NewAggregateCEL returns the cross-run CEL comparator (Name() == "aggregate-cel").
func NewAggregateCEL(pricing core.Pricing) core.AggregateComparator {
	eng, err := celengine.NewAggregateEngine()
	if err != nil {
		panic(fmt.Sprintf("aggregate-cel: static env failed to build: %v", err))
	}
	return &aggregateCEL{engine: eng, pricing: pricing, programs: map[string]*celengine.AggregateProgram{}}
}

func (c *aggregateCEL) Name() string { return "aggregate-cel" }

// Compile type-checks and caches expr at scenario-init (so a malformed expression
// fails before any SUT is driven). Safe for concurrent scenarios.
func (c *aggregateCEL) Compile(expr string) error {
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

func (c *aggregateCEL) program(expr string) (*celengine.AggregateProgram, error) {
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

// Aggregate builds one record per run, binds `runs`, and evaluates expr.
func (c *aggregateCEL) Aggregate(_ context.Context, evs []core.Evidence, e core.Expectation) (core.Verdict, error) {
	exp, ok := e.(AggregateCELExpectation)
	if !ok {
		return core.Verdict{}, fmt.Errorf("aggregate-cel: expectation must be AggregateCELExpectation, got %T", e)
	}
	prg, err := c.program(exp.Expr)
	if err != nil {
		return core.Verdict{}, err
	}
	fields := prg.Fields()
	records := make([]any, 0, len(evs))
	for i, ev := range evs {
		rec, err := c.record(ev, fields)
		if err != nil {
			return core.Verdict{}, fmt.Errorf("aggregate-cel: run %d (%s): %w", i, ev.RunID, err)
		}
		records = append(records, rec)
	}
	pass, err := prg.Eval(map[string]any{celengine.VarRuns: records})
	if err != nil {
		return core.Verdict{}, err
	}
	if pass {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{aggregateReason(exp.Expr, evs)}}, nil
}

// record builds the per-run CEL map. Boundary fields are always present; each
// trace-derived field is bound only when the expression references it (cost/services
// hard-error on absent data, so binding them unconditionally would break agent runs).
func (c *aggregateCEL) record(ev core.Evidence, fields map[string]bool) (map[string]any, error) {
	rec := map[string]any{
		"runId":       ev.RunID,
		"failed":      ev.Failed,
		"failureKind": ev.FailureKind,
		"status":      int64(ev.Output.Status),
		"exitCode":    int64(ev.Output.ExitCode),
		"bodyText":    string(ev.Output.Body),
		"answer":      ev.Output.Answer,
	}
	if ev.Failed || ev.Trace == nil {
		// Provide empty lists so membership checks (e.g. "x" in r.tools) return false
		// rather than erroring on absent keys. Numeric fields (latencyMs, cost, tokens)
		// are intentionally absent — accessing them on a failed run is a hard error.
		rec["tools"] = []any{}
		rec["services"] = []any{}
		return rec, nil
	}
	if fields["tokens"] {
		n, err := tokenSum(ev.Trace)
		if err != nil {
			return nil, fmt.Errorf("binding tokens: %w", err)
		}
		rec["tokens"] = int64(n)
	}
	if fields["cost"] {
		v, err := costSum(ev.Trace, c.pricing)
		if err != nil {
			return nil, fmt.Errorf("binding cost: %w", err)
		}
		rec["cost"] = v
	}
	if fields["errors"] {
		rec["errors"] = int64(errorCount(ev.Trace))
	}
	if fields["latencyMs"] {
		rec["latencyMs"] = ev.Trace.Envelope().Milliseconds()
	}
	if fields["tools"] {
		tools, err := toolSequence(ev.Trace)
		if err != nil {
			return nil, fmt.Errorf("binding tools: %w", err)
		}
		rec["tools"] = toAnyList(tools)
	}
	if fields["services"] {
		svcs, err := serviceSequence(ev.Trace)
		if err != nil {
			return nil, fmt.Errorf("binding services: %w", err)
		}
		rec["services"] = toAnyList(svcs)
	}
	return rec, nil
}

func toAnyList(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// aggregateReason renders the failing expression plus a compact per-run table so a
// reader can copy a test.run.id into /traces to inspect the offending run (§8).
func aggregateReason(expr string, evs []core.Evidence) string {
	var b strings.Builder
	fmt.Fprintf(&b, "aggregate false: %s  (%d runs)\n", expr, len(evs))
	fmt.Fprintf(&b, "  run  test.run.id            failed  kind\n")
	for i, ev := range evs {
		fmt.Fprintf(&b, "  %-3d  %-22s %-7t %s\n", i, ev.RunID, ev.Failed, ev.FailureKind)
	}
	return strings.TrimRight(b.String(), "\n")
}
