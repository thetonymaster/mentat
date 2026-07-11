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
	boundaryRef := fields["status"] || fields["exitCode"] || fields["bodyText"] || fields["answer"]
	records := make([]any, 0, len(evs))
	for i, ev := range evs {
		// R4.3 (A6): an expression referencing a boundary field over a driver-failed
		// sample (no real Output) is a hard error — never a fabricated zero.
		if boundaryRef && !hasRealOutput(ev) {
			detail := ev.FailureMsg
			if detail == "" {
				detail = ev.FailureKind
			}
			return core.Verdict{}, fmt.Errorf("aggregate-cel: run %d (%s) has no boundary output (driver failed: %s); guard with r.failed or fix the run", i, ev.RunID, detail)
		}
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
	detail, err := buildCoreDetail(prg, exp.Expr, records)
	if err != nil {
		return core.Verdict{}, fmt.Errorf("aggregate-cel: %w", err)
	}
	if pass {
		return core.Verdict{Pass: true, Detail: detail}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{aggregateReason(exp.Expr, evs, detail)}, Detail: detail}, nil
}

// hasRealOutput reports whether the sample carries a real driver Output: a run that
// did not fail, or one that failed at resolve (the driver succeeded, Output retained).
// A driver-failed sample has no real Output (R4.3, A6).
func hasRealOutput(ev core.Evidence) bool {
	return !ev.Failed || ev.FailureKind == core.FailureKindResolve
}

// record builds the per-run CEL map. runId/failed/failureKind are always bound so
// r.failed guards work over any sample. Boundary fields (status/exitCode/bodyText/
// answer) are bound only for samples with a real Output — never fabricated for a
// driver-failed sample (the caller guards references to them). Each trace-derived
// field is bound only when the expression references it (cost/services hard-error on
// absent data, so binding them unconditionally would break agent runs).
func (c *aggregateCEL) record(ev core.Evidence, fields map[string]bool) (map[string]any, error) {
	rec := map[string]any{
		"runId":       ev.RunID,
		"failed":      ev.Failed,
		"failureKind": ev.FailureKind,
	}
	if hasRealOutput(ev) {
		rec["status"] = int64(ev.Output.Status)
		rec["exitCode"] = int64(ev.Output.ExitCode)
		rec["bodyText"] = string(ev.Output.Body)
		rec["answer"] = ev.Output.Answer
	}
	if ev.Failed || ev.Trace == nil {
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

// aggregateReason renders the failing assertion. With a canonical Detail it shows
// computed-vs-expected and a per-run value column; otherwise it shows the raw
// expression. Both include a per-run table so a test.run.id can be pasted into /traces.
func aggregateReason(expr string, evs []core.Evidence, d *core.AggregateDetail) string {
	var b strings.Builder
	if d != nil {
		fmt.Fprintf(&b, "aggregate false: %s = %.2f, want %s %.2f  (%d runs)\n", d.Macro, d.Computed, d.Op, d.Expected, len(evs))
		fmt.Fprintf(&b, "  run  test.run.id            failed  kind     value\n")
		for i, ev := range evs {
			val := ""
			if i < len(d.PerRun) {
				val = fmt.Sprintf("%g", d.PerRun[i])
			}
			fmt.Fprintf(&b, "  %-3d  %-22s %-7t %-8s %s\n", i, ev.RunID, ev.Failed, ev.FailureKind, val)
		}
		return strings.TrimRight(b.String(), "\n")
	}
	fmt.Fprintf(&b, "aggregate false: %s  (%d runs)\n", expr, len(evs))
	fmt.Fprintf(&b, "  run  test.run.id            failed  kind\n")
	for i, ev := range evs {
		fmt.Fprintf(&b, "  %-3d  %-22s %-7t %s\n", i, ev.RunID, ev.Failed, ev.FailureKind)
	}
	return strings.TrimRight(b.String(), "\n")
}

// buildCoreDetail runs the program's canonical-aggregate analysis and maps the
// cel-local Detail to core.AggregateDetail. Returns (nil, nil) for non-canonical.
func buildCoreDetail(prg *celengine.AggregateProgram, expr string, records []any) (*core.AggregateDetail, error) {
	d, ok, err := prg.Detail(map[string]any{celengine.VarRuns: records})
	if err != nil {
		return nil, fmt.Errorf("evaluating detail for %q: %w", expr, err)
	}
	if !ok {
		return nil, nil
	}
	return &core.AggregateDetail{
		Expr: expr, Macro: d.Macro, Op: d.Op,
		Computed: d.Computed, Expected: d.Expected, PerRun: d.PerRun,
	}, nil
}
