package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/expectations"
	"github.com/thetonymaster/mentat/internal/registry"
)

// Engine wires configuration, a trace store, and a correlator into the
// Drive/Comparator lifecycle. Build is the only way to construct it.
type Engine struct {
	cfg      config.Config
	cor      core.Correlator
	st       core.TraceStore
	sems     map[string]chan struct{} // per-target concurrency gate
	pinned   string                   // when set, Drive resolves this run id instead of driving
	pricing  core.Pricing
	patterns expectations.Patterns
}

// Pricing returns the per-model cost table wired at Build (may be nil).
func (e *Engine) Pricing() core.Pricing { return e.pricing }

// ShapePattern resolves a named sidecar shape pattern loaded at Build. The bool is false
// for an unknown name; the step layer pre-checks names in sc.Before so this is a safety net.
func (e *Engine) ShapePattern(name string) ([]comparator.ShapeExpectation, bool) {
	return e.patterns.Get(name)
}

// PinRun makes subsequent Drive calls resolve runID from the store instead of
// running the SUT — used by `mentatctl agent replay` to re-evaluate a stored run.
//
// Invariant: PinRun MUST be called before any concurrent Drive (i.e. at single-threaded
// composition/setup time, the same discipline the registry uses — populated before
// concurrent scenario execution begins). No mutex is needed because this is a setup-time
// operation, not a concurrent one.
func (e *Engine) PinRun(runID string) { e.pinned = runID }

// Comparator resolves a named comparator from the global registry.
func (e *Engine) Comparator(name string) (core.Comparator, bool) {
	return registry.Comparator(name)
}

// AggregateComparator resolves a named aggregate comparator from the registry.
func (e *Engine) AggregateComparator(name string) (core.AggregateComparator, bool) {
	return registry.AggregateComparator(name)
}

// Drive injects the run tag, runs the SUT, then resolves and merges its trace.
// When PinRun was called, it resolves the pinned run id without driving.
func (e *Engine) Drive(ctx context.Context, target string, args []string) (core.Evidence, error) {
	if e.pinned != "" {
		tr, err := e.cor.Resolve(ctx, e.st, e.pinned)
		if err != nil {
			return core.Evidence{}, fmt.Errorf("engine: resolve pinned run %q: %w", e.pinned, err)
		}
		return core.Evidence{RunID: e.pinned, Trace: tr}, nil
	}
	ev, err := e.driveOnce(ctx, target, args)
	if err != nil {
		return core.Evidence{}, err
	}
	return ev, nil
}

// driveOnce performs one live drive. On a harness failure it returns an Evidence
// flagged Failed (with RunID + FailureKind) AND the wrapped error, so single-run
// Drive can surface the error while multi-run DriveN can record the sample.
// Structural errors (unknown target/adapter) carry an empty RunID.
func (e *Engine) driveOnce(ctx context.Context, target string, args []string) (core.Evidence, error) {
	t, ok := e.cfg.Targets[target]
	if !ok {
		return core.Evidence{}, fmt.Errorf("engine: unknown target %q", target)
	}
	drv, ok := registry.Driver(t.Adapter)
	if !ok {
		return core.Evidence{}, fmt.Errorf("engine: no driver for adapter %q", t.Adapter)
	}
	// The target's resolved run budget bounds this run (feature 003). KillGrace rides
	// the spec so the driver reaps the process tree without importing config. The run
	// context derives its deadline from the scenario context and the budget timeout,
	// so cancellation composes: whichever bound (scenario, budget, poll) fires first
	// wins. A non-positive Timeout means no per-run bound (a zero-value budget from a
	// hand-built config); "unbounded" is the explicit opt-out.
	budget := t.Budget
	spec := core.RunSpec{
		Target:  target,
		Adapter: t.Adapter,
		Command: append(append([]string{}, t.Command...), args...),
		HTTP: core.HTTPSpec{
			URL:     t.HTTP.URL,
			Method:  t.HTTP.Method,
			Headers: t.HTTP.Headers,
		},
		Env:       map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": e.cfg.OTLPEndpoint},
		KillGrace: budget.KillGrace,
	}
	runID := e.cor.Inject(ctx, &spec)

	runCtx := ctx
	if !budget.Unbounded && budget.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, budget.Timeout)
		defer cancel()
	}

	sem := e.sems[target]
	select {
	case sem <- struct{}{}:
	case <-runCtx.Done():
		werr := budgetTimeout(ctx, runCtx, target, "drive", budget)
		if werr == nil {
			werr = fmt.Errorf("engine: drive %q: %w", target, runCtx.Err())
		}
		return core.Evidence{RunID: runID, Failed: true, FailureKind: core.FailureKindDriver, FailureMsg: werr.Error()}, werr
	}
	defer func() { <-sem }()

	// If the batch/scenario was cancelled while we waited for the slot, do not start
	// the SUT — its result would be discarded (FR-008). Abort before driving.
	if runCtx.Err() != nil {
		werr := budgetTimeout(ctx, runCtx, target, "drive", budget)
		if werr == nil {
			werr = fmt.Errorf("engine: drive %q: %w", target, runCtx.Err())
		}
		return core.Evidence{RunID: runID, Failed: true, FailureKind: core.FailureKindDriver, FailureMsg: werr.Error()}, werr
	}

	res, err := drv.Run(runCtx, spec)
	if err != nil {
		werr := budgetTimeout(ctx, runCtx, target, "drive", budget)
		if werr == nil {
			werr = fmt.Errorf("engine: drive %q: %w", target, err)
		}
		return core.Evidence{RunID: runID, Failed: true, FailureKind: core.FailureKindDriver, FailureMsg: werr.Error()}, werr
	}
	tr, err := e.cor.Resolve(runCtx, e.st, runID)
	if err != nil {
		werr := budgetTimeout(ctx, runCtx, target, "resolve", budget)
		if werr == nil {
			werr = fmt.Errorf("engine: resolve run %q: %w", runID, err)
		}
		// Retain the real driver Output: the driver succeeded, only resolution failed.
		return core.Evidence{RunID: runID, Output: res.Output, Failed: true, FailureKind: core.FailureKindResolve, FailureMsg: werr.Error()}, werr
	}
	return core.Evidence{RunID: runID, Trace: tr, Output: res.Output}, nil
}

// budgetTimeout returns the FR-007 per-run-budget-timeout error for a phase
// (drive/resolve), or nil when the failure was NOT a budget timeout (the caller
// then wraps the underlying cause with its usual message). A budget timeout is when
// the derived run context passed its deadline while the parent scenario context is
// still live — a scenario/suite cancellation would also cancel the parent, and is
// reported as cancellation, not a run-budget timeout. DeadlineExceeded is wrapped so
// callers can still errors.Is the returned error.
func budgetTimeout(parent, run context.Context, target, phase string, budget config.RunBudget) error {
	if !errors.Is(run.Err(), context.DeadlineExceeded) || parent.Err() != nil {
		return nil
	}
	return fmt.Errorf("engine: %s %q: run timeout after %s (phase: %s): %w", phase, target, budget.Timeout, phase, context.DeadlineExceeded)
}

// DriveN runs the scenario n times and returns one Evidence per run. A harness
// failure on an iteration becomes a typed failed sample (not an aborted batch);
// a structural error aborts. Serial by default; parallel iterations each acquire
// the existing per-target semaphore, with results collected by index.
func (e *Engine) DriveN(ctx context.Context, target string, args []string, n int, parallel bool) ([]core.Evidence, error) {
	if n < 1 {
		return nil, fmt.Errorf("engine: DriveN needs n>=1, got %d", n)
	}
	if e.pinned != "" && n > 1 {
		return nil, fmt.Errorf("engine: cannot multi-run a pinned scenario (n=%d); replay is deterministic", n)
	}
	evs := make([]core.Evidence, n)
	collect := func(ctx context.Context, i int) error {
		ev, err := e.driveOnce(ctx, target, args)
		if err != nil && ev.RunID == "" {
			return err // structural error: abort
		}
		evs[i] = ev // success, or a typed failed sample (ev.Failed)
		return nil
	}
	if !parallel {
		for i := 0; i < n; i++ {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("engine: DriveN %q cancelled: %w", target, ctx.Err())
			}
			if err := collect(ctx, i); err != nil {
				return nil, err
			}
		}
		return evs, nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("engine: DriveN %q cancelled: %w", target, ctx.Err())
	}
	// Wrap the batch so the first structural error cancels iterations that have not
	// yet started driving (FR-008): driveOnce honours batchCtx at the semaphore and
	// just before driving. Iterations already driving finish (bounded by their run
	// budget) — the guarantee is "not yet started ⇒ never starts", not mid-flight kill.
	batchCtx, cancelBatch := context.WithCancel(ctx)
	defer cancelBatch()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var structErr error
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if batchCtx.Err() != nil {
				return // batch already doomed: never start this iteration
			}
			if err := collect(batchCtx, i); err != nil {
				mu.Lock()
				if structErr == nil {
					structErr = err
				}
				mu.Unlock()
				cancelBatch()
			}
		}(i)
	}
	wg.Wait()
	if structErr != nil {
		return nil, structErr
	}
	// Mirror the serial path: if the parent context was cancelled mid-batch (after
	// the pre-check, while goroutines ran) with no structural error, un-started
	// iterations left zero-value Evidence. Surface the cancellation rather than
	// returning a silent partial success (CLAUDE.md invariant #4). Check the parent
	// ctx, not batchCtx — a structural cancel of batchCtx already returned via structErr.
	if ctx.Err() != nil {
		return nil, fmt.Errorf("engine: DriveN %q cancelled: %w", target, ctx.Err())
	}
	return evs, nil
}
